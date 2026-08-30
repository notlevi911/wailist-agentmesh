export type NodeType =
  | "trigger"
  | "agent"
  | "provider"
  | "tool"
  | "tool402"
  | "action"
  | "state"
  | "end";
export type EdgeKind = "flow" | "attach";
export type PortName = "in" | "out" | "model" | "tools" | "top";

// CustomParam is one hand-defined input field on an x402 node. A "file" kind
// carries its bytes base64-encoded in `value` and switches the whole outbound
// request to multipart/form-data.
export interface CustomParam {
  name: string;
  kind: "text" | "file";
  value?: string;
  fileName?: string;
  mimeType?: string;
}

export interface WorkflowNode {
  id: string;
  type: NodeType;
  template?: string;
  x: number;
  y: number;
  // display
  name?: string;
  label?: string;
  icon?: string;
  sub?: string;
  custom?: boolean;
  // agent-specific
  systemPrompt?: string;
  wallet?: string;
  balance?: string;
  spent?: string;
  // provider-specific
  apiKey?: string;
  model?: string;
  keyMode?: "byok" | "platform";
  // tool-specific
  url?: string;
  method?: string;
  // tool402-specific
  endpoint?: string;
  description?: string;
  price?: string;
  unit?: string;
  asset?: string;
  provider?: string;
  priceLive?: boolean;
  discoveredParams?: Array<{
    name: string;
    type: string;
    required: boolean;
    description: string;
    default?: string;
  }>;
  paramDefaults?: Record<string, string>;
  // User-defined input fields, for endpoints that publish no input schema of
  // their own (nothing can discover what those need, so the user states it).
  customParams?: CustomParam[];
  // How those fields reach the endpoint: "params" (default) builds the
  // request from the fields themselves, "json" sends bodyTemplate as the
  // body with {{...}} references to them. Fields alone cannot express a
  // nested body, and real endpoints want one.
  bodyMode?: "params" | "json";
  bodyTemplate?: string;
  // state-specific — reads and writes the workflow's persisted variables,
  // which survive between runs. stateValue is the literal to store for
  // "set" (itself subject to {{state.x}} expansion) or the numeric delta
  // for "increment".
  stateOp?: "get" | "set" | "increment" | "delete";
  stateKey?: string;
  stateValue?: string;
  // trigger-specific
  source?: string;
  // email action-specific
  emailTo?: string;
  emailFrom?: string;
  emailSubject?: string;
  emailBody?: string;
  emailApiKey?: string;
  emailProvider?: string;
  // generic per-connector storage — credentials go in secrets (encrypted server-side,
  // "__enc__" sentinel on read), non-secret settings go in config
  secrets?: Record<string, string>;
  config?: Record<string, string>;
}

export interface WorkflowEdge {
  id: string;
  from: string;
  to: string;
  kind: EdgeKind;
  toPort?: PortName;
}

export interface Workflow {
  id: string;
  name: string;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  // "deployed" is what the backend actually stores (models.WorkflowStatusDeployed);
  // it was missing here, so deployment state had to be inferred indirectly.
  status?: "active" | "paused" | "draft" | "deployed";
  updated?: string;
  updatedAt?: string;
  agents?: number;
  runs?: number;
  spend?: string;
  tags?: string[];
}

export interface NodeTypeMeta {
  w: number;
  h: number;
  ports: PortName[];
}

// ── Usage & Credits ─────────────────────────────────────────────────────────
export type UsageRange = "24h" | "7d" | "30d";
export type UsageCategory = "x402" | "llm" | "action";

export interface UsageSummary {
  totalAlgo: number; // x402 spend actually settled on-chain
  x402Calls: number;
  llmTokens: number;
  llmEstAlgo: number | null; // null = backend can't price tokens yet
  budget: { limit: number; used: number; resetsAt: string } | null;
  deltas: { totalAlgoPct: number };
}

export interface UsagePoint {
  ts: string; // pre-formatted bucket label (day / hour)
  x402Algo: number;
  llmAlgo: number;
  calls: number; // x402 calls in this bucket (usage series)
}

export interface WorkflowSpend {
  workflowId: string;
  name: string;
  status?: string;
  algo: number;
  calls: number;
}

export interface EndpointUsage {
  endpoint: string;
  host: string;
  provider: string;
  type: UsageCategory;
  calls: number;
  unitPrice: number | null; // null for token-priced LLM rows
  unit: string;
  totalAlgo: number;
  pctOfSpend: number;
  successRate: number | null;
  lastUsedAt: string; // ISO
}

export interface Settlement {
  ts: string; // ISO
  endpoint: string;
  amountAlgo: number;
  txId: string;
  explorerURL: string;
  workflowId: string;
}

export interface UsagePayload {
  summary: UsageSummary;
  timeseries: UsagePoint[];
  byWorkflow: WorkflowSpend[];
  byEndpoint: EndpointUsage[];
  settlements: Settlement[];
}

export interface PortCoord {
  x: number;
  y: number;
}
