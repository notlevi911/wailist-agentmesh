import { BASE, apiFetch } from "@/lib/api";
import { assertWritable } from "@/lib/readonly";

// Tendril charges a flat 0.01 USDC to open a lease; the hours themselves meter
// against credit at the machine's hourly rate. Kept in sync with the backend's
// tendrilRentGateFeeAtomic.
export const TENDRIL_RENT_GATE_FEE_USD = 0.01;

export interface TendrilMachine {
  id: string;
  label: string;
  cpuCores: number;
  ramMb: number;
  gpu: string | null;
  pricePerHourUsd: number;
}

// Total real-world cost of a rental across BOTH ledgers: the hourly rate is
// reserved from the user's Tendril credit, while the flat gate fee is a
// separate real x402 payment billed directly in AgentMesh credit (see the
// backend's tendrilRentGateFeeAtomic doc comment for why the two are no
// longer folded into one reservation). Display-only, for showing the
// all-in price tag next to a machine -- do NOT compare this against the
// Tendril credit balance to decide affordability, since part of it isn't
// drawn from that balance at all; use estimateLeaseHoursCostUSD for that.
export function estimateLeaseCostUSD(
  pricePerHourUsd: number,
  hours: number,
): number {
  return pricePerHourUsd * hours + TENDRIL_RENT_GATE_FEE_USD;
}

// What a rental actually reserves from the user's TENDRIL credit balance --
// the metered hours only, matching the backend's RequiredCreditAtomic
// exactly (no gate fee folded in, see that function's doc comment). Use
// this, not estimateLeaseCostUSD, for any "can this user afford to rent"
// check against their Tendril credit balance.
export function estimateLeaseHoursCostUSD(
  pricePerHourUsd: number,
  hours: number,
): number {
  return pricePerHourUsd * hours;
}

export interface TendrilLeaseSummary {
  id: string;
  tendrilNodeId: string;
  tendrilNodeLabel: string;
  sshHost: string;
  sshPort: number;
  sshUsername: string;
  sshCommand: string;
  rateUsdMicrosPerHour: number;
  status: string;
  startedAt: string;
  // fundedUntil is NOT the renter's own reservation window — it's how long
  // Tendril's shared pool wallet (every AgentMesh user's topups combined)
  // could fund this machine at its rate, which is almost always far more
  // than any one user asked for (confirmed live: a 1-hour rent showed a
  // ~2-hour fundedUntil). Use hoursPurchased for anything shown to the user
  // as "your window" — fundedUntil is a platform-side implementation detail.
  fundedUntil: string;
  hoursPurchased: number;
  // What renting this lease reserved from the user's Tendril credit — the
  // requested hours at this machine's rate ONLY. The flat gate fee is a
  // separate real payment billed in AgentMesh credit, not part of this
  // reservation (see estimateLeaseCostUSD's doc comment). Releasing early
  // refunds whatever of this Tendril reservation never actually billed.
  reservedUsdMicros: number;
}

// What releasing a lease actually settled: real backend fields (usedSeconds,
// charged, refunded), never the shared pool balance — see leases.go's
// ReleaseLease handler doc comment for why that stays server-side only.
export interface TendrilReleaseResult {
  usedSeconds: number;
  charged: number;
  refunded: number;
}

// What a topup actually settled — txId/explorerURL is the real on-chain leg
// (Wallet 1 -> Wallet 2); outboundTxId/outboundExplorerURL is the second
// leg (Wallet 2 -> Tendril's payTo) when the relay's response carried one.
// executeTendrilTopup (backend) only sets whichever of these it actually got
// back, so all four are optional.
export interface TendrilTopupResult {
  toppedUp: string;
  tendrilCreditBalance: string;
  previousBalance: string;
  txId?: string;
  explorerURL?: string;
  outboundTxId?: string;
  outboundExplorerURL?: string;
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await apiFetch(`${BASE}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? `${path}: ${res.status}`);
  return data as T;
}

export const tendril = {
  // Finds-or-creates the ONE hidden workflow row that backs this user's
  // console (backend: GetOrCreateSystemWorkflow, keyed on a fixed name) so
  // every "Load Tendril workflow" click opens the same row instead of
  // minting a fresh duplicate one every time -- workflowsApi.create would
  // do the latter, since it always inserts.
  async console(): Promise<string> {
    assertWritable("GET", "/tendril/console");
    const res = await apiFetch(`${BASE}/tendril/console`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`console: ${res.status}`);
    const body = (await res.json()) as { workflowId: string };
    return body.workflowId;
  },

  // Read-only counterpart to console() above: reports the console
  // workflow's id WITHOUT creating one if it doesn't exist yet. Use this
  // (not console()) for "is this workflow id the console" checks --
  // WorkflowRoute calls it once per workflow-page visit, and console()'s
  // create-on-first-call behavior would otherwise mint a hidden console
  // row for every user the instant they open any of their own, entirely
  // unrelated workflows.
  async consoleWorkflowIdIfExists(): Promise<string | null> {
    const res = await apiFetch(`${BASE}/tendril/console/exists`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`console/exists: ${res.status}`);
    const body = (await res.json()) as {
      exists: boolean;
      workflowId?: string;
    };
    return body.exists && body.workflowId ? body.workflowId : null;
  },

  async machines(): Promise<TendrilMachine[]> {
    const res = await apiFetch(`${BASE}/tendril/machines`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`machines: ${res.status}`);
    const body = (await res.json()) as { machines: TendrilMachine[] };
    return body.machines ?? [];
  },

  async credit(): Promise<number> {
    const res = await apiFetch(`${BASE}/tendril/credits`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`credits: ${res.status}`);
    const body = (await res.json()) as { tendrilCreditUsdMicros: number };
    return body.tendrilCreditUsdMicros / 1e6;
  },

  async leases(): Promise<TendrilLeaseSummary[]> {
    const res = await apiFetch(`${BASE}/leases`, { credentials: "include" });
    if (!res.ok) throw new Error(`leases: ${res.status}`);
    const body = (await res.json()) as { leases: TendrilLeaseSummary[] };
    return body.leases ?? [];
  },

  // Direct actions — each is its own call, with no chaining and no
  // "re-buy credit to run a job" side effect: buying credit, renting, and
  // running are three independent button presses, not steps of one pipeline.
  topup(amountUsd: string): Promise<TendrilTopupResult> {
    return postJSON("/tendril/topup", { amountUsd });
  },
  rent(nodeId: string, hours: string): Promise<Record<string, unknown>> {
    return postJSON("/tendril/rent", { nodeId, hours });
  },
  run(payload: string): Promise<Record<string, unknown>> {
    return postJSON("/tendril/run", { payload });
  },
  release(leaseId: string): Promise<TendrilReleaseResult> {
    return postJSON(`/leases/${leaseId}/release`, {});
  },
};
