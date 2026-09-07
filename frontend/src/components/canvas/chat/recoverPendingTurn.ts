import type { RunLogRecord } from "@/lib/api";
import type { LogEvent } from "../useRunTranscript";
import { resolveReply } from "./resolveReply";

// Decides what to do with a chat turn that is still `pending` when the console
// mounts with no live run.
//
// This happens on a plain page reload mid-run: CanvasPage holds `runId` in
// useState, so it is back to null after a refresh, while useChatSession has
// already persisted the pending turn to localStorage. Nothing in the normal
// flow would ever settle that turn -- and because a stale pending sits earlier
// in the transcript than any new one, it would also swallow the *next* turn's
// answer. So a stranded turn is not cosmetic; it corrupts every turn after it.
//
// Pure so the decision can be unit-tested without a browser or a live run.

export type PendingRecovery =
  | {
      kind: "resolved";
      text: string;
      isError: boolean;
      toolCount: number;
      spendUSD: number;
    }
  | { kind: "interrupted"; text: string };

// Matches the run statuses the engine treats as final (see useRunTranscript's
// reconcile loop, which polls until the run reports one of these).
const TERMINAL = new Set(["success", "failed", "stopped"]);

/**
 * Narrow stored run rows to the shape resolveReply reads.
 *
 * Only settled rows carry a meaningful result: a row still marked `pending` or
 * `running` is a step the engine had not finished writing, so including it
 * would let resolveReply treat a half-written step as the answer.
 */
export function toLogEvents(rows: RunLogRecord[]): LogEvent[] {
  return rows
    .filter((r) => r.status === "success" || r.status === "failed")
    .map((r) => ({
      stepIndex: r.stepIndex,
      nodeId: r.nodeId,
      nodeType: r.nodeType,
      status: r.status as "success" | "failed",
      output: r.output,
      durationMs: r.durationMs ?? 0,
      ts: r.ts,
    }))
    .sort((a, b) => a.stepIndex - b.stepIndex);
}

/**
 * @param run   The run's stored record, or null when the turn never got a run
 *              id or the lookup failed.
 * @param rows  That run's stored log rows.
 */
export function recoverPendingTurn(
  run: { status: string } | null,
  rows: RunLogRecord[],
): PendingRecovery {
  // No run to point at: the reload landed between sending the message and the
  // backend returning a run id, so nothing was ever started worth recovering.
  if (!run) {
    return {
      kind: "interrupted",
      text: "Interrupted before this run started — send the message again to retry.",
    };
  }

  // The run is still going somewhere; this browser tab just stopped watching
  // it. Claiming success or failure would be a guess, so say what we know.
  if (!TERMINAL.has(run.status)) {
    return {
      kind: "interrupted",
      text: "Interrupted by a reload while this run was still going — open the logs to see how it finished.",
    };
  }

  const summary = resolveReply(toLogEvents(rows));
  return {
    kind: "resolved",
    text: summary.text,
    isError: summary.isError,
    toolCount: summary.toolCount,
    spendUSD: summary.spendUSD,
  };
}
