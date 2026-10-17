// What a run was charged, per step and in total, as GET /runs/{runId} returns
// it (#111). Read straight from the backend's debit ledger, so every billable
// step -- an agent's platform-key fee, a connector's flat fee, an x402 call's
// relay cost and platform fee -- is costed the same way, not only the x402
// steps whose output happens to carry a payment receipt.
//
// Only committed charges appear: a balance the engine reserved and later
// released never produces a ledger row, so a figure shown here never goes
// back down.

export interface StepCost {
  nodeId: string;
  totalUsdMicros: number;
  /** The step's total split by debit kind, e.g. x402_relay_cost. */
  byKind: Record<string, number>;
}

export interface RunCosts {
  totalUsdMicros: number;
  /** One entry per charged node, in order of its first charge. */
  steps: StepCost[];
}

const KIND_LABELS: Record<string, string> = {
  platform_key_llm_fee: "LLM fee",
  byok_flat_fee: "flat fee",
  x402_relay_cost: "API cost",
  x402_platform_fee: "platform fee",
  tendril_lease: "Tendril lease",
};

/**
 * Formats USD micros for display. Amounts of a cent or more use two decimals;
 * smaller ones keep their significant digits (up to six), so a fractional relay
 * cost reads "$0.0005" rather than rounding away to "$0.00".
 *
 * All rounding stays in integer micros: dividing by 1e6 first lands on binary
 * floating point, where an exact half-cent total like 1_565_000 micros
 * ($1.565) rounds DOWN to "$1.56" instead of up to "$1.57".
 */
export function formatUsdMicros(micros: number): string {
  if (micros <= 0) return "$0.00";
  if (micros >= 10_000) {
    // 1 cent = 10_000 micros. Round to the nearest cent in integer space,
    // then split that integer into dollars and cents for the format string.
    const cents = Math.round(micros / 10_000);
    return `$${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, "0")}`;
  }
  // Sub-cent: integer micros already encode six decimal places of a dollar
  // (1e6 micros = $1), so pad to six digits and trim trailing zeros instead
  // of round-tripping through a float.
  const frac = String(micros).padStart(6, "0").replace(/0+$/, "");
  return `$0.${frac}`;
}

/** Each charged node's cost, keyed by node id. Empty when costs are unknown. */
export function costsByNode(costs: RunCosts | null): Map<string, StepCost> {
  const out = new Map<string, StepCost>();
  for (const step of costs?.steps ?? []) out.set(step.nodeId, step);
  return out;
}

/**
 * A one-line breakdown of a step's charges for a hover title, largest first:
 * "API cost $0.40 · platform fee $1.50" becomes "platform fee $1.50 · API
 * cost $0.40". Unknown kinds fall back to their raw name.
 */
export function describeStepCost(step: StepCost): string {
  return Object.entries(step.byKind)
    .sort(([, a], [, b]) => b - a)
    .map(
      ([kind, micros]) =>
        `${KIND_LABELS[kind] ?? kind} ${formatUsdMicros(micros)}`,
    )
    .join(" · ");
}

/**
 * Whether two cost snapshots are the same, so a poll that returns unchanged
 * costs can keep the previous object and skip a re-render.
 */
export function sameRunCosts(a: RunCosts | null, b: RunCosts | null): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (
    a.totalUsdMicros !== b.totalUsdMicros ||
    a.steps.length !== b.steps.length
  ) {
    return false;
  }
  return a.steps.every((s, i) => {
    const t = b.steps[i];
    return (
      s.nodeId === t.nodeId &&
      s.totalUsdMicros === t.totalUsdMicros &&
      JSON.stringify(s.byKind) === JSON.stringify(t.byKind)
    );
  });
}
