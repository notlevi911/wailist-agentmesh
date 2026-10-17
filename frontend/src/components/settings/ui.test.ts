import { describe, expect, it } from "vitest";
import { parseUsdInput, usdInput } from "./ui";

describe("editable USD amounts", () => {
  it.each([0, 1, 50_001, 1_234_567, 5_000_000, Number.MAX_SAFE_INTEGER])("round trips %s micros without cent rounding", (micros) => {
    expect(parseUsdInput(usdInput(micros))).toBe(micros);
  });
  it.each(["", "-1", "1e2", "Infinity", "NaN", "1,000", "1.0000001", "9007199254.740992", "1.2.3"])("rejects %s", (text) => {
    expect(parseUsdInput(text)).toBeNull();
  });
  it("accepts explicit sub-cent values", () => {
    expect(parseUsdInput(" 1.234567 ")).toBe(1_234_567);
  });
});

