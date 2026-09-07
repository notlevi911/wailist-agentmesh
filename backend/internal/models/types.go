package models

import "time"

type NodeType string
type EdgeKind string

const (
	NodeTypeTrigger  NodeType = "trigger"
	NodeTypeAgent    NodeType = "agent"
	NodeTypeProvider NodeType = "provider"
	NodeTypeTool     NodeType = "tool"
	NodeTypeTool402  NodeType = "tool402"
	NodeTypeAction   NodeType = "action"
	NodeTypeState    NodeType = "state"
	NodeTypeEnd      NodeType = "end"
	NodeTypeTendril  NodeType = "tendril"
	// NodeTypeGoogle covers Gmail/Sheets/Calendar/Drive -- grouped under one
	// node type (Template selects the specific operation, e.g.
	// "gmail_send") the same way NodeTypeTendril's TendrilAction does,
	// rather than one node type per Google product, since they all share
	// the identical OAuth-credential-lookup mechanics (see
	// nodes.ExecuteGoogle) and differ only in which API they call.
	NodeTypeGoogle NodeType = "google"
)

const (
	EdgeKindFlow   EdgeKind = "flow"
	EdgeKindAttach EdgeKind = "attach"
)

type ParamDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
}

// How a tool402 node builds its request. See WorkflowNode.BodyMode.
const (
	BodyModeParams = "params"
	BodyModeJSON   = "json"
)

// CustomParam is an input field a user defined by hand on a tool402 node,
// for the many real endpoints that publish no Bazaar input schema at all
// (confirmed 2026-08-02: prism-99h2.onrender.com declares none) — nothing can
// discover what those need, so the user states it.
//
// Kind "file" carries its bytes base64-encoded in Value and switches the whole
// request to multipart/form-data. Files ride inside the workflow document
// rather than separate blob storage, which keeps them bound to the node that
// uses them (no orphaned uploads, no extra lifecycle to get wrong) at the cost
// of a size ceiling — see maxParamFileBytes.
type CustomParam struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "text" | "file"
	Value    string `json:"value,omitempty"`
	FileName string `json:"fileName,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

type WorkflowNode struct {
	ID           string   `json:"id"`
	Type         NodeType `json:"type"`
	Template     string   `json:"template,omitempty"`
	X            float64  `json:"x,omitempty"`
	Y            float64  `json:"y,omitempty"`
	Name         string   `json:"name,omitempty"`
	Label        string   `json:"label,omitempty"`
	Icon         string   `json:"icon,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Wallet       string   `json:"wallet,omitempty"`
	Balance      string   `json:"balance,omitempty"`
	APIKey       string   `json:"apiKey,omitempty"`
	Model        string   `json:"model,omitempty"`
	// KeyMode selects which API key a Provider node's LLM call uses: "byok"
	// (default, empty string) uses APIKey; "platform" uses AgentMesh's own
	// key for that Template, resolved server-side and never round-tripped
	// to the client.
	KeyMode  string `json:"keyMode,omitempty"`
	URL      string `json:"url,omitempty"`
	Method   string `json:"method,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Price    string `json:"price,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Provider string `json:"provider,omitempty"`
	Source   string `json:"source,omitempty"`
	// email action fields
	EmailTo       string `json:"emailTo,omitempty"`
	EmailFrom     string `json:"emailFrom,omitempty"`
	EmailSubject  string `json:"emailSubject,omitempty"`
	EmailBody     string `json:"emailBody,omitempty"`
	EmailAPIKey   string `json:"emailApiKey,omitempty"`
	EmailProvider string `json:"emailProvider,omitempty"`
	// x402 tool discovered params (populated by frontend discover)
	DiscoveredParams []ParamDef `json:"discoveredParams,omitempty"`
	// ParamDefaults holds the values a user typed for DiscoveredParams in the
	// canvas. Applied to the outbound request at call time (query string for
	// GET, JSON body otherwise) — see nodes.applyParamDefaults. Only fills
	// params not already supplied per-call, so an agent's LLM-chosen args
	// still win over these static fallbacks. Non-secret, like Config.
	ParamDefaults map[string]string `json:"paramDefaults,omitempty"`
	// CustomParams are user-defined fields, used alongside (and taking
	// precedence over) DiscoveredParams for endpoints that declare nothing.
	CustomParams []CustomParam `json:"customParams,omitempty"`
	// BodyMode selects how CustomParams/ParamDefaults reach the endpoint:
	// "" or BodyModeParams builds the request from the fields themselves
	// (query string for GET, flat JSON or multipart otherwise), while
	// BodyModeJSON sends BodyTemplate verbatim as the JSON body.
	//
	// The fields-only shape cannot express a nested body, and real endpoints
	// want one: prism-99h2.onrender.com/resume-screen-accurate takes
	// {"task_description": "...", "files": [{"filename": "...",
	// "content_base64": "..."}]} — an array of objects with a file's bytes
	// inside a JSON string. No arrangement of flat key/value fields produces
	// that, and the endpoint declares no schema we could generate a form
	// from, so the caller has to be able to write the body itself.
	BodyMode string `json:"bodyMode,omitempty"`
	// BodyTemplate is the JSON body for BodyModeJSON, with placeholders
	// filled from CustomParams at call time — see nodes.expandBodyTemplate
	// for the supported forms. Attached files stay in CustomParams and are
	// referenced from here rather than uploaded separately, since the whole
	// point of this mode is bodies where a file is one field among others.
	BodyTemplate string `json:"bodyTemplate,omitempty"`
	Description  string `json:"description,omitempty"`
	// Secrets holds per-connector credential values for connectors added after the
	// original dedicated fields (APIKey, EmailAPIKey, ...). Each value is encrypted
	// independently, exactly like EmailAPIKey, via encryptNodes/maskNodes/decryptNodes.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Config holds per-connector non-secret settings (list IDs, project keys, channel
	// names, etc.) for the same connectors. Never encrypted.
	Config map[string]string `json:"config,omitempty"`
	// State node fields. StateOp is one of "get", "set", "increment",
	// "delete". StateValue is the literal to store for "set" (itself
	// subject to {{state.x}} expansion) or the numeric delta for
	// "increment".
	StateOp    string `json:"stateOp,omitempty"`
	StateKey   string `json:"stateKey,omitempty"`
	StateValue string `json:"stateValue,omitempty"`
	// Tendril node fields. TendrilAction is "topup" | "rent" | "run" | "release";
	// TendrilHours is how many hours of credit to guarantee before renting,
	// as a decimal string ("1", "2", "0.5") — a string, like every other
	// canvas-entered value on this struct.
	TendrilAction string `json:"tendrilAction,omitempty"`
	TendrilNodeID string `json:"tendrilNodeId,omitempty"`
	TendrilHours  string `json:"tendrilHours,omitempty"`
	// TendrilAmount is USD of AgentMesh credit to convert into Tendril
	// credit, on a topup node.
	TendrilAmount string `json:"tendrilAmount,omitempty"`
	// TendrilLeaseToken is a bearer the TARGET needs, carried to the relay
	// out of band. Never persisted on a saved workflow — it is only ever set
	// on the synthesized nodes payTendril builds at call time.
	TendrilLeaseToken string `json:"-"`
	// MaxRetries is how many additional attempts the runner makes for this
	// node after a failure classified as safe to retry (see
	// nodes.IsRetryable) — 0 (default) means no automatic retry. A
	// non-retryable error is never retried regardless of this value. Clamped
	// to MaxNodeRetries on save (see handlers.clampRetryFields) so a saved
	// workflow can't hammer a third-party endpoint indefinitely.
	MaxRetries int `json:"maxRetries,omitempty"`
	// RetryBackoffMs is the delay before each retry attempt. 0 with
	// MaxRetries > 0 means retry immediately. Clamped to
	// MaxNodeRetryBackoffMs on save, same reasoning as MaxRetries.
	RetryBackoffMs int `json:"retryBackoffMs,omitempty"`
}

// MaxNodeRetries and MaxNodeRetryBackoffMs bound a node's retry
// configuration so a saved workflow can't hold a run's goroutine open
// indefinitely retrying against a third-party endpoint -- 5 attempts at up
// to 30s apart is generous for real transient failures (a blip, a brief
// rate limit) without letting a single misconfigured or abusive node retry
// for hours.
const (
	MaxNodeRetries        = 5
	MaxNodeRetryBackoffMs = 30_000
)

type WorkflowEdge struct {
	ID     string   `json:"id"`
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   EdgeKind `json:"kind"`
	ToPort string   `json:"toPort,omitempty"`
}

type WorkflowGraph struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

type WorkflowStatus string

const (
	WorkflowStatusDraft    WorkflowStatus = "draft"
	WorkflowStatusDeployed WorkflowStatus = "deployed"
	WorkflowStatusError    WorkflowStatus = "error"
)

type Workflow struct {
	ID          string         `json:"id"`
	UserID      string         `json:"userId,omitempty"`
	Name        string         `json:"name"`
	Status      WorkflowStatus `json:"status"`
	Nodes       []WorkflowNode `json:"nodes"`
	Edges       []WorkflowEdge `json:"edges"`
	DeployedAt  *time.Time     `json:"deployedAt,omitempty"`
	RunEndpoint string         `json:"runEndpoint,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Agents      int            `json:"agents,omitempty"`
	Runs        int            `json:"runs,omitempty"`
	Spend       string         `json:"spend,omitempty"`
	Updated     string         `json:"updated,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	// ScheduleCron is a standard 5-field cron expression (UTC). Empty/nil
	// means the workflow has no schedule -- set via SetWorkflowSchedule,
	// never written directly through UpdateWorkflow's graph save.
	ScheduleCron *string `json:"scheduleCron,omitempty"`
	// ScheduleNextRunAt is when the scheduler will next fire this workflow.
	// Advanced past `now` atomically at claim time (Store.ClaimDueSchedules)
	// so two server replicas polling concurrently can't both fire the same
	// tick.
	ScheduleNextRunAt *time.Time `json:"scheduleNextRunAt,omitempty"`
	// Geofence centre and radius. All three are set or all three are nil --
	// a partially configured zone is meaningless, so the store writes and
	// clears them together and the handler validates them as one unit.
	GeofenceLat     *float64 `json:"geofenceLat,omitempty"`
	GeofenceLng     *float64 `json:"geofenceLng,omitempty"`
	GeofenceRadiusM *float64 `json:"geofenceRadiusM,omitempty"`
	// GeofenceInside is tri-state: nil means no fix has been recorded yet.
	// That is deliberately distinct from false -- see migration 000029. Only
	// a change from a known state counts as a crossing.
	GeofenceInside *bool `json:"geofenceInside,omitempty"`
	// GeofenceLastFixAt is the CLIENT's timestamp for the last fix acted on,
	// not the server's receive time. Offline pings flush in a burst, so the
	// only ordering that means anything is the one the device observed.
	GeofenceLastFixAt *time.Time `json:"geofenceLastFixAt,omitempty"`
	// IsSystem marks a row GetOrCreateSystemWorkflow finds-or-creates to back
	// a partner console (Tendril, Prism) rather than something a user built.
	// Set once, at INSERT, and never touched by UpdateWorkflow -- identity
	// lives in this column, not in the name, precisely so renaming a
	// workflow can never make it (or unmake it) a console. json:"-": this is
	// server-internal bookkeeping, not something the frontend needs to see.
	IsSystem bool `json:"-"`
}

type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
	RunStatusStopped RunStatus = "stopped"
)

type Run struct {
	ID           string     `json:"id"`
	WorkflowID   string     `json:"workflowId"`
	TriggeredBy  string     `json:"triggeredBy"`
	Status       RunStatus  `json:"status"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	InputContext any        `json:"inputContext,omitempty"`
}

type LogStatus string

const (
	LogStatusPending LogStatus = "pending"
	LogStatusRunning LogStatus = "running"
	LogStatusSuccess LogStatus = "success"
	LogStatusFailed  LogStatus = "failed"
)

type RunLog struct {
	ID         string    `json:"id"`
	RunID      string    `json:"runId"`
	StepIndex  int       `json:"stepIndex"`
	NodeID     string    `json:"nodeId"`
	NodeType   NodeType  `json:"nodeType"`
	Status     LogStatus `json:"status"`
	Input      any       `json:"input,omitempty"`
	Output     any       `json:"output,omitempty"`
	DurationMs int       `json:"durationMs,omitempty"`
	Ts         time.Time `json:"ts"`
	// ConfigHash is a hash of the node's functionally-relevant config at
	// the moment this row reached LogStatusSuccess -- empty for every
	// status other than success, and for any success row written before
	// this field existed. Never sent to the client (see the "-" tag): it's
	// an internal detail of Resume's skip-on-prior-success check (see
	// engine.nodeConfigHash), not something a UI needs to render.
	ConfigHash string `json:"-"`
}

// DeadLetterRun records a node that failed permanently -- either a
// non-retryable error, or a retryable one that exhausted MaxRetries -- so a
// dead-lettered run isn't just an opaque "failed" status with no way to find
// it again. AttemptCount is every executeNode call made for this node
// across the run, including the first.
//
// PaymentRisk marks a failure where real money may already have moved --
// e.g. *nodes.ErrBalanceBlocked, where the agent's own LLM turn completed
// and its flat fee was already debited before the attached call it then
// tried ran into insufficient balance. Resume refuses to retry a
// payment-risk row unless explicitly forced, since re-executing the node
// would redo the LLM turn (and its billing) rather than just retrying the
// part that actually failed.
type DeadLetterRun struct {
	ID           string    `json:"id"`
	RunID        string    `json:"runId"`
	NodeID       string    `json:"nodeId"`
	Error        string    `json:"error"`
	AttemptCount int       `json:"attemptCount"`
	PaymentRisk  bool      `json:"paymentRisk"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AgentWallet struct {
	ID                string `json:"id"`
	WorkflowID        string `json:"workflowId"`
	AgentNodeID       string `json:"agentNodeId"`
	Address           string `json:"address"`
	EncryptedMnemonic string `json:"-"`
	Network           string `json:"network"`
}

type LogEvent struct {
	StepIndex  int       `json:"stepIndex"`
	NodeID     string    `json:"nodeId"`
	NodeType   NodeType  `json:"nodeType"`
	Status     LogStatus `json:"status"`
	Output     any       `json:"output,omitempty"`
	DurationMs int       `json:"durationMs,omitempty"`
	Ts         time.Time `json:"ts"`
}

// AttachConfig holds the provider and tools attached to an agent node via "attach" edges.
type AttachConfig struct {
	Provider *WorkflowNode
	Tools    []WorkflowNode
}

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Name         string    `json:"name"`
	OrgName      string    `json:"orgName"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CreditTransaction is one row of the append-only credit_ledger table.
type CreditTransaction struct {
	ID                string     `json:"id"`
	UserID            string     `json:"userId"`
	Provider          string     `json:"provider"`
	ProviderOrderID   string     `json:"providerOrderId"`
	ProviderPaymentID *string    `json:"providerPaymentId,omitempty"`
	Status            string     `json:"status"`
	AmountINRPaise    *int64     `json:"amountInrPaise,omitempty"`
	FXRateUSDPerINR   *float64   `json:"fxRateUsdPerInr,omitempty"`
	AmountUSDCents    *int64     `json:"amountUsdCents,omitempty"`
	CreditUSDMicros   int64      `json:"creditUsdMicros"`
	CreatedAt         time.Time  `json:"createdAt"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
}

// DebitEntry is one row of the append-only debit_ledger table — a platform
// fee charged against a user's credit balance for a metered action inside
// a workflow run.
type DebitEntry struct {
	ID              string    `json:"id"`
	UserID          string    `json:"userId"`
	WorkflowID      string    `json:"workflowId"`
	RunID           string    `json:"runId"`
	NodeID          string    `json:"nodeId"`
	Kind            string    `json:"kind"`
	AmountUSDMicros int64     `json:"amountUsdMicros"`
	CreatedAt       time.Time `json:"createdAt"`
	// Model, TokensIn, TokensOut are only set for DebitKindPlatformKeyLLMFee
	// rows — internal margin-tracking data, not billing-authoritative (the
	// charge is the flat tier fee regardless of actual token count).
	Model     *string `json:"model,omitempty"`
	TokensIn  *int    `json:"tokensIn,omitempty"`
	TokensOut *int    `json:"tokensOut,omitempty"`
}

const (
	DebitKindByokFlatFee       = "byok_flat_fee"
	DebitKindX402PlatformFee   = "x402_platform_fee"
	DebitKindX402RelayCost     = "x402_relay_cost"
	DebitKindPlatformKeyLLMFee = "platform_key_llm_fee"
	DebitKindTendrilLease      = "tendril_lease"
)

// TendrilLease is one rented Tendril machine. A lease deliberately outlives
// the run that opened it: a workflow run finishes in seconds while the machine
// meters for hours, so this is a first-class AgentMesh resource with its own
// release lifecycle rather than run-scoped state.
type TendrilLease struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
	NodeID     string `json:"nodeId"`

	LeaseID          string `json:"leaseId"`
	LeaseTokenEnc    string `json:"-"`
	TendrilNodeID    string `json:"tendrilNodeId"`
	TendrilNodeLabel string `json:"tendrilNodeLabel"`

	SSHHost          string `json:"sshHost"`
	SSHPort          int    `json:"sshPort"`
	SSHUsername      string `json:"sshUsername"`
	SSHCommand       string `json:"sshCommand"`
	SSHPublicKey     string `json:"sshPublicKey"`
	SSHPrivateKeyEnc string `json:"-"`
	SSHPasswordEnc   string `json:"-"`

	RateUSDMicrosPerHour int64      `json:"rateUsdMicrosPerHour"`
	HoursPurchased       float64    `json:"hoursPurchased"`
	ReservedUSDMicros    int64      `json:"reservedUsdMicros"`
	ChargedUSDMicros     *int64     `json:"chargedUsdMicros,omitempty"`
	UsedSeconds          *int64     `json:"usedSeconds,omitempty"`
	Status               string     `json:"status"`
	StartedAt            time.Time  `json:"startedAt"`
	FundedUntil          time.Time  `json:"fundedUntil"`
	ReleasedAt           *time.Time `json:"releasedAt,omitempty"`
}

// OAuthCredential is one persisted, refreshable connection to an external
// OAuth2 provider (Google today; Slack/Microsoft could follow the same
// shape) that a workflow node reads a live access token from. Distinct from
// the sign-in OAuth flow (handlers/oauth.go): that flow authenticates a
// person into AgentMesh and never stores a token; this is a token a node
// calls the provider's own API with, on the user's behalf, long after the
// browser session that connected it is gone -- so AccessToken/RefreshToken
// are the encrypted-at-rest fields, never round-tripped to the frontend.
type OAuthCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"userId"`
	Provider        string    `json:"provider"`
	AccountLabel    string    `json:"accountLabel"`
	AccessTokenEnc  string    `json:"-"`
	RefreshTokenEnc string    `json:"-"`
	Scopes          string    `json:"scopes"`
	ExpiresAt       time.Time `json:"expiresAt"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// TendrilCreditEntry is one row of the append-only tendril_credit_ledger
// table — a movement of a single user's Tendril credit sub-ledger, distinct
// from and never checked against the shared Wallet 2 pool balance.
type TendrilCreditEntry struct {
	ID              string    `json:"id"`
	UserID          string    `json:"userId"`
	Kind            string    `json:"kind"`
	AmountUSDMicros int64     `json:"amountUsdMicros"`
	LeaseID         *string   `json:"leaseId,omitempty"`
	TxID            *string   `json:"txId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

const (
	TendrilCreditKindTopup  = "topup"  // AgentMesh credits -> Tendril credits
	TendrilCreditKindCharge = "charge" // Tendril credits -> compute
	TendrilCreditKindRefund = "refund" // unused reservation returned
)

const (
	ByokFlatFeeUSDMicros     int64 = 500_000   // $0.50
	X402PlatformFeeUSDMicros int64 = 1_500_000 // $1.50
	// X402ProbeFloorUSDMicros is the pre-call balance floor checked before
	// letting an agent make ANY outbound HTTP request to a tool402 node's
	// endpoint -- including the unauthenticated probe that just fetches the
	// 402 price quote, before any real payment exists. Checked at two call
	// sites: nodes.executeFunctionCall (an agent-attached tool402 call not
	// covered by run funding) and Runner.executeNode (a standalone
	// NodeTypeTool402 node, which is never run-funded). Deliberately its own
	// constant, separate from X402PlatformFeeUSDMicros: this one guards
	// against an unfunded caller driving unbounded outbound requests through
	// a tool402 node (SSRF / DoS-amplification risk), not against
	// underpaying -- so it should stay cheap even as the real platform
	// markup moves for pricing reasons.
	X402ProbeFloorUSDMicros int64 = 50_000 // $0.05
	// MaxSingleX402QuoteUSDMicros is a sanity ceiling on any one attached
	// tool402 node's live quote during reserveAndFundRun's estimate
	// summation — generous against any real tool price, tight against an
	// adversarial or compromised target quoting near int64's range to
	// force the running sum to overflow negative.
	MaxSingleX402QuoteUSDMicros int64 = 1_000_000_000 // $1,000/call
)

type X402RelaySettlement struct {
	ID                string    `json:"id"`
	TargetURL         string    `json:"targetUrl"`
	InboundTxID       *string   `json:"inboundTxId,omitempty"`
	OutboundTxID      *string   `json:"outboundTxId,omitempty"`
	AmountAssetMicros int64     `json:"amountAssetMicros"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
}

// X402RunFunding is a single real GoPlausible-facilitated inbound payment
// (Wallet 1 -> Wallet 2) that pre-funds a whole run's worth of downstream
// x402 tool calls, instead of settling one inbound payment per call. Mirrors
// x402_run_fundings' columns exactly.
type X402RunFunding struct {
	ID                string    `json:"id"`
	RunID             string    `json:"runId"`
	InboundTxID       string    `json:"inboundTxId"`
	AmountAssetMicros int64     `json:"amountAssetMicros"`
	CreatedAt         time.Time `json:"createdAt"`
}

// Platform-key LLM tiers follow Zapier's flat-multiplier pattern: a fixed
// credit charge per tier per call, known upfront so it can feed the pre-run
// cost estimate, rather than live per-token metering. Token usage is still
// captured (DebitEntry.TokensIn/TokensOut) purely to confirm these
// multiples hold a healthy margin against real provider cost as
// pricing/models change. Tier ratio (1x/3x/5x) is relative to the economy
// tier itself, not to ByokFlatFeeUSDMicros — the two moved independently.
const (
	PlatformKeyEconomyFeeUSDMicros  int64 = 30_000  // $0.03 (1x)
	PlatformKeyStandardFeeUSDMicros int64 = 90_000  // $0.09 (3x)
	PlatformKeyFrontierFeeUSDMicros int64 = 150_000 // $0.15 (5x)
)
