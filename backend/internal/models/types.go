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
	Description  string        `json:"description,omitempty"`
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
}

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
)

const (
	ByokFlatFeeUSDMicros     int64 = 10_000  // $0.01
	X402PlatformFeeUSDMicros int64 = 500_000 // $0.50
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
// credit charge per tier per call (1x/3x/5x the BYOK convenience-fee unit),
// known upfront so it can feed the pre-run cost estimate, rather than live
// per-token metering. Token usage is still captured (DebitEntry.TokensIn/
// TokensOut) purely to confirm these multiples hold a healthy margin
// against real provider cost as pricing/models change.
const (
	PlatformKeyEconomyFeeUSDMicros  int64 = 10_000 // $0.01 (1x)
	PlatformKeyStandardFeeUSDMicros int64 = 30_000 // $0.03 (3x)
	PlatformKeyFrontierFeeUSDMicros int64 = 50_000 // $0.05 (5x)
)
