export type NodeType =
  | "trigger"
  | "agent"
  | "provider"
  | "tool"
  | "tool402"
  | "action"
  | "state"
  | "end"
  | "tendril"
  | "google";
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
  // generic per-connector storage -- credentials go in secrets (encrypted server-side,
  // "__enc__" sentinel on read), non-secret settings go in config
  secrets?: Record<string, string>;
  config?: Record<string, string>;
  // tendril-specific
  tendrilAction?: "topup" | "rent" | "run" | "release" | "auto";
  tendrilNodeId?: string;
  tendrilHours?: string;
  // USD of AgentMesh credit to convert into Tendril credit, on a topup node.
  // On an "auto" node, this doubles as its rent budget (default $1 if unset).
  tendrilAmount?: string;
  // On a topup node: skip (no charge) when Tendril credit is already at or
  // above this many USD. Empty = always top up.
  tendrilMinBalance?: string;
  // On a topup node: buy only what Tendril credit is short of renting the
  // cheapest online machine for this many hours, at least tendrilAmount.
  // Overrides tendrilMinBalance.
  tendrilCoverHours?: string;
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
  // "error" is stored too (models.WorkflowStatusError) and was missing as well.
  // "paused" and the legacy "active" only ever come from mock data.
  status?: "active" | "paused" | "draft" | "deployed" | "error";
  updated?: string;
  updatedAt?: string;
  createdAt?: string;
  // The newest run within the same 30 days `runs` counts. Absent when
  // nothing ran in that window.
  lastRunAt?: string;
  // What the workflow does, in a sentence or two. Absent until written.
  description?: string;
  // Every run the workflow has had. Only GET /workflows/{id} sends it.
  totalRuns?: number;
  agents?: number;
  runs?: number;
  spend?: string;
  // The backend could not aggregate runs/spend/lastRunAt for this list, so
  // their absence means "not known", not "none". `runs` is omitted at zero
  // and `spend` is omitted when nothing settled, so without this flag an
  // outage is indistinguishable from a workflow that has simply never run.
  statsUnavailable?: boolean;
  tags?: string[];
  scheduleCron?: string;
  scheduleNextRunAt?: string;
  // The geofence trigger. All three are present together or none is -- a
  // partly configured zone means nothing, so the backend writes and clears
  // them as a unit (models.Workflow, migration 000029).
  geofenceLat?: number;
  geofenceLng?: number;
  geofenceRadiusM?: number;
  // Tri-state on the wire: absent means no fix has been recorded yet, which
  // is deliberately NOT the same as being outside. The first fix after a zone
  // is saved only establishes a baseline; it never counts as a crossing.
  geofenceInside?: boolean;
  geofenceLastFixAt?: string;
}

// ── Run history ─────────────────────────────────────────────────────────────
// A run's lifecycle as the backend stores it (models.RunStatus).
export type RunStatus = "running" | "success" | "failed" | "stopped";

// One row of a run history list (GET /workflows/{id}/runs and GET /runs).
// Mirrors models.RunSummary; the run's input context is never included.
export interface RunSummary {
  id: string;
  workflowId: string;
  workflowName: string;
  // "manual", "schedule", "geofence", "webhook", or a partner console.
  triggeredBy: string;
  status: RunStatus;
  startedAt: string;
  finishedAt?: string;
  // Everything charged for the run so far, in USD micros. A running run can
  // still grow.
  spendUsdMicros: number;
}

// One page of run history. nextCursor is null on the last page.
export interface RunPage {
  runs: RunSummary[];
  nextCursor: string | null;
}

// One run the scheduler will start (GET /schedules/upcoming).
export interface UpcomingRun {
  workflowId: string;
  workflowName: string;
  at: string;
  cron: string;
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

export interface CostEstimateLine {
  nodeId: string;
  label: string;
  lowUsdMicros: number;
  highUsdMicros: number;
  note?: string;
}

// Static low/high band for one workflow run, from GET /workflows/:id/estimate.
export interface CostEstimate {
  lowUsdMicros: number;
  highUsdMicros: number;
  lines: CostEstimateLine[];
  hasUnpricedX402: boolean;
}

export interface PortCoord {
  x: number;
  y: number;
}
