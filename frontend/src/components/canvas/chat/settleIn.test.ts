import { describe, it, expect } from "vitest";
import {
  settleIn,
  lastUnboundPendingIndex,
  attachRunIn,
  reopenTurnForRunIn,
  serialiseForStorage,
  type ChatMessage,
} from "./useChatSession";

function msg(p: Partial<ChatMessage>): ChatMessage {
  return {
    id: "a-1",
    sender: "assistant",
    text: "",
    ts: "2026-08-07T09:31:02.100Z",
    ...p,
  };
}

describe("settleIn", () => {
  it("settles the turn bound to the given run, not the earliest pending", () => {
    const out = settleIn(
      [
        msg({ id: "a-old", pending: true, runId: "r-1" }),
        msg({ id: "a-new", pending: true, runId: "r-2" }),
      ],
      (m) => m.runId === "r-2",
      { text: "second answer" },
    );
    expect(out[0]).toMatchObject({ id: "a-old", pending: true, text: "" });
    expect(out[1]).toMatchObject({
      id: "a-new",
      pending: false,
      text: "second answer",
    });
  });

  it("settles one exact turn by id", () => {
    const out = settleIn(
      [
        msg({ id: "a-stranded", pending: true }),
        msg({ id: "a-fresh", pending: true }),
      ],
      (m) => m.id === "a-stranded",
      { text: "recovered", interrupted: true },
    );
    expect(out[0]).toMatchObject({ pending: false, interrupted: true });
    expect(out[1].pending).toBe(true);
  });

  it("never settles an already-settled turn", () => {
    const out = settleIn(
      [msg({ id: "a-done", pending: false, runId: "r-1", text: "kept" })],
      (m) => m.runId === "r-1",
      { text: "overwritten" },
    );
    expect(out[0].text).toBe("kept");
  });

  it("returns the same array when nothing matches", () => {
    const input = [msg({ id: "a-1", pending: true, runId: "r-1" })];
    expect(settleIn(input, (m) => m.runId === "r-999", { text: "x" })).toBe(
      input,
    );
  });
});

describe("lastUnboundPendingIndex", () => {
  it("skips a pending turn that is already bound to a run", () => {
    // Without the unbound check, a second run's id would rebind the turn that
    // is already waiting on the first one.
    const idx = lastUnboundPendingIndex([
      msg({ id: "a-1", pending: true, runId: "r-1" }),
      msg({ id: "a-2", pending: true }),
    ]);
    expect(idx).toBe(1);
  });

  it("prefers the most recent unbound turn", () => {
    const idx = lastUnboundPendingIndex([
      msg({ id: "a-old", pending: true }),
      msg({ id: "a-new", pending: true }),
    ]);
    expect(idx).toBe(1);
  });

  it("ignores settled turns", () => {
    const idx = lastUnboundPendingIndex([
      msg({ id: "a-1", pending: false }),
      msg({ id: "a-2", pending: true, runId: "r-9" }),
    ]);
    expect(idx).toBe(-1);
  });
});

describe("attachRunIn", () => {
  it("binds the last unbound pending turn, same as before", () => {
    const out = attachRunIn(
      [msg({ id: "a-1", sender: "user", pending: undefined }), msg({ id: "a-2", pending: true })],
      "r-1",
    );
    expect(out[1]).toMatchObject({ id: "a-2", runId: "r-1" });
  });

  it("seeds a bound assistant turn when nothing is waiting -- a run started outside the chat composer (e.g. the topbar Run button) still lands in the chat rail", () => {
    const out = attachRunIn([], "r-1");
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({
      sender: "assistant",
      pending: true,
      runId: "r-1",
    });
  });

  it("does not duplicate a turn already bound to this run", () => {
    const seeded = attachRunIn([], "r-1");
    const out = attachRunIn(seeded, "r-1");
    expect(out).toBe(seeded);
  });
});

describe("reopenTurnForRunIn", () => {
  it("reopens a settled turn bound to the given run", () => {
    const out = reopenTurnForRunIn(
      [msg({ id: "a-1", pending: false, runId: "r-1", text: "failed" })],
      "r-1",
    );
    expect(out[0]).toMatchObject({ pending: true });
  });

  it("leaves an already-pending turn untouched", () => {
    const input = [msg({ id: "a-1", pending: true, runId: "r-1" })];
    expect(reopenTurnForRunIn(input, "r-1")).toBe(input);
  });

  it("returns the same array when no turn matches the run", () => {
    const input = [msg({ id: "a-1", pending: false, runId: "r-1" })];
    expect(reopenTurnForRunIn(input, "r-999")).toBe(input);
  });
});

describe("serialiseForStorage", () => {
  const big = (n: number) =>
    Array.from({ length: n }, (_, i) =>
      msg({ id: `a-${i}`, text: "x".repeat(20_000) }),
    );

  it("keeps the newest turn when the transcript is oversized", () => {
    // The old code returned without writing at all, so the next hydration
    // served a stale snapshot missing the newest (already billed) turn.
    const messages = [...big(20), msg({ id: "a-newest", text: "the answer" })];
    const parsed = JSON.parse(
      serialiseForStorage({ sessionId: "s1", messages }),
    );
    expect(parsed.messages.at(-1).id).toBe("a-newest");
    expect(parsed.messages.length).toBeLessThan(messages.length);
  });

  it("always produces a writable payload, never an empty result", () => {
    const out = serialiseForStorage({ sessionId: "s1", messages: big(20) });
    expect(out.length).toBeGreaterThan(0);
    expect(JSON.parse(out).sessionId).toBe("s1");
  });

  it("leaves a small transcript untouched", () => {
    const messages = [msg({ id: "a-1", text: "hi" })];
    const parsed = JSON.parse(
      serialiseForStorage({ sessionId: "s1", messages }),
    );
    expect(parsed.messages).toHaveLength(1);
  });
});
