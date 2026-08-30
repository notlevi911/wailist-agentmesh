import {
  NodeTypeMeta,
  Workflow,
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
  tool: { w: 200, h: 64, ports: ["top"] },
  tool402: { w: 220, h: 84, ports: ["top"] },
  action: { w: 200, h: 64, ports: ["in", "out"] },
  state: { w: 200, h: 64, ports: ["in", "out"] },
  end: { w: 200, h: 60, ports: ["in"] },
};

export const TRIGGER_TEMPLATES = [
  { id: "manual", name: "Manual Trigger", desc: "Click to test", icon: "▶" },
  { id: "chat", name: "On Chat Message", desc: "Inbound chat", icon: "◴" },
  { id: "webhook", name: "Webhook", desc: "HTTP POST endpoint", icon: "◷" },
  { id: "cron", name: "Schedule", desc: "Cron / interval", icon: "◵" },
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

// Display-only mirror of backend/internal/engine/nodes/tier.go's modelTiers
// map — the backend is the billing-authoritative source; this only drives
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

export const TIER_FEES: Record<"economy" | "standard" | "frontier", number> = {
  economy: 0.01,
  standard: 0.03,
  frontier: 0.05,
};

// modelTier mirrors nodes.ModelTier's default: unrecognized template/model
// pairs are "standard", never "economy".
export function modelTier(
  template: string,
  model: string,
): "economy" | "standard" | "frontier" {
  return MODEL_TIERS[template]?.[model] ?? "standard";
}

export const TOOL_TEMPLATES = [
  { id: "http", name: "HTTP Request", desc: "GET/POST any URL", icon: "⟶" },
  { id: "code", name: "Code", desc: "Run JS/Python inline", icon: "{}" },
  { id: "calc", name: "Calculator", desc: "Math expressions", icon: "Σ" },
  {
    id: "vector",
    name: "Vector Store",
    desc: "Pinecone / pgvector",
    icon: "⊕",
  },
  {
    id: "memory",
    name: "Conversation Memory",
    desc: "Recent turns",
    icon: "◐",
  },
];

export const TOOL402_TEMPLATES = [
  {
    id: "tavily",
    name: "Tavily Search",
    provider: "tavily.x402",
    price: "0.002",
    unit: "call",
    icon: "⌕",
  },
  {
    id: "firecrawl",
    name: "Firecrawl Scrape",
    provider: "firecrawl.x402",
    price: "0.005",
    unit: "page",
    icon: "◐",
  },
  {
    id: "alpaca",
    name: "AlpacaQuote",
    provider: "alpaca.x402",
    price: "0.001",
    unit: "quote",
    icon: "$",
  },
  {
    id: "ocr",
    name: "OCR.space",
    provider: "ocr.x402",
    price: "0.003",
    unit: "page",
    icon: "⊟",
  },
  {
    id: "flux",
    name: "FluxImage",
    provider: "flux.x402",
    price: "0.020",
    unit: "image",
    icon: "✦",
  },
  {
    id: "weather",
    name: "WeatherKit",
    provider: "weatherkit.x402",
    price: "0.0008",
    unit: "call",
    icon: "◌",
  },
];

export const ACTION_TEMPLATES = [
  { id: "email", name: "Send Email", desc: "Postmark / Resend", icon: "✉" },
  { id: "slack", name: "Slack Message", desc: "Post to channel", icon: "#" },
  { id: "db", name: "Database Insert", desc: "Postgres / Neon", icon: "▤" },
  { id: "discord", name: "Discord Message", desc: "Webhook post", icon: "d" },
  { id: "teams", name: "Teams Message", desc: "Webhook post", icon: "T" },
  {
    id: "google_chat",
    name: "Google Chat Message",
    desc: "Webhook post",
    icon: "G",
  },
  { id: "ntfy", name: "Ntfy Push", desc: "Topic notification", icon: "n" },
  { id: "telegram", name: "Telegram Message", desc: "Bot API send", icon: "t" },
  { id: "github", name: "GitHub Issue", desc: "Create an issue", icon: "gh" },
  { id: "notion", name: "Notion Block", desc: "Append to a page", icon: "N" },
  {
    id: "airtable",
    name: "Airtable Record",
    desc: "Create a record",
    icon: "A",
  },
  { id: "hubspot", name: "HubSpot Note", desc: "Log a CRM note", icon: "hs" },
  { id: "trello", name: "Trello Card", desc: "Create a card", icon: "tr" },
  { id: "asana", name: "Asana Task", desc: "Create a task", icon: "as" },
  { id: "clickup", name: "ClickUp Task", desc: "Create a task", icon: "cu" },
  { id: "jira", name: "Jira Issue", desc: "Create an issue", icon: "J" },
  {
    id: "mailchimp",
    name: "Mailchimp Subscriber",
    desc: "Add to a list",
    icon: "mc",
  },
  { id: "linear", name: "Linear Issue", desc: "Create an issue", icon: "L" },
  { id: "todoist", name: "Todoist Task", desc: "Create a task", icon: "td" },
  { id: "gitlab", name: "GitLab Issue", desc: "Create an issue", icon: "gl" },
  { id: "sentry", name: "Sentry Event", desc: "Capture a message", icon: "S" },
  { id: "supabase", name: "Supabase Insert", desc: "Insert a row", icon: "sb" },
  {
    id: "woocommerce",
    name: "WooCommerce Note",
    desc: "Add an order note",
    icon: "wc",
  },
  {
    id: "elevenlabs",
    name: "ElevenLabs Speech",
    desc: "Text to speech",
    icon: "11",
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
        "Real-time weather data — temperature, wind, conditions for any city worldwide. Accepts: city (string, required), units (celsius|fahrenheit, optional).",
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

export const WORKFLOWS: Workflow[] = [
  {
    id: "wf-triage",
    name: "Customer Support Triage",
    status: "active",
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
    status: "active",
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
    status: "active",
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
    endpoint: "Tavily Search",
    host: "api.tavily.x402/search",
    provider: "tavily.x402",
    type: "x402",
    unitPrice: 0.002,
    unit: "call",
    calls30: 3820,
    success: 99.8,
    lastUsedMin: 8,
  },
  {
    endpoint: "Firecrawl Scrape",
    host: "api.firecrawl.x402/scrape",
    provider: "firecrawl.x402",
    type: "x402",
    unitPrice: 0.005,
    unit: "page",
    calls30: 940,
    success: 97.4,
    lastUsedMin: 26,
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
    status: "active",
    share: 0.34,
    calls30: 4200,
  },
  {
    workflowId: "wf-onchain",
    name: "On-chain Compliance Watch",
    status: "active",
    share: 0.24,
    calls30: 3100,
  },
  {
    workflowId: "wf-brief",
    name: "Daily Market Brief",
    status: "active",
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

  // Credit balance is account-level — it must NOT change with the selected chart
  // range. Compute lifetime spend at full scale (no range multiplier) so
  // "credits left" reads the same across 24h / 7d / 30d.
  const lifetimeSpend = r6(
    EP_SEEDS.reduce((a, s) => {
      if (s.type === "llm") return a + (s.estAlgo30 ?? 0);
      if (s.unitPrice != null) return a + s.calls30 * s.unitPrice;
      return a;
    }, 0),
  );

  // No spending cap — an account just holds a credit balance (grows on top-up,
  // shrinks on spend). "Total bought" = balance + lifetime spend, and % left is
  // computed against that, so there is no fixed limit.
  const creditsBalance = 250; // mock remaining balance (ALGO) — real value comes from the account

  // Workflows
  const byWorkflow: WorkflowSpend[] = WF_SEEDS.map((w) => ({
    workflowId: w.workflowId,
    name: w.name,
    status: w.status,
    algo: r6(x402Total * w.share),
    calls: Math.round(w.calls30 * mult),
  })).sort((a, b) => b.algo - a.algo);

  // Settlements (most recent x402 payments — independent of range)
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
