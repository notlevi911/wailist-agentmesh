package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPasswordAccountExists is returned when an OAuth login resolves to an email
// that already belongs to a password account. We refuse to silently link them,
// since our password signup does not verify email ownership — auto-linking would
// allow a pre-registered password account to capture a victim's OAuth identity.
var ErrPasswordAccountExists = errors.New("password account exists for email")

type Store struct {
	pool *pgxpool.Pool
}

func (s *Store) Close() {
	s.pool.Close()
}

// --- Workflow methods ---

func (s *Store) CreateWorkflow(ctx context.Context, name, userID string) (models.Workflow, error) {
	id := uuid.New().String()
	emptyGraph := `{"nodes":[],"edges":[]}`
	var w models.Workflow
	var graphJSON []byte
	var runEndpoint *string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflows (id, user_id, name, status, graph)
		VALUES ($1, $2, $3, 'draft', $4::jsonb)
		RETURNING id, user_id, name, status, graph, deployed_at, run_endpoint, created_at, updated_at
	`, id, userID, name, emptyGraph).Scan(
		&w.ID, &w.UserID, &w.Name, &w.Status, &graphJSON,
		&w.DeployedAt, &runEndpoint, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return w, err
	}
	if runEndpoint != nil {
		w.RunEndpoint = *runEndpoint
	}
	unmarshalGraph(graphJSON, &w)
	return w, nil
}

func (s *Store) GetWorkflow(ctx context.Context, id string) (models.Workflow, error) {
	var w models.Workflow
	var graphJSON []byte
	var runEndpoint *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, name, status, graph, deployed_at, run_endpoint, created_at, updated_at
		FROM workflows WHERE id = $1
	`, id).Scan(
		&w.ID, &w.UserID, &w.Name, &w.Status, &graphJSON,
		&w.DeployedAt, &runEndpoint, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return w, err
	}
	if runEndpoint != nil {
		w.RunEndpoint = *runEndpoint
	}
	unmarshalGraph(graphJSON, &w)
	return w, nil
}

func (s *Store) ListWorkflows(ctx context.Context, userID string) ([]models.Workflow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, status, graph, deployed_at, run_endpoint, created_at, updated_at
		FROM workflows WHERE user_id = $1 ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var wfs []models.Workflow
	for rows.Next() {
		var w models.Workflow
		var graphJSON []byte
		var runEndpoint *string
		if err := rows.Scan(
			&w.ID, &w.UserID, &w.Name, &w.Status, &graphJSON,
			&w.DeployedAt, &runEndpoint, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if runEndpoint != nil {
			w.RunEndpoint = *runEndpoint
		}
		unmarshalGraph(graphJSON, &w)
		wfs = append(wfs, w)
	}
	return wfs, rows.Err()
}

func (s *Store) UpdateWorkflow(ctx context.Context, id, name string, graph models.WorkflowGraph) (models.Workflow, error) {
	graphJSON, _ := json.Marshal(graph)
	var w models.Workflow
	var gJSON []byte
	var runEndpoint *string
	err := s.pool.QueryRow(ctx, `
		UPDATE workflows SET name=$2, graph=$3::jsonb, updated_at=NOW()
		WHERE id=$1
		RETURNING id, user_id, name, status, graph, deployed_at, run_endpoint, created_at, updated_at
	`, id, name, string(graphJSON)).Scan(
		&w.ID, &w.UserID, &w.Name, &w.Status, &gJSON,
		&w.DeployedAt, &runEndpoint, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return w, err
	}
	if runEndpoint != nil {
		w.RunEndpoint = *runEndpoint
	}
	unmarshalGraph(gJSON, &w)
	return w, nil
}

func (s *Store) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1`, id)
	return err
}

func (s *Store) SetWorkflowDeployed(ctx context.Context, id, runEndpoint string, deployedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE workflows SET status='deployed', run_endpoint=$2, deployed_at=$3, updated_at=NOW()
		WHERE id=$1
	`, id, runEndpoint, deployedAt)
	return err
}

func unmarshalGraph(data []byte, w *models.Workflow) {
	var g models.WorkflowGraph
	if err := json.Unmarshal(data, &g); err == nil {
		w.Nodes = g.Nodes
		w.Edges = g.Edges
	}
}

// --- Run methods ---

func (s *Store) CreateRun(ctx context.Context, workflowID, triggeredBy string, inputContext []byte) (models.Run, error) {
	var r models.Run
	var ic []byte
	err := s.pool.QueryRow(ctx, `
		INSERT INTO runs (workflow_id, triggered_by, status, input_context)
		VALUES ($1, $2, 'running', $3::jsonb)
		RETURNING id, workflow_id, triggered_by, status, started_at, finished_at, input_context
	`, workflowID, triggeredBy, string(inputContext)).Scan(
		&r.ID, &r.WorkflowID, &r.TriggeredBy, &r.Status,
		&r.StartedAt, &r.FinishedAt, &ic,
	)
	if err != nil {
		return r, err
	}
	if ic != nil {
		json.Unmarshal(ic, &r.InputContext)
	}
	return r, nil
}

func (s *Store) GetRun(ctx context.Context, runID string) (models.Run, error) {
	var r models.Run
	var ic []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, triggered_by, status, started_at, finished_at, input_context
		FROM runs WHERE id=$1
	`, runID).Scan(
		&r.ID, &r.WorkflowID, &r.TriggeredBy, &r.Status,
		&r.StartedAt, &r.FinishedAt, &ic,
	)
	if err != nil {
		return r, err
	}
	if ic != nil {
		json.Unmarshal(ic, &r.InputContext)
	}
	return r, nil
}

func (s *Store) FinishRun(ctx context.Context, runID string, status models.RunStatus) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status=$2, finished_at=NOW() WHERE id=$1
	`, runID, string(status))
	return err
}

// --- RunLog methods ---

func (s *Store) InsertRunLog(ctx context.Context, l models.RunLog) (models.RunLog, error) {
	inputJSON, _ := json.Marshal(l.Input)
	var out models.RunLog
	var inJSON, outJSON []byte
	var durationMs *int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO run_logs (run_id, step_index, node_id, node_type, status, input)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb)
		RETURNING id, run_id, step_index, node_id, node_type, status, input, output, duration_ms, ts
	`, l.RunID, l.StepIndex, l.NodeID, string(l.NodeType), string(l.Status), string(inputJSON)).Scan(
		&out.ID, &out.RunID, &out.StepIndex, &out.NodeID, &out.NodeType,
		&out.Status, &inJSON, &outJSON, &durationMs, &out.Ts,
	)
	if err != nil {
		return out, err
	}
	if durationMs != nil {
		out.DurationMs = *durationMs
	}
	if inJSON != nil {
		json.Unmarshal(inJSON, &out.Input)
	}
	return out, nil
}

func (s *Store) UpdateRunLog(ctx context.Context, id string, status models.LogStatus, outputJSON []byte, durationMs int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE run_logs SET status=$2, output=$3::jsonb, duration_ms=$4 WHERE id=$1
	`, id, string(status), string(outputJSON), durationMs)
	return err
}

func (s *Store) GetRunLogs(ctx context.Context, runID string) ([]models.RunLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, step_index, node_id, node_type, status, output, duration_ms, ts
		FROM run_logs WHERE run_id=$1 ORDER BY step_index, ts
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []models.RunLog
	for rows.Next() {
		var l models.RunLog
		var outJSON []byte
		var durationMs *int
		if err := rows.Scan(
			&l.ID, &l.RunID, &l.StepIndex, &l.NodeID, &l.NodeType,
			&l.Status, &outJSON, &durationMs, &l.Ts,
		); err != nil {
			return nil, err
		}
		if durationMs != nil {
			l.DurationMs = *durationMs
		}
		if outJSON != nil {
			json.Unmarshal(outJSON, &l.Output)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// --- AgentWallet methods ---

func (s *Store) InsertAgentWallet(ctx context.Context, w models.AgentWallet) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_wallets (workflow_id, agent_node_id, address, encrypted_mnemonic, network)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (workflow_id, agent_node_id) DO UPDATE
		  SET address=EXCLUDED.address, encrypted_mnemonic=EXCLUDED.encrypted_mnemonic
	`, w.WorkflowID, w.AgentNodeID, w.Address, w.EncryptedMnemonic, w.Network)
	return err
}

func (s *Store) GetAgentWallet(ctx context.Context, workflowID, agentNodeID string) (models.AgentWallet, error) {
	var w models.AgentWallet
	err := s.pool.QueryRow(ctx, `
		SELECT id, workflow_id, agent_node_id, address, encrypted_mnemonic, network
		FROM agent_wallets WHERE workflow_id=$1 AND agent_node_id=$2
	`, workflowID, agentNodeID).Scan(
		&w.ID, &w.WorkflowID, &w.AgentNodeID, &w.Address, &w.EncryptedMnemonic, &w.Network,
	)
	return w, err
}

func (s *Store) ListAgentWallets(ctx context.Context, workflowID string) ([]models.AgentWallet, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workflow_id, agent_node_id, address, encrypted_mnemonic, network
		FROM agent_wallets WHERE workflow_id=$1
	`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var wallets []models.AgentWallet
	for rows.Next() {
		var w models.AgentWallet
		if err := rows.Scan(&w.ID, &w.WorkflowID, &w.AgentNodeID, &w.Address, &w.EncryptedMnemonic, &w.Network); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// --- User methods ---

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES (gen_random_uuid()::text, $1, $2)
		RETURNING id, email, password_hash, name, org_name, created_at
	`, email, passwordHash).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

// UpdateProfile sets the display name and organization name for a user —
// collected at signup for password accounts, or via a post-login onboarding
// step for OAuth accounts (which have no name/org until the provider redirect
// completes, since neither Google nor GitHub's basic profile scope carries one).
func (s *Store) UpdateProfile(ctx context.Context, userID, name, orgName string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET name = $2, org_name = $3
		WHERE id = $1
		RETURNING id, email, password_hash, name, org_name, created_at
	`, userID, name, orgName).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	return u, err
}

// GetOrCreateOAuthUser returns the user for a verified OAuth email, creating an
// OAuth-only account (empty password_hash, so bcrypt password login always fails)
// when none exists. Linking to an existing OAuth account by verified email is
// allowed; linking to a password account returns ErrPasswordAccountExists.
func (s *Store) GetOrCreateOAuthUser(ctx context.Context, email string) (models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, name, org_name, created_at FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	if err == nil {
		if u.PasswordHash != "" {
			return models.User{}, ErrPasswordAccountExists
		}
		return u, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, err
	}

	// No existing user — create an OAuth-only account. name/org_name are left
	// blank: neither provider's basic profile scope carries an organization,
	// and the frontend prompts for both once the user lands back on the app
	// (see Deps.Me's needsOnboarding and Deps.UpdateProfile).
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES (gen_random_uuid()::text, $1, '')
		ON CONFLICT (email) DO NOTHING
		RETURNING id, email, password_hash, name, org_name, created_at
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost a race: a row appeared between SELECT and INSERT. Re-fetch and
		// apply the same password-account guard.
		err = s.pool.QueryRow(ctx, `
			SELECT id, email, password_hash, name, org_name, created_at FROM users WHERE email = $1
		`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.OrgName, &u.CreatedAt)
		if err == nil && u.PasswordHash != "" {
			return models.User{}, ErrPasswordAccountExists
		}
	}
	return u, err
}

// --- Waitlist methods ---

func (s *Store) InsertWaitlistEmail(ctx context.Context, email string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO waitlist (email) VALUES ($1) ON CONFLICT (email) DO NOTHING
	`, email)
	return err
}

// --- Credit ledger methods ---

func (s *Store) CreateCreditTransaction(ctx context.Context, userID, providerOrderID string, amountINRPaise int64, fxRate float64) (models.CreditTransaction, error) {
	return s.CreateCreditTransactionForProvider(ctx, "cashfree", userID, providerOrderID, amountINRPaise, fxRate)
}

func (s *Store) CreateCreditTransactionForProvider(ctx context.Context, provider, userID, providerOrderID string, amountINRPaise int64, fxRate float64) (models.CreditTransaction, error) {
	creditUSDMicros := int64(math.Round(float64(amountINRPaise) / 100.0 * fxRate * 1e6))
	var txn models.CreditTransaction
	err := s.pool.QueryRow(ctx, `
		INSERT INTO credit_ledger (user_id, provider, provider_order_id, status, amount_inr_paise, fx_rate_usd_per_inr, credit_usd_micros)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6)
		RETURNING id, user_id, provider, provider_order_id, status, amount_inr_paise, fx_rate_usd_per_inr, credit_usd_micros, created_at
	`, userID, provider, providerOrderID, amountINRPaise, fxRate, creditUSDMicros).Scan(
		&txn.ID, &txn.UserID, &txn.Provider, &txn.ProviderOrderID, &txn.Status,
		&txn.AmountINRPaise, &txn.FXRateUSDPerINR, &txn.CreditUSDMicros, &txn.CreatedAt,
	)
	return txn, err
}

// CreateCryptoCreditTransaction records a pending ledger row for a hosted crypto invoice
// (NOWPayments or any future crypto gateway sharing this shape). Unlike the Razorpay path,
// the amount is already USD-denominated by the gateway, so there is no FX rate to store.
func (s *Store) CreateCryptoCreditTransaction(ctx context.Context, userID, provider, providerOrderID string, amountUSDCents int64) (models.CreditTransaction, error) {
	creditUSDMicros := amountUSDCents * 10_000
	var txn models.CreditTransaction
	err := s.pool.QueryRow(ctx, `
		INSERT INTO credit_ledger (user_id, provider, provider_order_id, status, amount_usd_cents, credit_usd_micros)
		VALUES ($1, $2, $3, 'pending', $4, $5)
		RETURNING id, user_id, provider, provider_order_id, status, amount_usd_cents, credit_usd_micros, created_at
	`, userID, provider, providerOrderID, amountUSDCents, creditUSDMicros).Scan(
		&txn.ID, &txn.UserID, &txn.Provider, &txn.ProviderOrderID, &txn.Status,
		&txn.AmountUSDCents, &txn.CreditUSDMicros, &txn.CreatedAt,
	)
	return txn, err
}

// ErrCreditTransactionNotFound is returned when no credit_ledger row exists for the given
// provider order ID — the caller supplied an order Razorpay never told us about (or that
// our own CreateCreditTransaction failed to record). Callers should treat this as a
// permanent 4xx, not a transient failure: retrying an unknown order will never succeed.
var ErrCreditTransactionNotFound = errors.New("credit transaction not found")

// CompleteCreditTransaction marks the ledger row for providerOrderID as completed and
// credits the user's cached balance, atomically. Idempotent: if the row is already
// completed (webhook/verify replay), it returns the stored amount without re-crediting.
// The bool return is true only when this call is the one that actually completed the
// transaction (false on a replay) — callers use it to fire an audit-log notification
// exactly once per real credit, not once per redundant client-verify/webhook race.
func (s *Store) CompleteCreditTransaction(ctx context.Context, provider, providerOrderID, providerPaymentID string) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	var (
		id              string
		userID          string
		status          string
		creditUSDMicros int64
		completedAt     *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, status, credit_usd_micros, completed_at
		FROM credit_ledger
		WHERE provider_order_id = $1 AND provider = $2
		FOR UPDATE
	`, providerOrderID, provider).Scan(&id, &userID, &status, &creditUSDMicros, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrCreditTransactionNotFound
	}
	if err != nil {
		return 0, false, err
	}

	// Gate on completed_at (replay-safety, unchanged) *and* on status != 'failed': a row
	// a crypto webhook already marked failed/expired must never be resurrected by a
	// late or out-of-order "finished" IPN retry — see MarkCreditTransactionStatus.
	if completedAt != nil || status == "failed" {
		return creditUSDMicros, false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE credit_ledger SET status = 'completed', provider_payment_id = $1, completed_at = NOW()
		WHERE id = $2
	`, providerPaymentID, id); err != nil {
		return 0, false, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1 WHERE id = $2
	`, creditUSDMicros, userID); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return creditUSDMicros, true, nil
}

// RefundCreditTransaction reverses previously-credited USD micros when Razorpay reports a
// refund against an order. totalRefundedINRPaise is the *cumulative* amount refunded on the
// payment so far — Razorpay resends this on every refund event (partial or full), so this
// method tracks refunded_inr_paise on the ledger row and only acts on the delta between the
// new total and what was already applied, making repeated/replayed events safe.
//
// If the order was never completed in our ledger (still 'pending' or already 'expired'), no
// credit was ever granted, so no balance reversal happens — only the bookkeeping columns are
// updated. credit_balance_usd_micros is floored at 0 via GREATEST so a reversal can never push
// a user negative even under an unexpected ordering of events.
//
// The bool return is true only when this call applied a new refund delta (false when the
// cumulative total matches what's already recorded, i.e. a replayed webhook) — callers use
// it to fire an audit-log notification exactly once per real refund event.
func (s *Store) RefundCreditTransaction(ctx context.Context, providerOrderID string, totalRefundedINRPaise int64) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	var (
		id               string
		userID           string
		status           string
		amountINRPaise   int64
		fxRate           float64
		refundedINRPaise int64
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, status, amount_inr_paise, fx_rate_usd_per_inr, refunded_inr_paise
		FROM credit_ledger
		WHERE provider_order_id = $1
		FOR UPDATE
	`, providerOrderID).Scan(&id, &userID, &status, &amountINRPaise, &fxRate, &refundedINRPaise)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrCreditTransactionNotFound
	}
	if err != nil {
		return 0, false, err
	}

	delta := totalRefundedINRPaise - refundedINRPaise
	if delta <= 0 {
		return 0, false, nil
	}

	var reversedUSDMicros int64
	if status == "completed" || status == "refunded" {
		reversedUSDMicros = int64(math.Round(float64(delta) / 100.0 * fxRate * 1e6))
		if _, err := tx.Exec(ctx, `
			UPDATE users SET credit_balance_usd_micros = GREATEST(0, credit_balance_usd_micros - $1) WHERE id = $2
		`, reversedUSDMicros, userID); err != nil {
			return 0, false, err
		}
	}

	newStatus := status
	if totalRefundedINRPaise >= amountINRPaise {
		newStatus = "refunded"
	}

	if _, err := tx.Exec(ctx, `
		UPDATE credit_ledger SET refunded_inr_paise = $1, status = $2 WHERE id = $3
	`, totalRefundedINRPaise, newStatus, id); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return reversedUSDMicros, true, nil
}

func (s *Store) GetCreditBalance(ctx context.Context, userID string) (int64, error) {
	var balance int64
	err := s.pool.QueryRow(ctx, `SELECT credit_balance_usd_micros FROM users WHERE id = $1`, userID).Scan(&balance)
	return balance, err
}

// --- Coupons ---

// CouponAmountsUSDMicros is the fixed catalog of redeemable coupon codes. Each
// code is independently redeemable once per user (enforced by the UNIQUE
// (user_id, code) constraint on coupon_redemptions) — redeeming multiple
// distinct codes stacks.
var CouponAmountsUSDMicros = map[string]int64{
	"AYASHISGAY6969": 5_000_000, // $5
	"INNOFUSIONFTW":  5_000_000, // $5
}

var (
	ErrCouponInvalid         = errors.New("invalid coupon code")
	ErrCouponAlreadyRedeemed = errors.New("coupon already redeemed")
)

// RedeemCoupon credits a user's balance for an unredeemed, known coupon code,
// atomically. The UNIQUE (user_id, code) constraint plus ON CONFLICT DO
// NOTHING is what actually enforces "once per user per code" under
// concurrent requests — RowsAffected == 0 means another request (or an
// earlier one) already claimed this code for this user.
func (s *Store) RedeemCoupon(ctx context.Context, userID, code string) (int64, error) {
	amount, ok := CouponAmountsUSDMicros[code]
	if !ok {
		return 0, ErrCouponInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO coupon_redemptions (user_id, code, credit_usd_micros)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, code) DO NOTHING
	`, userID, code, amount)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrCouponAlreadyRedeemed
	}

	var newBalance int64
	if err := tx.QueryRow(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1
		WHERE id = $2
		RETURNING credit_balance_usd_micros
	`, amount, userID).Scan(&newBalance); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return newBalance, nil
}

// MarkCreditTransactionStatus moves a still-pending ledger row directly to status
// (e.g. "failed"/"expired" for a NOWPayments IPN that will never complete, or "partial"
// for partially_paid) without touching the user's balance — a pending row never credited
// anything, so there's nothing to reverse. No-op if the row is no longer pending, so it's
// safe to call on IPN replays.
func (s *Store) MarkCreditTransactionStatus(ctx context.Context, provider, providerOrderID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE credit_ledger SET status = $1
		WHERE provider_order_id = $2 AND provider = $3 AND status = 'pending'
	`, status, providerOrderID, provider)
	return err
}

// ExpireStalePendingTransactions marks credit_ledger rows for provider still 'pending'
// after olderThan as 'expired' — checkouts the user opened but never completed (closed
// tab, abandoned QR scan, on-chain payment never sent). Scoped to a single provider so
// callers can use a per-provider staleness window: fast checkout providers like Razorpay
// warrant a short window, while on-chain crypto providers like NOWPayments need a much
// longer one to avoid expiring payments still working through block confirmations. Keeps
// 'pending' meaningful as "still in progress" rather than accumulating dead rows.
func (s *Store) ExpireStalePendingTransactions(ctx context.Context, provider string, olderThan time.Duration) (int64, error) {
	// The cutoff is computed by the database, not by this process.
	// created_at is written by Postgres NOW(), so comparing it against an
	// app-computed time.Now() straddles two clocks: any skew between them
	// (a containerised Postgres on a macOS VM routinely runs a fraction of
	// a second ahead of the host) shifts the effective window by that skew,
	// and a row created moments ago can be newer than a cutoff that was
	// supposed to already include it. Evaluating both sides in the DB makes
	// the window exactly olderThan, whatever either clock says.
	tag, err := s.pool.Exec(ctx, `
		UPDATE credit_ledger SET status = 'expired'
		WHERE status = 'pending' AND provider = $1
		  AND created_at < NOW() - make_interval(secs => $2)
	`, provider, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- Debit ledger methods ---

// ErrInsufficientCredits is returned by DebitCredits when the user's balance
// is below the amount being charged. Callers treat this as a permanent
// failure for that call — the node did not run (or, for x402, the payment
// already happened and this is logged rather than retried).
var ErrInsufficientCredits = errors.New("insufficient credits")

// debitCredits atomically locks userID's balance, checks it covers
// amountUSDMicros, decrements it, then lets insertLedger write whatever
// debit_ledger row shape the caller needs inside the same transaction.
// Shared by DebitCredits and DebitCreditsForPlatformLLM so both kinds get
// the identical atomicity guarantee — lock, check, decrement, all inside
// one transaction, same pattern as CompleteCreditTransaction.
func (s *Store) debitCredits(ctx context.Context, userID string, amountUSDMicros int64, insertLedger func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT credit_balance_usd_micros FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&balance); err != nil {
		return err
	}

	if balance < amountUSDMicros {
		return ErrInsufficientCredits
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros - $1 WHERE id = $2
	`, amountUSDMicros, userID); err != nil {
		return err
	}

	if err := insertLedger(ctx, tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ReserveCredits atomically checks a user's balance covers amountUSDMicros
// and, if so, immediately decrements it — without yet writing a debit_ledger
// row. x402 payments split the check from the real payment attempt by a
// network round trip (sign, relay, wait for settlement); reserving the
// balance up front, at the same atomic-decrement primitive DebitCredits
// already uses, closes the gap where concurrent or sequential calls within
// one node execution could all pass a check against the same stale balance.
// Pair with CommitReservedDebit once the payment is confirmed settled, or
// ReleaseReservedCredits if it never happened.
func (s *Store) ReserveCredits(ctx context.Context, userID string, amountUSDMicros int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT credit_balance_usd_micros FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&balance); err != nil {
		return err
	}
	if balance < amountUSDMicros {
		return fmt.Errorf("insufficient credits: balance %d micros, need %d micros: %w", balance, amountUSDMicros, ErrInsufficientCredits)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros - $1 WHERE id = $2
	`, amountUSDMicros, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CommitReservedDebit records the debit_ledger audit row for a
// ReserveCredits reservation that turned into a real, settled charge. The
// balance was already decremented at reservation time, so this only writes
// the audit trail — it must never be called with an amount that wasn't
// already reserved.
func (s *Store) CommitReservedDebit(ctx context.Context, userID string, amountUSDMicros int64, kind, workflowID, runID, nodeID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, workflowID, runID, nodeID, kind, amountUSDMicros)
	return err
}

// ReleaseReservedCredits credits back a ReserveCredits reservation that
// never became a real charge (the payment attempt failed, or was never
// confirmed settled, before any money moved). No debit_ledger row: nothing
// was ever actually charged, so there is nothing there to reverse.
func (s *Store) ReleaseReservedCredits(ctx context.Context, userID string, amountUSDMicros int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET credit_balance_usd_micros = credit_balance_usd_micros + $1 WHERE id = $2
	`, amountUSDMicros, userID)
	return err
}

// DebitCredits atomically charges a user's credit balance for a metered
// action inside a workflow run, and records the charge in debit_ledger.
func (s *Store) DebitCredits(ctx context.Context, userID string, amountUSDMicros int64, kind, workflowID, runID, nodeID string) error {
	return s.debitCredits(ctx, userID, amountUSDMicros, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, userID, workflowID, runID, nodeID, kind, amountUSDMicros)
		return err
	})
}

// DebitCreditsForPlatformLLM is DebitCredits specialized for the
// platform_key_llm_fee kind: same atomic lock/check/decrement guarantee,
// plus the model and token counts captured for internal margin tracking —
// the charge is always the flat tier fee in amountUSDMicros regardless of
// actual token count, so these columns are informational, not billing.
func (s *Store) DebitCreditsForPlatformLLM(ctx context.Context, userID string, amountUSDMicros int64, workflowID, runID, nodeID, model string, tokensIn, tokensOut int) error {
	return s.debitCredits(ctx, userID, amountUSDMicros, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO debit_ledger (user_id, workflow_id, run_id, node_id, kind, amount_usd_micros, model, tokens_in, tokens_out)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, userID, workflowID, runID, nodeID, models.DebitKindPlatformKeyLLMFee, amountUSDMicros, model, tokensIn, tokensOut)
		return err
	})
}

// ListDebitLedger returns every debit_ledger row for a run, oldest first.
// Used by the credits/usage dashboard and by tests asserting exactly which
// charges a run produced.
func (s *Store) ListDebitLedger(ctx context.Context, runID string) ([]models.DebitEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, workflow_id, run_id, node_id, kind, amount_usd_micros, created_at, model, tokens_in, tokens_out
		FROM debit_ledger WHERE run_id = $1 ORDER BY created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DebitEntry
	for rows.Next() {
		var e models.DebitEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.WorkflowID, &e.RunID, &e.NodeID, &e.Kind, &e.AmountUSDMicros, &e.CreatedAt, &e.Model, &e.TokensIn, &e.TokensOut); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- X402 Relay Settlement methods ---

// ErrDuplicateSettlement is returned when an inbound settlement's txid has already
// been recorded — a replayed X-PAYMENT payload must never be processed twice.
var ErrDuplicateSettlement = errors.New("duplicate settlement txid")

func (s *Store) RecordInboundSettlement(ctx context.Context, targetURL, inboundTxID string, amountAssetMicros int64) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_relay_settlements (target_url, inbound_tx_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
	`, targetURL, inboundTxID, amountAssetMicros).Scan(
		&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt,
	)
	if err != nil && strings.Contains(err.Error(), "duplicate key value") {
		return models.X402RelaySettlement{}, ErrDuplicateSettlement
	}
	return row, err
}

func (s *Store) RecordOutboundSettlement(ctx context.Context, id, outboundTxID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE x402_relay_settlements SET outbound_tx_id = $2, status = $3 WHERE id = $1
	`, id, outboundTxID, status)
	return err
}

// GetX402RelaySettlementByInboundTx looks up a relay ledger row by its
// inbound settlement tx id — used to verify what was actually recorded
// (e.g. the settled amount) after a relay flow completes.
func (s *Store) GetX402RelaySettlementByInboundTx(ctx context.Context, inboundTxID string) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		SELECT id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
		FROM x402_relay_settlements WHERE inbound_tx_id = $1
	`, inboundTxID).Scan(
		&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt,
	)
	return row, err
}

// RecordRunFunding inserts one x402_run_fundings audit row for a real,
// already-settled inbound payment (Wallet 1 -> Wallet 2) that pre-funds a
// whole run's worth of downstream x402 tool calls, instead of one inbound
// settlement per call.
func (s *Store) RecordRunFunding(ctx context.Context, runID, inboundTxID string, amountAssetMicros int64) (models.X402RunFunding, error) {
	var f models.X402RunFunding
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_run_fundings (run_id, inbound_tx_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, run_id, inbound_tx_id, amount_asset_micros, created_at
	`, runID, inboundTxID, amountAssetMicros).Scan(&f.ID, &f.RunID, &f.InboundTxID, &f.AmountAssetMicros, &f.CreatedAt)
	return f, err
}

// RecordRunFundedSettlement inserts an x402_relay_settlements audit row
// attributed to an existing run-level bulk settlement (run_funding_id)
// instead of a fresh per-call inbound one (inbound_tx_id). Takes
// amountAssetMicros directly at INSERT time — RecordOutboundSettlement only
// ever updates outbound_tx_id/status, never amount_asset_micros, so there is
// no later call that could backfill a placeholder value here.
// RecordInboundSettlement (the existing per-call equivalent) already sets
// the real amount at INSERT time for the same reason — this mirrors that,
// not a new pattern. status is left unset so it defaults to
// 'pending_outbound', matching RecordInboundSettlement's behavior;
// RecordOutboundSettlement's later call must pass "settled" or "failed" to
// satisfy the table's status CHECK constraint.
func (s *Store) RecordRunFundedSettlement(ctx context.Context, runFundingID, targetURL string, amountAssetMicros int64) (models.X402RelaySettlement, error) {
	var row models.X402RelaySettlement
	err := s.pool.QueryRow(ctx, `
		INSERT INTO x402_relay_settlements (target_url, run_funding_id, amount_asset_micros)
		VALUES ($1, $2, $3)
		RETURNING id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
	`, targetURL, runFundingID, amountAssetMicros).Scan(&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt)
	return row, err
}

// ListX402RunFundingsByRun returns every x402_run_fundings row for a given
// run, oldest first. Used by tests asserting exactly one run-level pre-fund
// happened per agent run (Task 5's reserveAndFundRun).
func (s *Store) ListX402RunFundingsByRun(ctx context.Context, runID string) ([]models.X402RunFunding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, inbound_tx_id, amount_asset_micros, created_at
		FROM x402_run_fundings WHERE run_id = $1 ORDER BY created_at ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.X402RunFunding
	for rows.Next() {
		var row models.X402RunFunding
		if err := rows.Scan(&row.ID, &row.RunID, &row.InboundTxID, &row.AmountAssetMicros, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListX402RelaySettlementsByRunFunding returns every x402_relay_settlements
// row attributed to a given run-level bulk funding (run_funding_id), oldest
// first. Used by tests asserting exactly which per-call settlements a
// run-funded agent turn produced.
func (s *Store) ListX402RelaySettlementsByRunFunding(ctx context.Context, runFundingID string) ([]models.X402RelaySettlement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, target_url, inbound_tx_id, outbound_tx_id, amount_asset_micros, status, created_at
		FROM x402_relay_settlements WHERE run_funding_id = $1 ORDER BY created_at ASC
	`, runFundingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.X402RelaySettlement
	for rows.Next() {
		var row models.X402RelaySettlement
		if err := rows.Scan(&row.ID, &row.TargetURL, &row.InboundTxID, &row.OutboundTxID, &row.AmountAssetMicros, &row.Status, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// --- Workflow variable methods ---

const (
	// MaxWorkflowVariables caps how many keys one workflow may hold. This
	// is bounded key/value state for "remember the last row I processed",
	// not a document store.
	MaxWorkflowVariables = 64
	// maxWorkflowVariableBytes mirrors the CHECK constraint on the column,
	// enforced here too so the caller gets a typed error instead of a raw
	// Postgres constraint violation.
	maxWorkflowVariableBytes = 16384
)

var (
	ErrVariableQuotaExceeded = errors.New("workflow variable limit reached")
	ErrVariableTooLarge      = errors.New("workflow variable value too large")
)

// GetWorkflowVariables returns every variable for a workflow, JSON-decoded.
// Returns an empty (non-nil) map when the workflow has none, so callers can
// index it without a nil check.
func (s *Store) GetWorkflowVariables(ctx context.Context, workflowID string) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT key, value FROM workflow_variables WHERE workflow_id=$1
	`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]any)
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		var decoded any
		json.Unmarshal(v, &decoded)
		out[k] = decoded
	}
	return out, rows.Err()
}

// SetWorkflowVariable upserts one variable. Concurrent writers are
// last-write-wins by design: the alternative (optimistic versioning) would
// make the common cases — "cache this token", "remember this cursor" —
// fail spuriously when two runs overlap. Callers that need a correct
// counter under concurrency use IncrementWorkflowVariable instead, which
// is atomic in the database.
//
// The key-count quota is checked inside the same transaction as the write
// so two concurrent inserts cannot both slip past the cap.
func (s *Store) SetWorkflowVariable(ctx context.Context, workflowID, key string, valueJSON []byte) error {
	if len(valueJSON) > maxWorkflowVariableBytes {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrVariableTooLarge, len(valueJSON), maxWorkflowVariableBytes)
	}
	if key == "" || len(key) > 128 {
		return errors.New("workflow variable key must be 1-128 characters")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var count int
	var exists bool
	// COALESCE because BOOL_OR over zero rows is NULL, which will not scan
	// into a bool.
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(BOOL_OR(key=$2), FALSE)
		FROM workflow_variables WHERE workflow_id=$1
	`, workflowID, key).Scan(&count, &exists); err != nil {
		return err
	}
	// Updating a key that already exists never adds to the count, so it is
	// allowed even at the cap.
	if !exists && count >= MaxWorkflowVariables {
		return fmt.Errorf("%w: %d keys, limit %d", ErrVariableQuotaExceeded, count, MaxWorkflowVariables)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO workflow_variables (workflow_id, key, value, updated_at)
		VALUES ($1,$2,$3::jsonb,NOW())
		ON CONFLICT (workflow_id, key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = NOW()
	`, workflowID, key, string(valueJSON)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IncrementWorkflowVariable adds delta to a numeric variable and returns
// the new value, creating it at delta if absent. A single statement, so
// overlapping runs of the same workflow cannot lose an update the way a
// read-then-write from application code would.
//
// A non-numeric existing value is replaced by delta rather than erroring —
// the counter use case wants to keep counting, not to fail a run because
// something once wrote a string there.
func (s *Store) IncrementWorkflowVariable(ctx context.Context, workflowID, key string, delta float64) (float64, error) {
	if key == "" || len(key) > 128 {
		return 0, errors.New("workflow variable key must be 1-128 characters")
	}
	var out float64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflow_variables (workflow_id, key, value, updated_at)
		VALUES ($1,$2,to_jsonb($3::numeric),NOW())
		ON CONFLICT (workflow_id, key) DO UPDATE
		SET value = to_jsonb(
			CASE WHEN jsonb_typeof(workflow_variables.value) = 'number'
			     THEN (workflow_variables.value)::numeric + $3::numeric
			     ELSE $3::numeric
			END),
		    updated_at = NOW()
		RETURNING (value)::numeric
	`, workflowID, key, delta).Scan(&out)
	return out, err
}

// DeleteWorkflowVariable removes one key. Deleting a key that does not
// exist is not an error.
func (s *Store) DeleteWorkflowVariable(ctx context.Context, workflowID, key string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM workflow_variables WHERE workflow_id=$1 AND key=$2
	`, workflowID, key)
	return err
}
