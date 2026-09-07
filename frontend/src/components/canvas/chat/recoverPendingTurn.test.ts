import { describe, it, expect } from "vitest";
import { recoverPendingTurn, toLogEvents } from "./recoverPendingTurn";
import type { RunLogRecord } from "@/lib/api";

function row(partial: Partial<RunLogRecord>): RunLogRecord {
  return {
    id: "rl-1",
    runId: "r-1842",
    stepIndex: 0,
    nodeId: "n2",
    nodeType: "agent",
    status: "success",
    output: null,
    durationMs: 0,
    ts: "2026-08-07T09:31:04.220Z",
    ...partial,
  };
}

describe("toLogEvents", () => {
  it("drops rows the engine had not finished writing", () => {
    const events = toLogEvents([
      row({ stepIndex: 0, status: "success", output: "done" }),
      row({ stepIndex: 1, status: "running" as RunLogRecord["status"] }),
      row({ stepIndex: 2, status: "pending" }),
    ]);
    expect(events).toHaveLength(1);
    expect(events[0].output).toBe("done");
  });

  it("orders by step index regardless of stored order", () => {
    const events = toLogEvents([
      row({ stepIndex: 2, nodeId: "c" }),
      row({ stepIndex: 0, nodeId: "a" }),
      row({ stepIndex: 1, nodeId: "b" }),
    ]);
    expect(events.map((e) => e.nodeId)).toEqual(["a", "b", "c"]);
  });

  it("defaults a missing duration to zero rather than undefined", () => {
    const events = toLogEvents([row({ durationMs: undefined })]);
    expect(events[0].durationMs).toBe(0);
  });
});

describe("recoverPendingTurn", () => {
  it("marks a turn interrupted when it never got a run id", () => {
    const r = recoverPendingTurn(null, []);
    expect(r.kind).toBe("interrupted");
    expect(r.text).toContain("send the message again");
  });

  it("marks a turn interrupted while its run is still going", () => {
    const r = recoverPendingTurn({ status: "running" }, []);
    expect(r.kind).toBe("interrupted");
    expect(r.text).toContain("open the logs");
  });

  it("does not claim success for a run still queued", () => {
    const r = recoverPendingTurn({ status: "pending" }, [
      row({ output: "half-written" }),
    ]);
    expect(r.kind).toBe("interrupted");
  });

  it("recovers the real answer from a finished run", () => {
    const r = recoverPendingTurn({ status: "success" }, [
      row({
        stepIndex: 0,
        nodeId: "n4",
        nodeType: "tool402",
        output: { txId: "aa", settledUsdMicros: 65000 },
      }),
      row({ stepIndex: 1, nodeId: "n2", output: { message: "14.2°C" } }),
    ]);
    expect(r).toMatchObject({
      kind: "resolved",
      text: "14.2°C",
      isError: false,
      toolCount: 1,
    });
    if (r.kind === "resolved") expect(r.spendUSD).toBeCloseTo(0.065, 6);
  });

  it("recovers a failure as a failure, not an answer", () => {
    const r = recoverPendingTurn({ status: "failed" }, [
      row({ status: "failed", output: { error: "provider key not set" } }),
    ]);
    expect(r).toMatchObject({
      kind: "resolved",
      isError: true,
      text: "provider key not set",
    });
  });

  it("treats a stopped run as terminal", () => {
    const r = recoverPendingTurn({ status: "stopped" }, [
      row({ output: "partial" }),
    ]);
    expect(r.kind).toBe("resolved");
  });
});
