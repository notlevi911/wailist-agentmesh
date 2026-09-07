"use client";
import { useEffect, useRef } from "react";
import { useRunTranscript, type LogEvent } from "../useRunTranscript";
import { useChatSession, type ChatSession } from "./useChatSession";
import { resolveReply } from "./resolveReply";
import { recoverPendingTurn } from "./recoverPendingTurn";
import {
  runs as runsApi,
  type RunLogRecord,
  type DeadLetterRun,
} from "@/lib/api";

interface UseChatConsoleArgs {
  runId: string | null;
  running: boolean;
  onRunComplete: () => void;
  workflowId?: string;
  onSendMessage?: (text: string) => Promise<boolean>;
  // Build mode routes a chat turn to graph editing instead of running the
  // deployed agent -- see CanvasPage's hasProviderNode/buildMode wiring.
  buildMode?: boolean;
  onBuildMessage?: (text: string) => Promise<{ ok: boolean; reply?: string }>;
  attempt?: number;
}

export interface ChatConsole {
  logs: LogEvent[];
  elapsed: number | null;
  done: boolean;
  leaseId: string | null;
  session: ChatSession;
  busy: boolean;
  handleSend: (text: string) => void;
  deadLetters: DeadLetterRun[];
}

// Owns the run transcript (SSE + reconciliation) and the chat session
// together, as a single hook call, so the right-side chat rail and the
// bottom-dock log list share one subscription instead of racing two.
export function useChatConsole({
  runId,
  running,
  onRunComplete,
  workflowId,
  onSendMessage,
  buildMode,
  onBuildMessage,
  attempt,
}: UseChatConsoleArgs): ChatConsole {
  const { logs, elapsed, done, leaseId, stopped, deadLetters } =
    useRunTranscript({
      runId,
      running,
      onRunComplete,
      workflowId,
      attempt,
    });

  const session = useChatSession(workflowId);

  // Bind the turn the user just sent to the run the backend actually started.
  // This effect has no access to the id startTurn returned to handleSend's
  // caller, so it binds by predicate via attachRun instead (see
  // useChatSession's attachRun for why that's still correct).
  const {
    attachRun,
    completeTurnForRun,
    completeTurnById,
    reopenTurnForRun,
    hydrated,
  } = session;
  useEffect(() => {
    if (!runId || !hydrated) return;
    attachRun(runId);
  }, [runId, hydrated, attachRun]);

  // A resume bumps `attempt` on the SAME runId (see CanvasPage's
  // handleResume / useRunTranscript's attempt-keyed reconnect), which takes
  // the transcript's `done` from true back to false and eventually true
  // again. But the chat turn this runId is bound to already settled
  // (pending: false) from the FIRST attempt's completion -- without
  // reopening it here, the "fill that turn in" effect below calls
  // completeTurnForRun a second time and it silently no-ops (settleIn only
  // ever touches a pending turn), leaving the bubble frozen on the
  // pre-resume outcome even after the resumed attempt succeeds.
  const prevAttemptRef = useRef(attempt);
  useEffect(() => {
    if (attempt === undefined || attempt === prevAttemptRef.current) return;
    prevAttemptRef.current = attempt;
    if (runId) reopenTurnForRun(runId);
  }, [attempt, runId, reopenTurnForRun]);

  // Fill that turn in once the run reaches a terminal state. Guarded on runId
  // so a transcript restored from cache (which arrives already `done`) can't
  // resolve a turn that belongs to some earlier run.
  useEffect(() => {
    if (!done || !runId || !hydrated) return;

    // A stopped run is finished but has no answer. Sending it through
    // resolveReply would report "The run finished." off a transcript with no
    // terminal rows -- stating an outcome we never got. Reuse the existing
    // `interrupted` state instead: same honest "we stopped watching" framing
    // as a reload-stranded turn, and it already renders with a warm label.
    if (stopped) {
      completeTurnForRun(runId, {
        text: "Run stopped. Whatever finished before then is in the logs.",
        interrupted: true,
        elapsedS: elapsed ?? undefined,
        runId,
      });
      return;
    }

    const summary = resolveReply(logs);
    completeTurnForRun(runId, {
      text: summary.text,
      isError: summary.isError,
      toolCount: summary.toolCount,
      spendUSD: summary.spendUSD,
      elapsedS: elapsed ?? undefined,
      runId,
    });
  }, [done, stopped, runId, hydrated, logs, elapsed, completeTurnForRun]);

  // Recover a turn stranded by a page reload. See ConsolePanel's former
  // version of this effect for the full ordering rationale -- unchanged here,
  // just relocated.
  const recoveredRef = useRef(false);
  const messagesRef = useRef(session.messages);
  useEffect(() => {
    messagesRef.current = session.messages;
  }, [session.messages]);
  useEffect(() => {
    if (!hydrated || runId || recoveredRef.current) return;
    recoveredRef.current = true;

    const stranded = [...messagesRef.current].reverse().find((m) => m.pending);
    if (!stranded) return;

    let cancelled = false;
    void (async () => {
      let record: { status: string } | null = null;
      let rows: RunLogRecord[] = [];
      if (stranded.runId) {
        try {
          const res = await runsApi.get(stranded.runId);
          record = res.run;
          rows = res.logs;
        } catch {
          // Leave record null: recoverPendingTurn reports it as interrupted
          // rather than inventing an outcome.
        }
      }
      if (cancelled) return;
      const recovery = recoverPendingTurn(record, rows);
      completeTurnById(
        stranded.id,
        recovery.kind === "resolved"
          ? {
              text: recovery.text,
              isError: recovery.isError,
              toolCount: recovery.toolCount,
              spendUSD: recovery.spendUSD,
              runId: stranded.runId,
            }
          : {
              text: recovery.text,
              interrupted: true,
              runId: stranded.runId,
            },
      );
    })();

    return () => {
      cancelled = true;
    };
  }, [hydrated, runId, completeTurnById]);

  const handleSend = (text: string) => {
    const turnId = session.startTurn(text);
    // A send that never becomes a run (offline, not deployed, backend
    // refusal) has nothing left to settle its turn -- startRun only surfaces
    // a toast -- so the bubble would spin forever. Settle it here instead.
    void (async () => {
      if (buildMode) {
        // Build mode has no runId/SSE transcript to settle against -- it
        // resolves synchronously in one round trip, so settle the turn
        // directly here instead of via the runId-keyed effects above.
        const res = (await onBuildMessage?.(text)) ?? { ok: false };
        completeTurnById(turnId, {
          text:
            res.reply ??
            (res.ok
              ? "Done."
              : "Could not update the workflow — see the notification for why, then try again."),
          isError: !res.ok,
        });
        return;
      }
      const ok = (await onSendMessage?.(text)) ?? false;
      if (!ok) {
        completeTurnById(turnId, {
          text: "Could not start this run — see the notification for why, then try again.",
          isError: true,
        });
      }
    })();
  };

  // The composer must lock the instant a turn is created, not when `running`
  // flips -- CanvasPage sets that only after workflows.run() resolves,
  // leaving the whole round trip open to a double Send that starts two real,
  // billed runs. A pending turn is the earliest honest signal that we are
  // busy.
  // `!hydrated` matters as much as the other two: hydration lands one
  // requestAnimationFrame after mount, and a send inside that window is
  // appended to in-memory state and then wiped by the deferred
  // setMessages(stored...) -- while workflows.run() has already fired and can
  // bill. The run happens, the chat has no record of it.
  const busy = !hydrated || running || session.messages.some((m) => m.pending);

  return {
    logs,
    elapsed,
    done,
    leaseId,
    session,
    busy,
    handleSend,
    deadLetters,
  };
}
