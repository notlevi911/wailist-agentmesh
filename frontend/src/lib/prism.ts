import { BASE, apiFetch } from "@/lib/api";
import { assertWritable } from "@/lib/readonly";

// PRISM's x402 endpoints, as served by GET /prism/endpoints. The backend's
// internal/prism package is the single source of truth for all of this — the
// prices here are the same values the Bazaar card quotes and the console
// charges, which is why none of it is hardcoded on this side.

export type PrismFieldKind = "text" | "textarea" | "file";

export interface PrismField {
  name: string;
  label: string;
  kind: PrismFieldKind;
  required: boolean;
  placeholder?: string;
  description?: string;
  /** File picker `accept` attribute; file fields only. */
  accept?: string;
}

export interface PrismTask {
  key: string;
  title: string;
  description: string;
}

export interface PrismEndpoint {
  id: string;
  task: string;
  tier: string;
  title: string;
  description: string;
  path: string;
  method: string;
  /** The vendor's price in atomic USDC. NOT the total — see totalCostMicros. */
  amountMicros: number;
  fields: PrismField[];
  /**
   * How this endpoint's request shape is known:
   * - "live"       a real paid call to this exact endpoint succeeded
   * - "documented" the shape is given in PRISM's own spec
   * - "sibling"    identical request template to a live/documented sibling,
   *                differing only in path and model tier
   *
   * Only "sibling" gets a note in the console, and a quiet one: sharing a
   * template byte-for-byte with a call that has demonstrably settled is very
   * different from a guess.
   */
  verified: "live" | "documented" | "sibling";
}

export interface PrismSpec {
  provider: string;
  host: string;
  asset: string;
  /** AgentMesh's flat markup per x402 call, applied to every endpoint. */
  platformFeeUsdMicros: number;
  tasks: PrismTask[];
  endpoints: PrismEndpoint[];
  maxFileBytes: number;
}

export interface PrismRunField {
  kind: "text" | "file";
  value: string;
  fileName?: string;
  mimeType?: string;
}

export interface PrismRunResult {
  endpoint: string;
  response: unknown;
  /**
   * False when the call completed without any payment settling. That is not a
   * cheaper success: it means the endpoint did not answer with a payment
   * challenge, so nothing was paid and nothing was billed. The console says so
   * rather than presenting the response as a paid result.
   */
  settled: boolean;
  settledUsdMicros: number;
  platformFeeUsdMicros: number;
  totalUsdMicros: number;
  txId?: string;
  explorerURL?: string;
  platformFeeTxId?: string;
  platformFeeExplorerURL?: string;
}

// What a call really costs the user: the vendor's price plus AgentMesh's flat
// per-call markup. For PRISM the markup is the larger half of every total, so
// showing the vendor price alone would understate a run by roughly 7x.
export function totalCostMicros(
  endpoint: Pick<PrismEndpoint, "amountMicros">,
  platformFeeUsdMicros: number,
): number {
  return endpoint.amountMicros + platformFeeUsdMicros;
}

// formatUsd renders atomic USDC (6 decimals) as a plain dollar figure. Two
// decimals: every amount in play here is a round cent, and more places only
// add noise to a price tag.
export function formatUsd(micros: number): string {
  return `$${(micros / 1e6).toFixed(2)}`;
}

// PrismRunError carries the HTTP status alongside the message so the console
// can tell a blocked balance (402, backend's ErrBalanceBlocked) from a gateway
// failure and offer a top-up instead of a bare error line. A 402 here means
// the request was rejected BEFORE any payment, so nothing was charged.
export class PrismRunError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "PrismRunError";
    this.status = status;
  }
}

export const prism = {
  // Finds-or-creates the ONE hidden workflow row that backs this user's Prism
  // console, so every entry point (the Bazaar card, the workflow list) opens
  // the same row rather than minting a duplicate each time.
  async console(): Promise<string> {
    assertWritable("GET", "/prism/console");
    const res = await apiFetch(`${BASE}/prism/console`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`console: ${res.status}`);
    const body = (await res.json()) as { workflowId: string };
    return body.workflowId;
  },

  // Read-only counterpart: reports the console workflow's id WITHOUT creating
  // one. WorkflowRoute calls this on every workflow-page visit, where the
  // creating variant would mint a hidden Prism row for every user the instant
  // they opened any of their own, unrelated workflows.
  async consoleWorkflowIdIfExists(): Promise<string | null> {
    const res = await apiFetch(`${BASE}/prism/console/exists`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`console/exists: ${res.status}`);
    const body = (await res.json()) as { exists: boolean; workflowId?: string };
    return body.exists && body.workflowId ? body.workflowId : null;
  },

  async spec(): Promise<PrismSpec> {
    const res = await apiFetch(`${BASE}/prism/endpoints`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`endpoints: ${res.status}`);
    return (await res.json()) as PrismSpec;
  },

  async run(
    endpoint: string,
    fields: Record<string, PrismRunField>,
  ): Promise<PrismRunResult> {
    const res = await apiFetch(`${BASE}/prism/run`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ endpoint, fields }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new PrismRunError(data.error ?? `run: ${res.status}`, res.status);
    }
    return data as PrismRunResult;
  },
};
