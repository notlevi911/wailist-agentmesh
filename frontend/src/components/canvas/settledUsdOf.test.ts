import { describe, it, expect } from "vitest";
import { settledUsdOf } from "./useRunTranscript";

// This helper is the single source of truth shared by the chat activity strip
// and the usage page's settlement rows. The two used to compute spend
// separately and disagreed, so the shared contract is asserted directly.
describe("settledUsdOf", () => {
  it("counts a settlement that returned no tx id", () => {
    expect(settledUsdOf({ settledUsdMicros: 65000 })).toBeCloseTo(0.065, 6);
  });

  it("adds the platform fee to the settled amount", () => {
    expect(
      settledUsdOf({ settledUsdMicros: 65000, platformFeeUsdMicros: 5000 }),
    ).toBeCloseTo(0.07, 6);
  });

  it("returns null when the step paid for nothing", () => {
    expect(settledUsdOf({ message: "just an answer" })).toBeNull();
    expect(settledUsdOf("a bare string")).toBeNull();
    expect(settledUsdOf(null)).toBeNull();
  });

  it("does not treat a tx id alone as a charge", () => {
    // A receipt with an id but no settled amount is not money moved.
    expect(settledUsdOf({ txId: "aa" })).toBeNull();
  });

  it("distinguishes a genuine zero settlement from no settlement", () => {
    expect(settledUsdOf({ settledUsdMicros: 0 })).toBe(0);
  });
});
