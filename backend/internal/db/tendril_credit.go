package db

import (
	"context"
	"fmt"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

func (s *Store) TendrilCreditBalance(ctx context.Context, userID string) (int64, error) {
	var bal int64
	err := s.pool.QueryRow(ctx,
		`SELECT tendril_credit_usd_micros FROM users WHERE id = $1`, userID).Scan(&bal)
	return bal, err
}

// RecentTendrilCreditLedger is the current user's own movements only — never
// the shared pool, which is every user's money and must never be shown to
// one of them.
func (s *Store) RecentTendrilCreditLedger(ctx context.Context, userID string, limit int) ([]models.TendrilCreditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, kind, amount_usd_micros, lease_id, tx_id, created_at
		  FROM tendril_credit_ledger
		 WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TendrilCreditEntry
	for rows.Next() {
		var e models.TendrilCreditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &e.AmountUSDMicros, &e.LeaseID, &e.TxID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TendrilCreditLedgerSince is the current user's own ledger movements from a
// given time on — the Usage page's window into Tendril activity, which lives
// entirely in this table and never touches debit_ledger (see usage.go's own
// doc comment: Tendril credit is a separate sub-ledger, not AgentMesh spend,
// so it was invisible on that page until this existed).
func (s *Store) TendrilCreditLedgerSince(ctx context.Context, userID string, since time.Time) ([]models.TendrilCreditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, kind, amount_usd_micros, lease_id, tx_id, created_at
		  FROM tendril_credit_ledger
		 WHERE user_id = $1 AND created_at >= $2
		 ORDER BY created_at DESC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TendrilCreditEntry
	for rows.Next() {
		var e models.TendrilCreditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &e.AmountUSDMicros, &e.LeaseID, &e.TxID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ConvertCreditsToTendril credits the user's Tendril sub-ledger after a real
// topup payment has already settled. It is credit-ONLY: it does NOT also
// debit credit_balance_usd_micros. The AgentMesh-credit side of a topup is
// already charged exactly once, by the x402 relay's own Reserve/Commit
// inside payTendril (the console's ledger, newConsolePaymentLedger, reserves/
// commits the settled amount plus models.X402PlatformFeeUSDMicros markup)
// -- that debit covers the real vendor amount (this same amountUSDMicros) and
// settles the matching
// USDC into the shared Wallet 2 pool. A second debit here, of the same
// amountUSDMicros, used to double-charge the user for one purchase: it
// would drain their AgentMesh balance twice over for a single topup, and
// deterministically fail (crediting nothing) for anyone topping up their
// exact full balance, since the relay's debit alone already reduced it to
// zero. Guarded by the credit_balance_non_negative-adjacent invariant on
// the OTHER side (tendril_credit_non_negative doesn't apply to a pure add,
// but see ChargeTendrilCredit for the debit-side guard that DOES need it).
//
// txID is the on-chain id of the topup settlement that put the matching USDC
// into the shared Wallet 2 pool — recorded so a user's Tendril credit is
// always traceable to the real payment that backs it.
func (s *Store) ConvertCreditsToTendril(ctx context.Context, userID string, amountUSDMicros int64, txID string) (int64, error) {
	if amountUSDMicros <= 0 {
		return 0, fmt.Errorf("tendril topup amount must be positive, got %d", amountUSDMicros)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var tendrilBalance int64
	if err := tx.QueryRow(ctx, `
		UPDATE users SET tendril_credit_usd_micros = tendril_credit_usd_micros + $2
		 WHERE id = $1 RETURNING tendril_credit_usd_micros`,
		userID, amountUSDMicros).Scan(&tendrilBalance); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tendril_credit_ledger (user_id, kind, amount_usd_micros, tx_id)
		VALUES ($1, 'topup', $2, $3)`, userID, amountUSDMicros, nullIfEmpty(txID)); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tendrilBalance, nil
}

// ChargeTendrilCredit debits (kind "charge") or credits back (kind "refund")
// one user's Tendril balance. The non-negative CHECK is what stops a user
// spending hours they did not buy — the shared pool is never consulted.
func (s *Store) ChargeTendrilCredit(ctx context.Context, userID, leaseID, kind string, amountUSDMicros int64) error {
	if amountUSDMicros <= 0 {
		return fmt.Errorf("tendril charge amount must be positive, got %d", amountUSDMicros)
	}
	delta := -amountUSDMicros
	if kind == "refund" {
		delta = amountUSDMicros
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE users SET tendril_credit_usd_micros = tendril_credit_usd_micros + $2
		 WHERE id = $1`, userID, delta); err != nil {
		return fmt.Errorf("insufficient Tendril credit for %d micros: %w", amountUSDMicros, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tendril_credit_ledger (user_id, kind, amount_usd_micros, lease_id)
		VALUES ($1, $2, $3, $4)`, userID, kind, amountUSDMicros, nullIfEmpty(leaseID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
