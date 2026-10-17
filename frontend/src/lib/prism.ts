import { BASE, apiFetch } from "@/lib/api";
import { paymentRefusalCode, type PaymentRefusalCode } from "./paymentRefusal";

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

export class PrismRunError extends Error {
  readonly status: number;
  readonly code: PaymentRefusalCode | null;
  constructor(message: string, status: number, code?: unknown) {
    super(message);
    this.name = "PrismRunError";
    this.status = status;
    this.code = paymentRefusalCode(status, code);
  }
}

export interface PrismRepoFile {
  path: string;
  size: number;
  /** Empty when reviewable; otherwise a short reason the file was left out. */
  skip?: string;
}

export interface PrismRepoListing {
  owner: string;
  name: string;
  ref: string;
  files: PrismRepoFile[];
  reviewableCount: number;
  maxFiles: number;
  /** The flat markup, charged ONCE for the whole repo run — not per file. */
  platformFeeTotal: number;
}

export interface PrismRepoFileResult {
  path: string;
  response?: unknown;
  error?: string;
  costUsdMicros: number;
  txId?: string;
}

export interface PrismRepoReviewResult {
  repo: string;
  ref: string;
  tier: string;
  results: PrismRepoFileResult[];
  vendorTotalUsdMicros: number;
  platformFeeUsdMicros: number;
  totalUsdMicros: number;
}

// repoRunCost is what a repo review actually costs: the per-file vendor price
// times the number of files, plus ONE platform fee for the run.
//
// This is the whole reason the batch exists. Billing the flat fee per call — as
// every other x402 path does — made a 30-file review $48, of which $45 was
// markup on $3 of review.
export function repoRunCost(
  fileCount: number,
  perFileMicros: number,
  platformFeeMicros: number,
): number {
  if (fileCount <= 0) return 0;
  return fileCount * perFileMicros + platformFeeMicros;
}

export const prism = {
  async spec(): Promise<PrismSpec> {
    const res = await apiFetch(`${BASE}/prism/endpoints`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`endpoints: ${res.status}`);
    return (await res.json()) as PrismSpec;
  },

  async repoFiles(repo: string): Promise<PrismRepoListing> {
    const res = await apiFetch(`${BASE}/prism/repo/files`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repo }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? "Could not read that repository.");
    return data as PrismRepoListing;
  },

  async repoReview(
    repo: string,
    ref: string,
    tier: string,
    paths: string[],
  ): Promise<PrismRepoReviewResult> {
    const res = await apiFetch(`${BASE}/prism/repo/review`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repo, ref, tier, paths }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new PrismRunError(data.error ?? "The review failed.", res.status, data.code);
    }
    return data as PrismRepoReviewResult;
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
      throw new PrismRunError(data.error ?? `run: ${res.status}`, res.status, data.code);
    }
    return data as PrismRunResult;
  },
};
