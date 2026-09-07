import {
  NodeTypeMeta,
  Workflow,
  WorkflowNode,
  UsageRange,
  UsageCategory,
  UsagePayload,
  EndpointUsage,
  Settlement,
  UsagePoint,
  WorkflowSpend,
} from "./types";

export const NODE_TYPES: Record<string, NodeTypeMeta> = {
  trigger: { w: 200, h: 60, ports: ["out"] },
  agent: { w: 260, h: 124, ports: ["in", "out", "model", "tools"] },
  provider: { w: 220, h: 76, ports: ["top"] },
  tool: { w: 200, h: 64, ports: ["top", "in", "out"] },
  tool402: { w: 220, h: 84, ports: ["top", "in", "out"] },
  action: { w: 200, h: 64, ports: ["in", "out"] },
  state: { w: 200, h: 64, ports: ["in", "out"] },
  end: { w: 200, h: 60, ports: ["in"] },
  tendril: { w: 240, h: 96, ports: ["in", "out", "top"] },
  // Flow-only (no "top" attach port) -- unlike tool/tool402, a Google node
  // was never made agent-attachable on the backend (isValidConnection's
  // attach list below doesn't include "google"), so there's no second port
  // set to offer here.
  google: { w: 220, h: 76, ports: ["in", "out"] },
};

// "cron" (Schedule) intentionally omitted: there is no scheduler in the
// backend (grep -ri "cron|schedul" backend/internal turns up nothing
// non-test), so a workflow whose only trigger is Schedule would never fire
// on its own. Re-add once a real scheduler exists -- see the node-cleanup
// plan's Part B5.
export const TRIGGER_TEMPLATES = [
  { id: "manual", name: "Manual Trigger", desc: "Click to test", icon: "▶" },
  { id: "chat", name: "On Chat Message", desc: "Inbound chat", icon: "◴" },
  { id: "webhook", name: "Webhook", desc: "HTTP POST endpoint", icon: "◷" },
];

export const AGENT_TEMPLATES = [
  { id: "agent", name: "AI Agent", desc: "Reasoning + tool use", icon: "◇" },
  {
    id: "router",
    name: "Router Agent",
    desc: "Classify + dispatch",
    icon: "◊",
  },
  { id: "human", name: "Human-in-loop", desc: "Approval gate", icon: "○" },
];

export const PROVIDER_TEMPLATES = [
  { id: "gemini", name: "Google Gemini", model: "gemini-2.5-flash", icon: "G" },
  { id: "openai", name: "OpenAI", model: "gpt-4.1", icon: "O" },
  { id: "anthropic", name: "Anthropic", model: "claude-sonnet-4-6", icon: "A" },
  { id: "mistral", name: "Mistral", model: "mistral-large-latest", icon: "M" },
  { id: "groq", name: "Groq", model: "llama-3.3-70b-versatile", icon: "q" },
];

// One shared OAuth connection (Config.oauthCredentialID) covers all four
// products -- see backend/internal/api/handlers/oauth2creds.go's
// googleConnectorScopes, requested together in one consent screen.
// "product" groups the palette's Google tab into sections the way
// ACTION_CATEGORIES groups the Actions tab.
export const GOOGLE_PRODUCTS = ["Gmail", "Sheets", "Calendar", "Drive"] as const;

// usesMessage marks the operations that actually send/write something and
// so benefit from a {{ }} message template (see resolveMessage/
// expandTemplate in connector_helpers.go) -- mirrors the write-op cases in
// backend/internal/engine/nodes/google.go (gmail_send/gmail_reply/
// sheets_append/calendar_create) so the Inspector's Message section can
// derive from this table instead of keeping its own separate id list.
export const GOOGLE_TEMPLATES = [
  { id: "gmail_list", name: "Gmail: List Messages", desc: "Search/list inbox messages", icon: "✉", product: "Gmail" },
  { id: "gmail_get", name: "Gmail: Get Message", desc: "Read one message's content", icon: "✉", product: "Gmail" },
  { id: "gmail_send", name: "Gmail: Send Message", desc: "Send a new email", icon: "✉", product: "Gmail", usesMessage: true },
  { id: "gmail_reply", name: "Gmail: Reply", desc: "Reply within a thread", icon: "✉", product: "Gmail", usesMessage: true },
  { id: "sheets_read", name: "Sheets: Read Range", desc: "Read cell values", icon: "▦", product: "Sheets" },
  { id: "sheets_append", name: "Sheets: Append Row", desc: "Add a row of data", icon: "▦", product: "Sheets", usesMessage: true },
  { id: "calendar_list", name: "Calendar: List Events", desc: "List upcoming events", icon: "◔", product: "Calendar" },
  { id: "calendar_create", name: "Calendar: Create Event", desc: "Schedule a new event", icon: "◔", product: "Calendar", usesMessage: true },
  { id: "drive_list", name: "Drive: List Files", desc: "Search/list files", icon: "▤", product: "Drive" },
  { id: "drive_get", name: "Drive: Get File Info", desc: "Read file metadata", icon: "▤", product: "Drive" },
  { id: "drive_download", name: "Drive: Download File", desc: "Fetch file contents", icon: "▤", product: "Drive" },
];

// Display-only mirror of backend/internal/engine/nodes/tier.go's modelTiers
// map -- the backend is the billing-authoritative source; this only drives
// the Inspector's tier badge so the fee is visible before a run happens.
// Keep in sync by hand when either the model dropdowns or the Go tier map
// change.
export const MODEL_TIERS: Record<
  string,
  Record<string, "economy" | "standard" | "frontier">
> = {
  gemini: {
    "gemini-2.5-flash": "economy",
    "gemini-2.0-flash": "economy",
    "gemini-1.5-flash": "economy",
    "gemini-2.5-pro": "standard",
    "gemini-1.5-pro": "standard",
  },
  openai: {
    "gpt-4o-mini": "economy",
    "o4-mini": "economy",
    "gpt-4.1": "standard",
    "gpt-4o": "standard",
    o3: "frontier",
  },
  anthropic: {
    "claude-haiku-4-5": "economy",
    "claude-sonnet-4-6": "standard",
    "claude-3-5-sonnet-20241022": "standard",
    "claude-opus-4-8": "frontier",
  },
  groq: {
    "llama-3.1-8b-instant": "economy",
    "gemma2-9b-it": "economy",
    "llama-3.3-70b-versatile": "standard",
    "mixtral-8x7b-32768": "standard",
  },
  mistral: {
    "mistral-small-latest": "economy",
    "codestral-latest": "economy",
    "mistral-large-latest": "standard",
    "mistral-medium-latest": "standard",
  },
};

// Display-only mirror of backend/internal/models.PlatformKeyEconomy/Standard/
// FrontierFeeUSDMicros -- same hand-sync caveat as MODEL_TIERS above: the
// backend is billing-authoritative, this only drives the Inspector's fee
// badge. Keep in sync by hand when the Go constants change.
export const TIER_FEES: Record<"economy" | "standard" | "frontier", number> =
  {
    economy: 0.03,
    standard: 0.09,
    frontier: 0.15,
  };

// modelTier mirrors nodes.ModelTier's default: unrecognized template/model
// pairs are "standard", never "economy".
export function modelTier(
  template: string,
  model: string,
): "economy" | "standard" | "frontier" {
  return MODEL_TIERS[template]?.[model] ?? "standard";
}

// "code" (Pinecone/pgvector-style vector store, JS/Python inline) and "memory"
// removed: neither has a case in ExecuteTool (backend/internal/engine/nodes/
// tool.go), so both fell through to a default case that echoed the input
// back and reported success, rendering a green node that did nothing. Real
// inline code execution exists today via the Tendril tab's "Run a Job" node
// (metered Python over x402); route there instead of reintroducing a stub.
export const TOOL_TEMPLATES = [
  { id: "http", name: "HTTP Request", desc: "GET/POST any URL", icon: "⟶" },
  { id: "calc", name: "Calculator", desc: "Math expressions", icon: "Σ" },
  { id: "set", name: "Edit Fields", desc: "Build an object from refs", icon: "≔" },
  { id: "json_extract", name: "JSON Extract", desc: "Pick a value by path", icon: "⌗" },
  { id: "crypto", name: "Crypto", desc: "Hash / HMAC / base64", icon: "⚿" },
  { id: "datetime", name: "Date & Time", desc: "Now, offset, timezone", icon: "◔" },
  { id: "xml", name: "XML → JSON", desc: "Parse XML payloads", icon: "⋔" },
  { id: "template", name: "Text Template", desc: "Compose with {{ refs }}", icon: "¶" },
  { id: "html_extract", name: "HTML Extract", desc: "CSS selector → text", icon: "⌸" },
  { id: "markdown", name: "Markdown → HTML", desc: "Render agent output", icon: "⌘" },
  { id: "quickchart", name: "QuickChart", desc: "Chart image URL", icon: "▦" },
  {
    id: "websearch",
    name: "Web Search",
    desc: "Search the live web (Gemini grounding)",
    icon: "⌕",
  },
];

// TOOL402_TEMPLATES removed: the x402 tab's palette `map` never set an
// `endpoint` (PalettePanel.tsx), and the providers advertised here
// (tavily.x402, firecrawl.x402, etc.) were invented hostnames -- nothing
// real is reachable at any of them. The working path is the "New x402
// Endpoint" custom creator (paste a real URL, Discover probes the live 402
// challenge for method/price), which stays untouched by this removal.

// ACTION_CATEGORIES mirrors the backend's own connector grouping
// (connectors_{messaging,productivity,devtools,data,media}.go) so the palette's
// grouping can't silently drift from how the connectors are actually organized
// server-side. "Email" is its own bucket rather than folded into Messaging --
// it lives directly in action.go, not a connectors_*.go file, and has a
// materially different shape (provider dropdown, from/subject/body) than a
// webhook-post connector.
export const ACTION_CATEGORIES = [
  "Messaging",
  "Email",
  "Productivity",
  "Developer Tools",
  "Data & CRM",
  "Commerce",
  "Support",
  "Media",
  "Utilities",
] as const;

export const ACTION_TEMPLATES = [
  {
    id: "email",
    name: "Send Email",
    desc: "Postmark / Resend",
    icon: "✉",
    category: "Email",
  },
  {
    id: "slack",
    name: "Slack Message",
    desc: "Post to channel",
    icon: "#",
    category: "Messaging",
  },
  {
    id: "db",
    name: "Database Insert",
    desc: "Write to Postgres",
    icon: "⛁",
    category: "Data & CRM",
  },
  {
    id: "discord",
    name: "Discord Message",
    desc: "Webhook post",
    icon: "d",
    category: "Messaging",
  },
  {
    id: "teams",
    name: "Teams Message",
    desc: "Webhook post",
    icon: "T",
    category: "Messaging",
  },
  {
    id: "google_chat",
    name: "Google Chat Message",
    desc: "Webhook post",
    icon: "G",
    category: "Messaging",
  },
  {
    id: "ntfy",
    name: "Ntfy Push",
    desc: "Topic notification",
    icon: "n",
    category: "Messaging",
  },
  {
    id: "telegram",
    name: "Telegram Message",
    desc: "Bot API send",
    icon: "t",
    category: "Messaging",
  },
  {
    id: "telegram_get_updates",
    name: "Telegram Get Updates",
    desc: "Read new bot messages",
    icon: "t",
    category: "Messaging",
  },
  {
    id: "github",
    name: "GitHub Issue",
    desc: "Create an issue",
    icon: "gh",
    category: "Developer Tools",
  },
  {
    id: "notion",
    name: "Notion Block",
    desc: "Append to a page",
    icon: "N",
    category: "Productivity",
  },
  {
    id: "airtable",
    name: "Airtable Record",
    desc: "Create a record",
    icon: "A",
    category: "Productivity",
  },
  {
    id: "hubspot",
    name: "HubSpot Note",
    desc: "Log a CRM note",
    icon: "hs",
    category: "Data & CRM",
  },
  {
    id: "trello",
    name: "Trello Card",
    desc: "Create a card",
    icon: "tr",
    category: "Productivity",
  },
  {
    id: "asana",
    name: "Asana Task",
    desc: "Create a task",
    icon: "as",
    category: "Productivity",
  },
  {
    id: "clickup",
    name: "ClickUp Task",
    desc: "Create a task",
    icon: "cu",
    category: "Productivity",
  },
  {
    id: "jira",
    name: "Jira Issue",
    desc: "Create an issue",
    icon: "J",
    category: "Developer Tools",
  },
  {
    id: "mailchimp",
    name: "Mailchimp Subscriber",
    desc: "Add to a list",
    icon: "mc",
    category: "Data & CRM",
  },
  {
    id: "linear",
    name: "Linear Issue",
    desc: "Create an issue",
    icon: "L",
    category: "Developer Tools",
  },
  {
    id: "todoist",
    name: "Todoist Task",
    desc: "Create a task",
    icon: "td",
    category: "Productivity",
  },
  {
    id: "gitlab",
    name: "GitLab Issue",
    desc: "Create an issue",
    icon: "gl",
    category: "Developer Tools",
  },
  {
    id: "sentry",
    name: "Sentry Event",
    desc: "Capture a message",
    icon: "S",
    category: "Developer Tools",
  },
  {
    id: "supabase",
    name: "Supabase Insert",
    desc: "Insert a row",
    icon: "sb",
    category: "Data & CRM",
  },
  {
    id: "woocommerce",
    name: "WooCommerce Note",
    desc: "Add an order note",
    icon: "wc",
    category: "Data & CRM",
  },
  {
    id: "elevenlabs",
    name: "ElevenLabs Speech",
    desc: "Text to speech",
    icon: "11",
    category: "Media",
  },
  {
    id: "twilio",
    name: "Twilio SMS",
    desc: "Send a text message",
    icon: "tw",
    category: "Messaging",
  },
  {
    id: "stripe",
    name: "Stripe Customer",
    desc: "Create a customer",
    icon: "$",
    category: "Commerce",
  },
  {
    id: "shopify",
    name: "Shopify Order Note",
    desc: "Add a note to an order",
    icon: "sp",
    category: "Commerce",
  },
  {
    id: "shopify_customer",
    name: "Shopify Customer",
    desc: "Create a customer",
    icon: "sp",
    category: "Commerce",
  },
  {
    id: "zendesk",
    name: "Zendesk Ticket",
    desc: "Create a support ticket",
    icon: "zd",
    category: "Support",
  },
  {
    id: "intercom",
    name: "Intercom Lead",
    desc: "Create a lead contact",
    icon: "ic",
    category: "Support",
  },
  {
    id: "pagerduty",
    name: "PagerDuty Incident",
    desc: "Trigger an incident",
    icon: "pd",
    category: "Developer Tools",
  },
  {
    id: "calendly",
    name: "Calendly Events",
    desc: "List scheduled events",
    icon: "cy",
    category: "Productivity",
  },
  {
    id: "baserow",
    name: "Baserow Row",
    desc: "Create a row",
    icon: "br",
    category: "Productivity",
  },
  {
    id: "openweathermap",
    name: "OpenWeatherMap",
    desc: "Get current weather",
    icon: "wx",
    category: "Utilities",
  },
  {
    id: "mattermost",
    name: "Mattermost Message",
    desc: "Webhook post",
    icon: "mm",
    category: "Messaging",
  },
  {
    id: "monday",
    name: "Monday.com Item",
    desc: "Create a board item",
    icon: "mo",
    category: "Productivity",
  },
  {
    id: "pipedrive",
    name: "Pipedrive Note",
    desc: "Log a CRM note",
    icon: "pi",
    category: "Data & CRM",
  },
  {
    id: "rss",
    name: "RSS Feed",
    desc: "Read a feed (no key)",
    icon: "rs",
    category: "Utilities",
  },
  {
    id: "graphql",
    name: "GraphQL Query",
    desc: "Any GraphQL endpoint",
    icon: "gq",
    category: "Developer Tools",
  },
  {
    id: "hackernews",
    name: "Hacker News",
    desc: "Search stories (no key)",
    icon: "hn",
    category: "Utilities",
  },
  {
    id: "coingecko",
    name: "CoinGecko Price",
    desc: "Spot prices (no key)",
    icon: "cg",
    category: "Utilities",
  },
];

// Workflow state: a key/value store scoped to the workflow that persists
// between runs. One template per operation rather than one "State" node
// with a mode field, so the palette shows what you can actually do with it
// and a dropped node already has its op set.
export const STATE_TEMPLATES = [
  {
    id: "get",
    name: "Read State",
    desc: "Load a saved value",
    icon: "\u25a4",
  },
  {
    id: "set",
    name: "Write State",
    desc: "Save a value for next run",
    icon: "\u25a5",
  },
  {
    id: "increment",
    name: "Counter",
    desc: "Add to a running total",
    icon: "\u002b",
  },
  {
    id: "delete",
    name: "Clear State",
    desc: "Remove a saved value",
    icon: "\u00d7",
  },
];

export const END_TEMPLATES = [
  { id: "http", name: "Respond to Webhook", desc: "Return JSON", icon: "◳" },
  { id: "done", name: "End", desc: "Mark complete", icon: "■" },
];

export const TENDRIL_TEMPLATES = [
  {
    id: "tendril_topup",
    name: "Buy Tendril Credit",
    desc: "AgentMesh credits → Tendril credit",
    action: "topup" as const,
    icon: "＄",
  },
  {
    id: "tendril_rent",
    name: "Rent a Machine",
    desc: "Open a metered SSH session",
    action: "rent" as const,
    icon: "▣",
  },
  {
    id: "tendril_run",
    name: "Run a Job",
    desc: "Execute Python on the machine",
    action: "run" as const,
    icon: "▶",
  },
  {
    id: "tendril_release",
    name: "Release",
    desc: "Stop the meter and bill",
    action: "release" as const,
    icon: "■",
  },
];

export const SAMPLE_WORKFLOW: Workflow = {
  id: "wf-weather",
  name: "Weather Agent Test",
  nodes: [
    {
      id: "n1",
      type: "trigger",
      template: "chat",
      x: 80,
      y: 220,
      label: "Chat trigger",
    },
    {
      id: "n2",
      type: "agent",
      template: "agent",
      x: 380,
      y: 200,
      name: "Weather Agent",
      systemPrompt:
        "You receive a message from the user. Use the x402 weather tool to get current weather for any city they mention, then return a clear, friendly summary of the conditions. If no city is mentioned, ask the user which city they want.",
    },
    {
      id: "n3",
      type: "provider",
      template: "gemini",
      x: 300,
      y: 430,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
    },
    {
      id: "n4",
      type: "tool402",
      custom: true,
      x: 500,
      y: 430,
      name: "x402 Weather",
      description:
        "Real-time weather data: temperature, wind, conditions for any city worldwide. Accepts: city (string, required), units (celsius|fahrenheit, optional).",
      endpoint: "http://localhost:4402/weather",
      price: "0.065",
      unit: "call",
      priceLive: true,
      discoveredParams: [
        {
          name: "city",
          type: "string",
          required: true,
          description: "City name (e.g. London, Tokyo)",
        },
        {
          name: "units",
          type: "string",
          required: false,
          description: "celsius or fahrenheit",
          default: "celsius",
        },
      ],
    },
    {
      id: "n5",
      type: "action",
      template: "email",
      x: 700,
      y: 200,
      name: "Send Result Email",
    },
    { id: "n6", type: "end", template: "done", x: 960, y: 210 },
  ],
  edges: [
    { id: "e1", from: "n1", to: "n2", kind: "flow", toPort: "in" },
    { id: "e2", from: "n3", to: "n2", kind: "attach", toPort: "model" },
    { id: "e3", from: "n4", to: "n2", kind: "attach", toPort: "tools" },
    { id: "e4", from: "n2", to: "n5", kind: "flow", toPort: "in" },
    { id: "e5", from: "n5", to: "n6", kind: "flow", toPort: "in" },
  ],
};

// DEMO_WORKFLOW mirrors the live workflow at /workflows/4b47fe4a-df79-4cf4-
// a9d6-3abb0bcdda79 (pulled directly from the workflows.graph column) --
// this is that workflow's actual node graph, node-for-node, including the
// system-prompt edits and the freshly-added manual trigger made in the
// canvas UI. One deliberate deviation: that row's two provider nodes were
// saved as keyMode "byok" with `apiKey: "enc:…"` -- the workflow owner's own
// real, encrypted Gemini key. Baking that into this shared, publicly-shipped
// template would mean every user who clicks "Load demo workflow" runs
// their agents on THAT PERSON'S personal Gemini key -- a credential leak
// and a billing problem, not a demo. Both providers are switched to
// keyMode "platform" here instead (no apiKey field at all) so a fresh copy
// uses AgentMesh's own platform key path and bills the flat platform fee,
// same as everyone else's copy of this template.
// Real, verified billing math (backend/internal/engine/runner.go +
// billing.go + models/types.go), not an invented number:
//   - agent node, platform-key mode: flat fee by model tier
//     (nodes.PlatformKeyFeeUSDMicros). gemini-2.5-flash is "economy" in
//     MODEL_TIERS/tier.go = $0.03/call.
//   - tool402/tendril node, real x402 relay: the merchant's live price PLUS
//     a flat $1.50 platform markup (models.X402PlatformFeeUSDMicros) -- NOT
//     $1.50 per node in general, only on an actual x402 relay call. Every
//     x402 node here points at the same REAL, live Bazaar-listed merchant
//     this repo's own backend already relays to (canix402-api.compx.io,
//     see internal/engine/nodes/walletpay.go) -- confirmed live by a direct
//     probe: GET /opportunities returned a real 402 challenge for 0.01 USDC
//     on Algorand mainnet (asset 31566704), tagged x402-global-challenge.
//   - action/google node, or an http-templated tool node, run standalone in
//     the flow: flat $0.50 (models.ByokFlatFeeUSDMicros, gated by
//     nodes.BillableFlatFee).
//   2 economy-tier Gemini 2.5 Flash calls  0.03 + 0.03      = 0.06  guaranteed
//   1 guaranteed CANIX402 x402 call (d8)   0.01 + 1.50      = 1.51  guaranteed
//   1 standalone http tool ("Fetch Data")                   = 0.50  guaranteed
//   -------------------------------------------------------------
//   guaranteed floor                                          $2.07 per successful run
//   + up to 2 more CANIX402 x402 calls (d4/d7, 0.01 + 1.50 = 1.51 each) --
//     billed only if the agent's LLM opts into calling them, so NOT part of
//     the guaranteed floor above. Not capped at one call each either:
//     provider.go allows up to 15 tool-calling iterations per agent, so a
//     run can bill for either tool more than once. $5.09 (both optional
//     calls firing exactly once) is a realistic typical figure, not a ceiling.
//   1 Telegram action ("Post Summary") -- unbilled, see below  = 0.00
// address (both the tool402 paramDefaults and the address text the workflow
// owner pasted into each system prompt) is the real Wallet 2 /
// PLATFORM_WALLET address, same as before.
// d10 ("Post Summary") ships with no telegramBotToken/telegramChatID --
// a shared public template can't embed one user's real credential without
// leaking it to everyone who clicks this button. ExecuteAction
// (connectors_messaging.go) returns ErrActionSkipped when unconfigured,
// which runner.go's NodeTypeAction case treats as a non-failure and never
// bills (see debitOrLog only firing after a non-skip result) -- so this step
// is a real, visible no-op until the user adds their own webhook, not a
// silent failure and not part of the guaranteed cost above.
// d8 (the third canixNode call) is deliberately NOT attached to either
// agent's tools port, unlike d4/d7 -- it's a guaranteed flow step that runs
// on every execution, not something the agent's LLM opts into. This is
// intentional, not an oversight: attaching it to d5 alongside d7 would give
// that agent two tool402 nodes with the identical name "CANIX402
// Opportunities", and toolFuncName() (provider.go) derives the LLM function
// name from Node.Name -- two identical declarations sent to the same model
// would collide. So d4/d7 are the "agent decides" calls (billed only if the
// agent's LLM actually invokes them), and d8 is the one guaranteed real
// CANIX402 call baked into the total above.
const CANIX_ADDRESS =
  "M6JQNJVX32HEN2LS5W2WX2PMSXPDHHKADVUATIQ5KQIVVVHSVILQNOS62A";

function canixNode(
  id: string,
  x: number,
  y: number,
  finalPull = false,
): WorkflowNode {
  return {
    id,
    type: "tool402",
    x,
    y,
    // finalPull (d8) isn't attached to either agent's tools port -- it's a
    // guaranteed flow step, not an agent-invoked one like d4/d7 -- so its
    // name/description say so, both to keep it visually distinct from d4/d7
    // on the canvas and because it can't reuse their exact name without
    // risking a toolFuncName() collision if it's ever attached later.
    name: finalPull
      ? "CANIX402 Opportunities (final pull)"
      : "CANIX402 Opportunities",
    description: finalPull
      ? "Real, live x402-paid DeFi opportunities feed on Algorand mainnet (canix402-api.compx.io). Runs unconditionally as a flow step after the Synthesis Agent finishes -- unlike the two tool402 nodes above, this one is not agent-invoked, so it's billed on every run. Accepts: address (Algorand address, required), limit (optional)."
      : "Real, live x402-paid DeFi opportunities feed on Algorand mainnet (canix402-api.compx.io). Accepts: address (Algorand address, required), limit (optional).",
    endpoint: "https://canix402-api.compx.io/opportunities",
    provider: "canix402.compx.io",
    price: "0.01",
    unit: "call",
    discoveredParams: [
      {
        name: "address",
        type: "string",
        required: true,
        description: "Algorand address to look up opportunities for",
      },
      {
        name: "limit",
        type: "string",
        required: false,
        description: "Max results to return",
      },
    ],
    paramDefaults: { address: CANIX_ADDRESS },
  };
}

export const DEMO_WORKFLOW: Workflow = {
  id: "wf-demo",
  name: "Demo: Research & Report Pipeline",
  nodes: [
    {
      id: "n_1787155279250",
      type: "trigger",
      template: "manual",
      icon: "▶",
      x: 51.57894736842107,
      y: 266.8421052631579,
      label: "Manual Trigger",
    },
    {
      id: "d2",
      type: "agent",
      template: "agent",
      x: 320,
      y: 220,
      name: "Research Agent",
      systemPrompt:
        "You are a research agent. Use the CANIX402 Opportunities tool to pull real DeFi opportunity data, then pass a structured research brief on to the next agent.\naddress M6JQNJVX32HEN2LS5W2WX2PMSXPDHHKADVUATIQ5KQIVVVHSVILQNOS62A",
    },
    {
      id: "d3",
      type: "provider",
      template: "gemini",
      x: 240,
      y: 460,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    canixNode("d4", 440, 460),
    {
      id: "d5",
      type: "agent",
      template: "agent",
      x: 700,
      y: 220,
      name: "Synthesis Agent",
      systemPrompt:
        "You receive a research brief from the prior agent. Use the CANIX402 Opportunities tool for a second, independent data pull, then write a final report summarizing both.\naddress M6JQNJVX32HEN2LS5W2WX2PMSXPDHHKADVUATIQ5KQIVVVHSVILQNOS62A",
    },
    {
      id: "d6",
      type: "provider",
      template: "gemini",
      x: 618.9473684210526,
      y: 461.05263157894734,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    canixNode("d7", 820, 460),
    canixNode("d8", 980, 220, true),
    {
      id: "d9",
      type: "tool",
      template: "http",
      x: 1220,
      y: 220,
      name: "Fetch Data",
      url: "https://httpbin.org/get",
      method: "GET",
    },
    {
      id: "d10",
      type: "action",
      template: "telegram",
      x: 1460,
      y: 220,
      name: "Post Summary",
      description:
        "Posts the pipeline's report to Telegram -- add your own bot token (Secrets) and chat ID (Config) in this node's settings to enable it. Unconfigured, this step no-ops (green, unbilled) rather than failing.",
    },
    { id: "d11", type: "end", template: "done", x: 1700, y: 220 },
  ],
  edges: [
    {
      id: "e_1787155284330",
      from: "n_1787155279250",
      to: "d2",
      kind: "flow",
      toPort: "in",
    },
    { id: "de2", from: "d3", to: "d2", kind: "attach", toPort: "model" },
    { id: "de3", from: "d4", to: "d2", kind: "attach", toPort: "tools" },
    { id: "de4", from: "d2", to: "d5", kind: "flow", toPort: "in" },
    { id: "de5", from: "d6", to: "d5", kind: "attach", toPort: "model" },
    { id: "de6", from: "d7", to: "d5", kind: "attach", toPort: "tools" },
    { id: "de7", from: "d5", to: "d8", kind: "flow", toPort: "in" },
    { id: "de8", from: "d8", to: "d9", kind: "flow", toPort: "in" },
    { id: "de9", from: "d9", to: "d10", kind: "flow", toPort: "in" },
    { id: "de10", from: "d10", to: "d11", kind: "flow", toPort: "in" },
  ],
};

// TENDRIL_DEMO_WORKFLOW and PRISM_DEMO_WORKFLOW are DEMO_WORKFLOW's shape
// (trigger -> two agents, each with its own model and an agent-invoked
// tool402 call -> one guaranteed flow-step tool402 call -> an http tool ->
// an unconfigured, unbilled action -> end), with the CANIX402 calls swapped
// for the partner the button is named after. Same reason as DEMO_WORKFLOW
// itself: real, live-callable endpoints and correct billing math, not an
// invented example.
//
// Neither node type needs the partner's console: curated:tendril-run has no
// lease step ("No lease needed -- Tendril picks the machine, runs the job in
// a throwaway sandbox, and destroys it", curated.go) and is plainly payable
// with one string param, and code-review-fast/accurate take two plain-text
// query params (raw_url, file_path) -- unlike resume-screen, nothing here
// needs a file upload, so an ordinary discoveredParams/paramDefaults tool402
// node (the same shape "Add to workflow" already produces for a curated
// entry) is enough. Both point at the real endpoints in
// backend/internal/bazaar/curated.go and backend/internal/prism/endpoints.go
// -- not a copy that can drift, since a run node's own field values are what
// get sent regardless.

// tendrilNode mirrors canixNode's shape one row up: a self-contained
// tool402 node pointed at Tendril's one payable endpoint, requiring no
// separate rent/lease step.
function tendrilNode(
  id: string,
  x: number,
  y: number,
  opts: { name: string; description: string; payload: string },
): WorkflowNode {
  return {
    id,
    type: "tool402",
    x,
    y,
    name: opts.name,
    description: opts.description,
    endpoint: "https://tendrilregister.007575.xyz/x402/run",
    method: "POST",
    provider: "tendrilregister.007575.xyz",
    price: "0.01",
    unit: "call",
    discoveredParams: [
      {
        name: "payload",
        type: "string",
        required: true,
        description: "Python source to execute. Its stdout is returned as `result`.",
      },
    ],
    paramDefaults: { payload: opts.payload },
  };
}

export const TENDRIL_DEMO_WORKFLOW: Workflow = {
  id: "wf-demo-tendril",
  name: "Demo: Tendril Codegen Pipeline",
  nodes: [
    {
      id: "t1",
      type: "trigger",
      template: "manual",
      icon: "▶",
      x: 40,
      y: 260,
      label: "Manual Trigger",
    },
    {
      id: "t2",
      type: "agent",
      template: "agent",
      x: 320,
      y: 220,
      name: "Codegen Agent",
      systemPrompt:
        "You are a Python developer. Write a short, correct Python script for the task you're given, then use the Tendril Run tool to actually execute it on rented compute and confirm the output before handing off.",
    },
    {
      id: "t3",
      type: "provider",
      template: "gemini",
      x: 240,
      y: 460,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    tendrilNode("t4", 440, 460, {
      name: "Tendril Run",
      description:
        "Run a Python script on rented compute and get its stdout back. No lease needed -- Tendril picks the machine, runs the job in a throwaway sandbox, and destroys it.",
      payload: "print(sum(range(1, 101)))",
    }),
    {
      id: "t5",
      type: "agent",
      template: "agent",
      x: 700,
      y: 220,
      name: "Verification Agent",
      systemPrompt:
        "You receive a script and its claimed output from the prior agent. Write a small independent check (e.g. recompute the same result a different way) and run it with the Tendril Run tool to confirm the first agent's output is actually correct before reporting.",
    },
    {
      id: "t6",
      type: "provider",
      template: "gemini",
      x: 618,
      y: 461,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    tendrilNode("t7", 820, 460, {
      name: "Tendril Run",
      description:
        "Run a Python script on rented compute and get its stdout back. No lease needed -- Tendril picks the machine, runs the job in a throwaway sandbox, and destroys it.",
      payload: "print(sorted([5, 3, 9, 1]))",
    }),
    tendrilNode("t8", 980, 220, {
      name: "Tendril Run (final pull)",
      description:
        "Runs unconditionally as a flow step after the Verification Agent finishes -- unlike the two tool402 nodes above, this one is not agent-invoked, so it's billed on every run.",
      payload: "print('pipeline complete')",
    }),
    {
      id: "t9",
      type: "tool",
      template: "http",
      x: 1220,
      y: 220,
      name: "Fetch Data",
      url: "https://httpbin.org/get",
      method: "GET",
    },
    {
      id: "t10",
      type: "action",
      template: "telegram",
      x: 1460,
      y: 220,
      name: "Post Summary",
      description:
        "Posts the pipeline's report to Telegram -- add your own bot token (Secrets) and chat ID (Config) in this node's settings to enable it. Unconfigured, this step no-ops (green, unbilled) rather than failing.",
    },
    { id: "t11", type: "end", template: "done", x: 1700, y: 220 },
  ],
  edges: [
    { id: "te1", from: "t1", to: "t2", kind: "flow", toPort: "in" },
    { id: "te2", from: "t3", to: "t2", kind: "attach", toPort: "model" },
    { id: "te3", from: "t4", to: "t2", kind: "attach", toPort: "tools" },
    { id: "te4", from: "t2", to: "t5", kind: "flow", toPort: "in" },
    { id: "te5", from: "t6", to: "t5", kind: "attach", toPort: "model" },
    { id: "te6", from: "t7", to: "t5", kind: "attach", toPort: "tools" },
    { id: "te7", from: "t5", to: "t8", kind: "flow", toPort: "in" },
    { id: "te8", from: "t8", to: "t9", kind: "flow", toPort: "in" },
    { id: "te9", from: "t9", to: "t10", kind: "flow", toPort: "in" },
    { id: "te10", from: "t10", to: "t11", kind: "flow", toPort: "in" },
  ],
};

// prismCodeReviewNode mirrors canixNode/tendrilNode. Deliberately restricted
// to code-review-fast/accurate: those take flat text query params (raw_url,
// file_path), unlike resume-screen's nested files array, so they are the
// only Prism endpoints an ordinary discoveredParams tool402 node can call --
// see backend/internal/bazaar/curated.go on why Prism otherwise needs its
// console.
function prismCodeReviewNode(
  id: string,
  x: number,
  y: number,
  opts: { name: string; description: string; tier: "fast" | "accurate"; rawUrl: string; filePath: string },
): WorkflowNode {
  return {
    id,
    type: "tool402",
    x,
    y,
    name: opts.name,
    description: opts.description,
    endpoint: `https://prism-99h2.onrender.com/code-review-${opts.tier}`,
    method: "GET",
    provider: "prism-99h2.onrender.com",
    price: opts.tier === "accurate" ? "0.20" : "0.10",
    unit: "call",
    discoveredParams: [
      {
        name: "raw_url",
        type: "string",
        required: true,
        description:
          "Public raw-text link to the file to review (e.g. a GitHub Raw URL).",
      },
      {
        name: "file_path",
        type: "string",
        required: true,
        description: "The file's name, extension included -- tells Prism which language to expect.",
      },
    ],
    paramDefaults: { raw_url: opts.rawUrl, file_path: opts.filePath },
  };
}

const PRISM_DEMO_RAW_URL =
  "https://raw.githubusercontent.com/octocat/Hello-World/master/README";

export const PRISM_DEMO_WORKFLOW: Workflow = {
  id: "wf-demo-prism",
  name: "Demo: Prism Code Review Pipeline",
  nodes: [
    {
      id: "p1",
      type: "trigger",
      template: "manual",
      icon: "▶",
      x: 40,
      y: 260,
      label: "Manual Trigger",
    },
    {
      id: "p2",
      type: "agent",
      template: "agent",
      x: 320,
      y: 220,
      name: "Review Agent",
      systemPrompt:
        "You review code for bugs and security issues. Use the Prism Code Review tool on the file you're given, then summarize the findings as a short, prioritized list for the next agent.",
    },
    {
      id: "p3",
      type: "provider",
      template: "gemini",
      x: 240,
      y: 460,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    prismCodeReviewNode("p4", 440, 460, {
      name: "Prism Code Review (quick)",
      description:
        "A quick pass over one file for bugs, security issues and obvious mistakes. Answers in seconds. Accepts: raw_url (required), file_path (required).",
      tier: "fast",
      rawUrl: PRISM_DEMO_RAW_URL,
      filePath: "README",
    }),
    {
      id: "p5",
      type: "agent",
      template: "agent",
      x: 700,
      y: 220,
      name: "Triage Agent",
      systemPrompt:
        "You receive a findings list from the prior agent. Use the Prism Code Review tool for a second, thorough pass, then write a final triage report ranking every finding from both passes by severity.",
    },
    {
      id: "p6",
      type: "provider",
      template: "gemini",
      x: 618,
      y: 461,
      name: "Gemini 2.5 Flash",
      model: "gemini-2.5-flash",
      keyMode: "platform",
    },
    prismCodeReviewNode("p7", 820, 460, {
      name: "Prism Code Review (quick)",
      description:
        "A quick pass over one file for bugs, security issues and obvious mistakes. Answers in seconds. Accepts: raw_url (required), file_path (required).",
      tier: "fast",
      rawUrl: PRISM_DEMO_RAW_URL,
      filePath: "README",
    }),
    prismCodeReviewNode("p8", 980, 220, {
      name: "Prism Code Review (thorough, final pull)",
      description:
        "A careful, senior-level review with a proper security pass. Runs unconditionally as a flow step after the Triage Agent finishes -- unlike the two tool402 nodes above, this one is not agent-invoked, so it's billed on every run.",
      tier: "accurate",
      rawUrl: PRISM_DEMO_RAW_URL,
      filePath: "README",
    }),
    {
      id: "p9",
      type: "tool",
      template: "http",
      x: 1220,
      y: 220,
      name: "Fetch Data",
      url: "https://httpbin.org/get",
      method: "GET",
    },
    {
      id: "p10",
      type: "action",
      template: "telegram",
      x: 1460,
      y: 220,
      name: "Post Summary",
      description:
        "Posts the triage report to Telegram -- add your own bot token (Secrets) and chat ID (Config) in this node's settings to enable it. Unconfigured, this step no-ops (green, unbilled) rather than failing.",
    },
    { id: "p11", type: "end", template: "done", x: 1700, y: 220 },
  ],
  edges: [
    { id: "pe1", from: "p1", to: "p2", kind: "flow", toPort: "in" },
    { id: "pe2", from: "p3", to: "p2", kind: "attach", toPort: "model" },
    { id: "pe3", from: "p4", to: "p2", kind: "attach", toPort: "tools" },
    { id: "pe4", from: "p2", to: "p5", kind: "flow", toPort: "in" },
    { id: "pe5", from: "p6", to: "p5", kind: "attach", toPort: "model" },
    { id: "pe6", from: "p7", to: "p5", kind: "attach", toPort: "tools" },
    { id: "pe7", from: "p5", to: "p8", kind: "flow", toPort: "in" },
    { id: "pe8", from: "p8", to: "p9", kind: "flow", toPort: "in" },
    { id: "pe9", from: "p9", to: "p10", kind: "flow", toPort: "in" },
    { id: "pe10", from: "p10", to: "p11", kind: "flow", toPort: "in" },
  ],
};

export const WORKFLOWS: Workflow[] = [
  {
    id: "wf-triage",
    name: "Customer Support Triage",
    status: "deployed",
    updated: "2m ago",
    agents: 1,
    runs: 1842,
    spend: "4.218",
    tags: ["support", "production"],
    nodes: [],
    edges: [],
  },
  {
    id: "wf-brief",
    name: "Daily Market Brief",
    status: "deployed",
    updated: "1h ago",
    agents: 4,
    runs: 38,
    spend: "1.482",
    tags: ["research"],
    nodes: [],
    edges: [],
  },
  {
    id: "wf-invoice",
    name: "Invoice Reconciliation",
    status: "paused",
    updated: "yesterday",
    agents: 2,
    runs: 217,
    spend: "0.890",
    tags: ["finance"],
    nodes: [],
    edges: [],
  },
  {
    id: "wf-leads",
    name: "Lead Enrichment v2",
    status: "draft",
    updated: "3d ago",
    agents: 3,
    runs: 0,
    spend: "0.000",
    tags: ["sales"],
    nodes: [],
    edges: [],
  },
  {
    id: "wf-onchain",
    name: "On-chain Compliance Watch",
    status: "deployed",
    updated: "5h ago",
    agents: 2,
    runs: 642,
    spend: "2.118",
    tags: ["compliance", "production"],
    nodes: [],
    edges: [],
  },
  {
    id: "wf-content",
    name: "Content Pipeline",
    status: "draft",
    updated: "1w ago",
    agents: 5,
    runs: 0,
    spend: "0.000",
    tags: ["marketing"],
    nodes: [],
    edges: [],
  },
];

export const WAITLIST_COUNT = 142;

// ── Usage & Credits fixtures ────────────────────────────────────────────────
// Deterministic mock data so the Usage page is fully developable/demoable
// before the backend exposes /usage/* aggregation endpoints. All numbers are
// synthetic. Kept range-aware: 30d is the base, smaller ranges scale down.

const r6 = (n: number) => Math.round(n * 1e6) / 1e6;

const RANGE_BUCKETS: Record<UsageRange, number> = {
  "24h": 24,
  "7d": 7,
  "30d": 30,
};
const RANGE_MULT: Record<UsageRange, number> = {
  "24h": 0.04,
  "7d": 0.256,
  "30d": 1,
};
const RANGE_DELTA: Record<UsageRange, number> = {
  "24h": 6,
  "7d": 12,
  "30d": 18,
};
// Rows the settlements fixture generates. Keep >= the largest `limit` any caller
// requests (the settlements API default is 20) so a default request isn't
// silently truncated to fewer rows than asked for.
const SETTLEMENT_ROWS = 24;

interface EPSeed {
  endpoint: string;
  host: string;
  provider: string;
  type: UsageCategory;
  unitPrice: number | null;
  unit: string;
  calls30: number;
  success: number | null;
  lastUsedMin: number;
  tokens30?: number;
  estAlgo30?: number;
}

const EP_SEEDS: EPSeed[] = [
  {
    endpoint: "x402 Weather",
    host: "localhost:4402/weather",
    provider: "weatherkit.x402",
    type: "x402",
    unitPrice: 0.065,
    unit: "call",
    calls30: 1204,
    success: 99.2,
    lastUsedMin: 2,
  },
  {
    // Real, live x402 endpoint (confirmed via GET tendrilregister.007575.xyz/platform
    // -- Algorand mainnet USDC, same facilitator this platform's own relay
    // uses). Replaces the former Tavily/Firecrawl rows, which pointed at
    // invented hostnames nothing ever answered.
    endpoint: "Tendril Run",
    host: "tendrilregister.007575.xyz/x402/run",
    provider: "tendril.x402",
    type: "x402",
    unitPrice: 0.01,
    unit: "call",
    calls30: 2140,
    success: 98.6,
    lastUsedMin: 4,
  },
  {
    endpoint: "Tendril Rent",
    host: "tendrilregister.007575.xyz/x402/rent",
    provider: "tendril.x402",
    type: "x402",
    unitPrice: 0.01,
    unit: "hour",
    calls30: 318,
    success: 99.1,
    lastUsedMin: 19,
  },
  {
    endpoint: "AlpacaQuote",
    host: "alpaca.x402/quote",
    provider: "alpaca.x402",
    type: "x402",
    unitPrice: 0.001,
    unit: "quote",
    calls30: 6110,
    success: 99.9,
    lastUsedMin: 1,
  },
  {
    endpoint: "OCR.space",
    host: "ocr.x402/parse",
    provider: "ocr.x402",
    type: "x402",
    unitPrice: 0.003,
    unit: "page",
    calls30: 412,
    success: 95.1,
    lastUsedMin: 140,
  },
  {
    endpoint: "FluxImage",
    host: "flux.x402/generate",
    provider: "flux.x402",
    type: "x402",
    unitPrice: 0.02,
    unit: "image",
    calls30: 168,
    success: 98.8,
    lastUsedMin: 55,
  },
  // LLM unit prices are estimates (hence the * in the UI): provider list prices
  // blended at ~3:1 input:output tokens, converted at the app's 0.17 USD/ALGO
  // display rate. Gemini 2.5 Flash $0.30/$2.50 per 1M → ~$0.85/1M → 5 ALGO/1M.
  // gpt-4o $2.50/$10.00 per 1M → ~$4.375/1M → 26 ALGO/1M. estAlgo30 stays
  // consistent with the price: tokens30/1_000_000 × unitPrice.
  {
    endpoint: "Gemini 2.5 Flash",
    host: "generativelanguage.googleapis.com",
    provider: "google",
    type: "llm",
    unitPrice: 5,
    unit: "1M",
    calls30: 2140,
    success: 99.6,
    lastUsedMin: 2,
    tokens30: 1_180_000,
    estAlgo30: 5.9,
  },
  {
    endpoint: "OpenAI gpt-4o",
    host: "api.openai.com",
    provider: "openai",
    type: "llm",
    unitPrice: 26,
    unit: "1M",
    calls30: 890,
    success: 99.1,
    lastUsedMin: 12,
    tokens30: 640_000,
    estAlgo30: 16.64,
  },
  {
    endpoint: "Resend Email",
    host: "api.resend.com",
    provider: "resend",
    type: "action",
    unitPrice: 0,
    unit: "send",
    calls30: 320,
    success: 100,
    lastUsedMin: 4,
  },
];

const WF_SEEDS = [
  {
    workflowId: "wf-triage",
    name: "Customer Support Triage",
    status: "deployed",
    share: 0.34,
    calls30: 4200,
  },
  {
    workflowId: "wf-onchain",
    name: "On-chain Compliance Watch",
    status: "deployed",
    share: 0.24,
    calls30: 3100,
  },
  {
    workflowId: "wf-brief",
    name: "Daily Market Brief",
    status: "deployed",
    share: 0.18,
    calls30: 1400,
  },
  {
    workflowId: "wf-invoice",
    name: "Invoice Reconciliation",
    status: "paused",
    share: 0.14,
    calls30: 900,
  },
  {
    workflowId: "wf-leads",
    name: "Lead Enrichment v2",
    status: "draft",
    share: 0.1,
    calls30: 480,
  },
];

const TX_B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
function fakeTx(seed: number): string {
  let x = (seed * 2654435761) % 2147483647;
  if (x <= 0) x += 2147483646;
  let s = "";
  for (let i = 0; i < 52; i++) {
    x = (x * 48271) % 2147483647;
    s += TX_B32[x % 32];
  }
  return s;
}

function bucketLabels(range: UsageRange): string[] {
  const n = RANGE_BUCKETS[range];
  const now = Date.now();
  const out: string[] = [];
  for (let i = 0; i < n; i++) {
    const back = n - 1 - i;
    if (range === "24h") {
      const d = new Date(now - back * 3_600_000);
      out.push(`${String(d.getHours()).padStart(2, "0")}:00`);
    } else {
      const d = new Date(now - back * 86_400_000);
      out.push(
        new Intl.DateTimeFormat("en", {
          month: "short",
          day: "numeric",
        }).format(d),
      );
    }
  }
  return out;
}

function buildTimeseries(
  range: UsageRange,
  x402Total: number,
  llmTotal: number,
  x402Calls: number,
): UsagePoint[] {
  const labels = bucketLabels(range);
  const n = labels.length;
  const weights: number[] = [];
  let wsum = 0;
  for (let i = 0; i < n; i++) {
    const w = Math.max(
      0.15,
      0.6 + 0.4 * Math.sin(i * 0.7) + 0.3 * Math.cos(i * 0.31) + (i / n) * 0.5,
    );
    weights.push(w);
    wsum += w;
  }
  return labels.map((ts, i) => {
    const frac = weights[i] / wsum;
    return {
      ts,
      x402Algo: r6(x402Total * frac),
      llmAlgo: r6(llmTotal * frac),
      calls: Math.round(x402Calls * frac),
    };
  });
}

export function buildUsage(range: UsageRange): UsagePayload {
  const mult = RANGE_MULT[range];

  // Endpoints
  const rows: EndpointUsage[] = EP_SEEDS.map((s) => {
    const calls = Math.round(s.calls30 * mult);
    let totalAlgo = 0;
    // LLM spend comes from a token estimate; everything else with a unit price
    // (x402 and priced actions alike) is calls × unitPrice. Keying on type only
    // meant a future priced action endpoint would always be costed at zero.
    if (s.type === "llm") totalAlgo = r6((s.estAlgo30 ?? 0) * mult);
    else if (s.unitPrice != null) totalAlgo = r6(calls * s.unitPrice);
    return {
      endpoint: s.endpoint,
      host: s.host,
      provider: s.provider,
      type: s.type,
      calls,
      unitPrice: s.unitPrice,
      unit: s.unit,
      totalAlgo,
      pctOfSpend: 0,
      successRate: s.success,
      lastUsedAt: new Date(Date.now() - s.lastUsedMin * 60_000).toISOString(),
    };
  });
  const sumSpend = rows.reduce((a, r) => a + r.totalAlgo, 0) || 1;
  rows.forEach((r) => {
    r.pctOfSpend = Math.round((r.totalAlgo / sumSpend) * 1000) / 10;
  });
  rows.sort((a, b) => b.totalAlgo - a.totalAlgo);

  const x402Total = r6(
    rows.filter((r) => r.type === "x402").reduce((a, r) => a + r.totalAlgo, 0),
  );
  const llmTotal = r6(
    rows.filter((r) => r.type === "llm").reduce((a, r) => a + r.totalAlgo, 0),
  );
  const x402Calls = rows
    .filter((r) => r.type === "x402")
    .reduce((a, r) => a + r.calls, 0);
  const llmTokens = Math.round(
    EP_SEEDS.reduce((a, s) => a + (s.tokens30 ?? 0), 0) * mult,
  );

  // Credit balance is account-level -- it must NOT change with the selected chart
  // range. Compute lifetime spend at full scale (no range multiplier) so
  // "credits left" reads the same across 24h / 7d / 30d.
  const lifetimeSpend = r6(
    EP_SEEDS.reduce((a, s) => {
      if (s.type === "llm") return a + (s.estAlgo30 ?? 0);
      if (s.unitPrice != null) return a + s.calls30 * s.unitPrice;
      return a;
    }, 0),
  );

  // No spending cap -- an account just holds a credit balance (grows on top-up,
  // shrinks on spend). "Total bought" = balance + lifetime spend, and % left is
  // computed against that, so there is no fixed limit.
  const creditsBalance = 250; // mock remaining balance (ALGO) -- real value comes from the account

  // Workflows
  const byWorkflow: WorkflowSpend[] = WF_SEEDS.map((w) => ({
    workflowId: w.workflowId,
    name: w.name,
    status: w.status,
    algo: r6(x402Total * w.share),
    calls: Math.round(w.calls30 * mult),
  })).sort((a, b) => b.algo - a.algo);

  // Settlements (most recent x402 payments -- independent of range)
  const x402Seeds = EP_SEEDS.filter((s) => s.type === "x402");
  // Guard the modulo below: with no x402 seeds, `i % 0` is NaN and the indexed
  // seed is undefined, which throws and takes the whole page down.
  const settlements: Settlement[] =
    x402Seeds.length === 0
      ? []
      : Array.from({ length: SETTLEMENT_ROWS }, (_, i) => {
          const s = x402Seeds[i % x402Seeds.length];
          const tx = fakeTx(i + 1);
          return {
            ts: new Date(Date.now() - i * 7 * 60_000).toISOString(),
            endpoint: s.endpoint,
            amountAlgo: r6(s.unitPrice ?? 0),
            txId: tx,
            explorerURL: `https://lora.algokit.io/testnet/transaction/${tx}`,
            workflowId: WF_SEEDS[i % WF_SEEDS.length].workflowId,
          };
        });

  return {
    summary: {
      totalAlgo: x402Total,
      x402Calls,
      llmTokens,
      llmEstAlgo: llmTotal,
      budget: {
        limit: r6(creditsBalance + lifetimeSpend),
        used: lifetimeSpend,
        resetsAt: "Aug 1",
      },
      deltas: { totalAlgoPct: RANGE_DELTA[range] },
    },
    timeseries: buildTimeseries(range, x402Total, llmTotal, x402Calls),
    byWorkflow,
    byEndpoint: rows,
    settlements,
  };
}
