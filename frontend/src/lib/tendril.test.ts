import { describe, expect, it } from "vitest";
import { estimateLeaseCostUSD, estimateLeaseHoursCostUSD } from "./tendril";

describe("estimateLeaseCostUSD", () => {
  // The all-in total across BOTH ledgers: hourly rate x hours (Tendril
  // credit) plus the flat $0.01 gate fee (AgentMesh credit, billed
  // separately by the relay -- see the backend's tendrilRentGateFeeAtomic
  // doc comment). Display-only; not what any affordability check should
  // compare against a Tendril credit balance -- see estimateLeaseHoursCostUSD.
  it("adds the flat rent gate fee to the metered hours", () => {
    expect(estimateLeaseCostUSD(6, 2)).toBeCloseTo(12.01, 6);
    expect(estimateLeaseCostUSD(6, 1)).toBeCloseTo(6.01, 6);
    expect(estimateLeaseCostUSD(1.5, 0.5)).toBeCloseTo(0.76, 6);
  });

  it("returns the bare gate fee for zero hours rather than NaN", () => {
    expect(estimateLeaseCostUSD(6, 0)).toBeCloseTo(0.01, 6);
  });
});

describe("estimateLeaseHoursCostUSD", () => {
  // Mirrors the backend's RequiredCreditAtomic exactly: hourly rate x
  // hours, no gate fee folded in. This is what any "can the user afford
  // this" check against their Tendril credit balance must use.
  it("is the metered hours cost with no gate fee", () => {
    expect(estimateLeaseHoursCostUSD(6, 2)).toBeCloseTo(12, 6);
    expect(estimateLeaseHoursCostUSD(6, 1)).toBeCloseTo(6, 6);
    expect(estimateLeaseHoursCostUSD(1.5, 0.5)).toBeCloseTo(0.75, 6);
  });

  it("is zero for zero hours", () => {
    expect(estimateLeaseHoursCostUSD(6, 0)).toBe(0);
  });
});
