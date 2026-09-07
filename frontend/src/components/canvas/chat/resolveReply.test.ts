import { describe, it, expect } from "vitest";
import { resolveReply } from "./resolveReply";
import type { LogEvent } from "../useRunTranscript";

// Minimal log-event factory: every test only cares about a few fields, and
// spelling out all seven each time buries the case being made.
function log(partial: Partial<LogEvent>): LogEvent {
  return {
    stepIndex: 0,
    nodeId: "n1",
    nodeType: "agent",
    status: "success",
    output: null,
    durationMs: 0,
    ts: "2026-08-07T09:00:00.000Z",
    ...partial,
  };
}

describe("resolveReply", () => {
  it("reads a bare-string agent answer (the BYOK shape)", () => {
    const r = resolveReply([log({ output: "Gold is about $2,640/oz." })]);
    expect(r.text).toBe("Gold is about $2,640/oz.");
    expect(r.isError).toBe(false);
  });

  it("prefers .message on the paid/platform-key agent shape", () => {
    const r = resolveReply([
      log({
        output: {
          message: "Gold is about $2,640/oz.",
          x402Payments: [{ txId: "abc" }],
          platformKeyUsage: { tier: "pro", tokensIn: 10, tokensOut: 20 },
        },
      }),
    ]);
    expect(r.text).toBe("Gold is about $2,640/oz.");
  });

  it("takes the last agent to speak when several ran", () => {
    const r = resolveReply([
      log({ stepIndex: 0, nodeId: "a1", output: "first" }),
      log({ stepIndex: 1, nodeId: "a2", output: "second" }),
    ]);
    expect(r.text).toBe("second");
  });

  it("reports a failure instead of an earlier partial answer", () => {
    const r = resolveReply([
      log({ stepIndex: 0, nodeId: "a1", output: "partial thought" }),
      log({
        stepIndex: 1,
        nodeId: "t1",
        nodeType: "tool402",
        status: "failed",
        output: { error: "endpoint returned 502" },
      }),
    ]);
    expect(r.isError).toBe(true);
    expect(r.text).toBe("endpoint returned 502");
  });

  it("never leaves a failed run without a reason", () => {
    const r = resolveReply([log({ status: "failed", output: null })]);
    expect(r.isError).toBe(true);
    expect(r.text).toBe("The run failed without returning a reason.");
  });

  it("counts only successful tool steps", () => {
    const r = resolveReply([
      log({ stepIndex: 0, nodeId: "t1", nodeType: "tool402" }),
      log({ stepIndex: 1, nodeId: "t2", nodeType: "tool" }),
      log({ stepIndex: 2, nodeId: "t3", nodeType: "tool", status: "stopped" }),
      log({ stepIndex: 3, nodeId: "a1", output: "done" }),
    ]);
    expect(r.toolCount).toBe(2);
  });

  it("sums settled spend across x402 payments, in USD", () => {
    const r = resolveReply([
      log({
        stepIndex: 0,
        nodeId: "t1",
        nodeType: "tool402",
        output: { txId: "aa", settledUsdMicros: 4200 },
      }),
      log({
        stepIndex: 1,
        nodeId: "t2",
        nodeType: "tool402",
        output: { txId: "bb", settledUsdMicros: 1800 },
      }),
      log({ stepIndex: 2, nodeId: "a1", output: "done" }),
    ]);
    expect(r.spendUSD).toBeCloseTo(0.006, 6);
  });

  it("treats a payment with no settled amount as free rather than NaN", () => {
    const r = resolveReply([
      log({ nodeId: "t1", nodeType: "tool402", output: { txId: "aa" } }),
    ]);
    expect(r.spendUSD).toBe(0);
  });

  it("falls back to the last successful step when no agent ran", () => {
    const r = resolveReply([
      log({ nodeId: "x1", nodeType: "action", output: "posted to slack" }),
    ]);
    expect(r.text).toBe("posted to slack");
    expect(r.isError).toBe(false);
  });

  it("says something rather than rendering an empty bubble", () => {
    const r = resolveReply([log({ output: "" })]);
    expect(r.text).toBe("The run finished.");
  });

  it("handles an empty transcript", () => {
    const r = resolveReply([]);
    expect(r.text).toBe("The run finished.");
    expect(r.toolCount).toBe(0);
    expect(r.spendUSD).toBe(0);
  });

  it("serialises a structured answer that has no text-like key", () => {
    const r = resolveReply([log({ output: { rows: [1, 2] } })]);
    expect(r.text).toContain("rows");
  });
  it("counts a settled payment that carries no tx id", () => {
    // paymentReceipt omits txId when the settlement returned none, but the
    // credits were still debited -- these runs used to display no cost at all.
    const r = resolveReply([
      log({
        nodeId: "t1",
        nodeType: "tool402",
        output: { settledUsdMicros: 65000, nodeName: "x402 Weather" },
      }),
    ]);
    expect(r.spendUSD).toBeCloseTo(0.065, 6);
  });

  it("adds the platform fee to the settled amount", () => {
    const r = resolveReply([
      log({
        nodeId: "t1",
        nodeType: "tool402",
        output: {
          txId: "aa",
          settledUsdMicros: 65000,
          platformFeeUsdMicros: 5000,
        },
      }),
    ]);
    expect(r.spendUSD).toBeCloseTo(0.07, 6);
  });

  it("counts a run-funded agent's per-call receipts as real tool calls, but not the funding row", () => {
    // A run-level pre-fund publishes one funding row plus one receipt per
    // real tool call, all sharing the funding tx id (runner.go's
    // prependRunFundingReceipt). Two real calls should read as 2, not 3.
    const r = resolveReply([
      log({
        stepIndex: 0,
        nodeId: "a1",
        nodeType: "tool402",
        output: {
          txId: "FUNDTX",
          settledUsdMicros: 50000,
          isFundingReceipt: true,
        },
      }),
      log({
        stepIndex: 1,
        nodeId: "t1",
        nodeType: "tool402",
        output: { txId: "FUNDTX", settledUsdMicros: 20000 },
      }),
      log({
        stepIndex: 2,
        nodeId: "t2",
        nodeType: "tool402",
        output: { txId: "FUNDTX", settledUsdMicros: 15000 },
      }),
    ]);
    expect(r.toolCount).toBe(2);
  });

  it("dedupes a run-funded agent's spend by tx id, keeping the full funded total", () => {
    const r = resolveReply([
      log({
        stepIndex: 0,
        nodeId: "a1",
        nodeType: "tool402",
        output: {
          txId: "FUNDTX",
          settledUsdMicros: 50000,
          isFundingReceipt: true,
        },
      }),
      log({
        stepIndex: 1,
        nodeId: "t1",
        nodeType: "tool402",
        output: { txId: "FUNDTX", settledUsdMicros: 20000 },
      }),
      log({
        stepIndex: 2,
        nodeId: "t2",
        nodeType: "tool402",
        output: { txId: "FUNDTX", settledUsdMicros: 15000 },
      }),
    ]);
    // Not 50000+20000+15000 -- the per-call rows are slices of the same
    // funding total, already counted once via the first (funding) row.
    expect(r.spendUSD).toBeCloseTo(0.05, 6);
  });

  it("sums two settlements with no tx id independently, not as duplicates of each other", () => {
    const r = resolveReply([
      log({
        stepIndex: 0,
        nodeId: "t1",
        nodeType: "tool402",
        output: { settledUsdMicros: 20000 },
      }),
      log({
        stepIndex: 1,
        nodeId: "t2",
        nodeType: "tool402",
        output: { settledUsdMicros: 15000 },
      }),
    ]);
    expect(r.spendUSD).toBeCloseTo(0.035, 6);
  });
});
