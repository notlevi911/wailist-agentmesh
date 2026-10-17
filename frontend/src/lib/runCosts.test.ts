import { describe, expect, it } from "vitest";
import {
  costsByNode,
  describeStepCost,
  formatUsdMicros,
  sameRunCosts,
  type RunCosts,
} from "./runCosts";

describe("formatUsdMicros", () => {
  it("uses two decimals from a cent up", () => {
    expect(formatUsdMicros(10_000)).toBe("$0.01");
    expect(formatUsdMicros(90_000)).toBe("$0.09");
    expect(formatUsdMicros(2_490_000)).toBe("$2.49");
  });

  // A half-cent total must round UP in integer space, not down through a
  // binary float: 1_565_000 micros is the common 65_000 relay cost plus the
  // 1_500_000 platform fee, and `micros / 1e6` would print $1.56.
  it("rounds an exact half-cent total up", () => {
    expect(formatUsdMicros(1_565_000)).toBe("$1.57");
    expect(formatUsdMicros(1_500_000)).toBe("$1.50");
    expect(formatUsdMicros(65_000)).toBe("$0.07");
  });

  // #111: a fractional relay cost must not display as $0.00.
  it("keeps sub-cent amounts visible", () => {
    expect(formatUsdMicros(500)).toBe("$0.0005");
    expect(formatUsdMicros(1)).toBe("$0.000001");
    expect(formatUsdMicros(9_999)).toBe("$0.009999");
  });

  it("shows nothing charged as $0.00", () => {
    expect(formatUsdMicros(0)).toBe("$0.00");
  });
});

const costs: RunCosts = {
  totalUsdMicros: 2_490_000,
  steps: [
    {
      nodeId: "agent1",
      totalUsdMicros: 90_000,
      byKind: { platform_key_llm_fee: 90_000 },
    },
    {
      nodeId: "x402",
      totalUsdMicros: 1_900_000,
      byKind: { x402_relay_cost: 400_000, x402_platform_fee: 1_500_000 },
    },
  ],
};

describe("costsByNode", () => {
  it("indexes each charged step by node id", () => {
    const byNode = costsByNode(costs);
    expect(byNode.get("x402")?.totalUsdMicros).toBe(1_900_000);
    expect(byNode.get("agent1")?.totalUsdMicros).toBe(90_000);
    expect(byNode.has("never-charged")).toBe(false);
  });

  it("is empty when costs are unknown (an older backend, or a cached run)", () => {
    expect(costsByNode(null).size).toBe(0);
  });
});

describe("describeStepCost", () => {
  it("lists a step's charges largest first with readable kind names", () => {
    expect(describeStepCost(costs.steps[1])).toBe(
      "platform fee $1.50 · API cost $0.40",
    );
  });

  it("falls back to the raw kind for one it does not know", () => {
    expect(
      describeStepCost({
        nodeId: "n",
        totalUsdMicros: 10_000,
        byKind: { some_new_kind: 10_000 },
      }),
    ).toBe("some_new_kind $0.01");
  });
});

describe("sameRunCosts", () => {
  it("treats an identical poll result as unchanged", () => {
    expect(sameRunCosts(costs, structuredClone(costs))).toBe(true);
  });

  it("sees a new charge on an existing step", () => {
    const next = structuredClone(costs);
    next.steps[0].byKind.byok_flat_fee = 500_000;
    expect(sameRunCosts(costs, next)).toBe(false);
  });

  it("sees a change in the total or the step list", () => {
    expect(sameRunCosts(costs, { ...costs, totalUsdMicros: 1 })).toBe(false);
    expect(sameRunCosts(costs, { ...costs, steps: costs.steps.slice(1) })).toBe(
      false,
    );
    expect(sameRunCosts(null, costs)).toBe(false);
    expect(sameRunCosts(null, null)).toBe(true);
  });
});
