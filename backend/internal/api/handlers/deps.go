package handlers

import (
	"context"

	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/payments"
	"github.com/agentmesh/backend/internal/sse"
	"github.com/agentmesh/backend/internal/tendril"
	"github.com/agentmesh/backend/internal/wallet"
	"github.com/agentmesh/backend/internal/x402"
)

type contextKey string

const CtxUserID contextKey = "userID"

// CashfreeClient is the subset of *payments.CashfreeClient the handlers need.
// Defined here so tests can inject a fake without hitting the real API.
type CashfreeClient interface {
	CreateOrder(ctx context.Context, amountPaise int64, orderID, customerID, customerEmail, customerPhone string) (payments.CashfreeOrder, error)
	GetOrderStatus(ctx context.Context, orderID string) (string, error)
	VerifyWebhookSignature(body []byte, signature, timestamp string) bool
}

// NOWPaymentsClient is the subset of *payments.NOWPaymentsClient the handlers need.
// Defined here so tests can inject a fake without hitting the real API.
type NOWPaymentsClient interface {
	CreateInvoice(ctx context.Context, amountUSDCents int64, orderID, ipnCallbackURL, successURL, cancelURL string) (payments.Invoice, error)
	VerifyIPNSignature(body []byte, signature string) bool
}

// USDCSigner builds a gasless USDC atomic-payment group for the X-Payment
// header. Satisfied by *wallet.Service (SignUSDCPaymentGroup). Mirrors
// nodes.USDCGroupSigner exactly (including SignUSDCPaymentSingle, used by
// payTargetAndRespond's outbound-to-third-party leg) so d.USDCSigner keeps
// satisfying nodes.Wallet2PayConfig.USDCSigner without an explicit assertion.
type USDCSigner interface {
	SignUSDCPaymentGroup(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64, feePayerAddr string) ([]string, int, error)
	SignUSDCPaymentSingle(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64) ([]string, int, error)
}

var _ USDCSigner = (*wallet.Service)(nil)

type Deps struct {
	Store   *db.Store
	Broker  *sse.Broker
	Wallet  *wallet.Service
	Engine  *engine.Runner
	BaseURL string
	// RelayBaseURL is where THIS instance's own /x402/relay is reached from
	// (see main.go's relayBaseURL — distinct from BaseURL, which also signs
	// auth cookies). Needed by the Tendril console's direct-action endpoints
	// to build their own X402RelayConfig.
	RelayBaseURL  string
	JWTSecret     string
	EncryptionKey string

	FrontendURL        string
	GithubClientID     string
	GithubClientSecret string
	GoogleClientID     string
	GoogleClientSecret string

	// BazaarBaseURL is GoPlausible's facilitator, whose public
	// /discovery/resources catalog backs the Bazaar page. Overridable so tests
	// can point at a fake upstream.
	BazaarBaseURL string
	bazaarCache   bazaarCache

	Cashfree      CashfreeClient
	CashfreeAppID string

	NOWPayments NOWPaymentsClient

	PlatformWalletAddress     string
	PlatformWalletEncMnemonic string
	// PlatformSpendWalletEncMnemonic is Wallet 1 — the wallet that actually
	// signs and sends every relayed outbound x402 payment. Needed here (not
	// just inside engine.Runner) so the Tendril console's direct-action
	// endpoints can build their own X402RelayConfig without going through a
	// workflow run.
	PlatformSpendWalletEncMnemonic string
	// PlatformGeminiAPIKey powers the chat-driven workflow builder's
	// meta-agent (nodes.BuildGraph) -- independent of engine.Runner's own
	// platform-key map (main.go's runner.SetPlatformKeys call) since Deps
	// has no access to Runner's private fields.
	PlatformGeminiAPIKey string
	FacilitatorClient    *x402.FacilitatorClient
	USDCAssetID          uint64
	RelayNetwork         string
	RelayFeePayer        string
	USDCSigner           USDCSigner
	// MaxRelayOutboundUSDMicros caps a single outbound relay payment
	// (Wallet 2 -> target). The relay fetches a target's price quote twice
	// per cycle (once for the public challenge preview, once again at
	// settle-time to enforce/pay) — a target that answers cheap on the
	// first fetch and expensive on the second could otherwise cause the
	// platform to pay out more than the caller's inbound payment ever
	// covered, bounded only by whatever the GoPlausible facilitator itself
	// enforces. This is an independent, local backstop bounding worst-case
	// loss per call to a fixed ceiling regardless of facilitator behavior.
	// Zero means no cap (not recommended for a production deployment).
	MaxRelayOutboundUSDMicros int64

	// TendrilClient is nil when TENDRIL_REGISTRY_URL is unset, in which case
	// the Tendril-facing endpoints below fail closed with a clear error
	// rather than a nil-pointer panic.
	TendrilClient *tendril.Client
	// TendrilSession is the same Wallet 2 session engine.Runner holds, shared
	// here so the Tendril console's direct-action endpoints (topup/rent/run)
	// can call nodes.ExecuteTendril without going through a workflow run.
	TendrilSession *tendril.Session

	SlackOAuthClientID          string
	SlackOAuthClientSecret      string
	GitHubConnectorClientID     string
	GitHubConnectorClientSecret string
	NotionClientID              string
	NotionClientSecret          string
	AirtableClientID            string
	AirtableClientSecret        string
	HubSpotClientID             string
	HubSpotClientSecret         string
	AsanaClientID               string
	AsanaClientSecret           string
	ClickUpClientID             string
	ClickUpClientSecret         string
	JiraClientID                string
	JiraClientSecret            string
	LinearClientID              string
	LinearClientSecret          string
	MailchimpClientID           string
	MailchimpClientSecret       string
	GitLabClientID              string
	GitLabClientSecret          string
	TodoistClientID             string
	TodoistClientSecret         string
}
