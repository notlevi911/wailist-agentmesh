"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  runs as runsApi,
  type RunLogRecord,
  type DeadLetterRun,
} from "@/lib/api";

// This hook owns everything about *what happened in a run*: the live SSE
// stream, the DB reconciliation that covers the stream's gaps, and the cached
// last-run transcript.
// It was extracted verbatim from LogDrawer -- every comment below is a
// postmortem of a real failure, so treat behavioural changes here as bugs
// unless they come with their own investigation.
//
// Presentation (height, tabs, open/closed) deliberately stays in the panel
// components: this hook is the data layer they share.

export interface LogEvent {
  stepIndex: number;
  nodeId: string;
  nodeType: string;
  status: "running" | "success" | "failed" | "stopped";
  output: unknown;
  durationMs: number;
  ts: string;
}

export interface X402Payment {
  txId: string;
  amount?: string;
  explorerURL?: string;
  // outboundTxId/outboundExplorerURL are the SECOND real settlement leg --
  // txId above is always the inbound leg (caller -> Wallet 2), this is
  // Wallet 2 -> the actual target, when the target returned one (not
  // every target does).
  outboundTxId?: string;
  outboundExplorerURL?: string;
  nodeName?: string;
  nodeId?: string;
  // Set by the backend's prependRunFundingReceipt on the one synthetic row
  // that reports a run-level pre-fund's up-front settlement -- bookkeeping,
  // not a tool invocation. A per-call receipt never carries this.
  isFundingReceipt?: boolean;
}

// isX402Payment answers "does this receipt have an id I can link to an explorer".
// That is a *display* question -- paymentReceipt (provider.go) writes txId only
// when p.TxID != "", so a v2/relay settlement without one is still a real,
// billed payment. Never use this to decide whether money moved.
export function isX402Payment(output: unknown): output is X402Payment {
  return typeof output === "object" && output !== null && "txId" in output;
}

/**
 * What a step actually cost, in USD -- or null when it paid for nothing.
 *
 * The single source of truth for "money moved", deliberately shared between the
 * chat activity strip and the settlement rows the usage page reads. They were
 * written separately and drifted: one keyed off settledUsdMicros, the other off
 * the presence of a txId, so a v2/relay settlement with no id showed a cost in
 * chat that usage could not account for.
 *
 * platformFeeUsdMicros is added because it is a separate component of the
 * charge: paymentReceipt's comment states that "settledUsdMicros alone is NOT
 * the total charged for a v2 call ... a consumer that wants the real total must
 * add both fields."
 */
export function settledUsdOf(output: unknown): number | null {
  if (typeof output !== "object" || output === null) return null;
  const rec = output as {
    settledUsdMicros?: unknown;
    platformFeeUsdMicros?: unknown;
  };
  if (typeof rec.settledUsdMicros !== "number") return null;
  const fee =
    typeof rec.platformFeeUsdMicros === "number" ? rec.platformFeeUsdMicros : 0;
  return (rec.settledUsdMicros + fee) / 1e6;
}

const RUN_CACHE_PREFIX = "agentmesh_lastrun_";
// localStorage is ~5 MB per origin and a single x402 response can be tens of
// KB. Cap what we cache so one large transcript can't evict everything else
// (or throw on write) — the live view is never truncated, only the cache.
const MAX_CACHED_RUN_BYTES = 512 * 1024;

interface CachedRun {
  runId: string;
  logs: LogEvent[];
  elapsed: number | null;
  // Optional: cached blobs written before this field existed won't have it,
  // and readCachedRun must still accept those. Caches the already-resolved
  // (visibleDeadLetters) view, not the raw backend list, so a dead-letter
  // that was fixed by a resume before the cache write doesn't come back
  // from the dead on the next reload.
  deadLetters?: DeadLetterRun[];
}

function readCachedRun(workflowId: string | undefined): CachedRun | null {
  if (!workflowId || typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(RUN_CACHE_PREFIX + workflowId);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as CachedRun;
    if (!Array.isArray(parsed.logs)) return null;
    // deadLetters is a newer field -- an older cached blob won't have it,
    // and a corrupt one shouldn't crash the filter this feeds into.
    if (!Array.isArray(parsed.deadLetters)) parsed.deadLetters = [];
    return parsed;
  } catch {
    return null;
  }
}

function writeCachedRun(workflowId: string | undefined, run: CachedRun): void {
  if (!workflowId || typeof window === "undefined") return;
  try {
    const serialized = JSON.stringify(run);
    if (serialized.length > MAX_CACHED_RUN_BYTES) return;
    window.localStorage.setItem(RUN_CACHE_PREFIX + workflowId, serialized);
  } catch {
    /* quota or unavailable storage: caching is best-effort */
  }
}

const _CONFIGURED = process.env.NEXT_PUBLIC_API_URL ?? "";

// The SSE stream deliberately bypasses the /api rewrite proxy that every
// other API call in this codebase uses (see lib/api.ts's BASE pattern, kept
// for those calls since they're quick request/response and benefit from the
// same-site cookie handling that proxy exists for). Confirmed live
// (2026-08-01) that Next's rewrite (both `next dev`'s built-in proxy,
// unverified but plausibly also true of Vercel's edge rewrite in
// production) does not correctly hold a long-lived text/event-stream
// connection open: a real x402 settlement taking ~30s+ (a completely
// normal duration for a real facilitator+algod round trip) caused the
// proxied stream to silently fail with a bare 500 after ~30s, never
// delivering a single log event, while the exact same run watched directly
// against the backend delivered every event correctly. The frontend has no
// way to tell "the run legitimately finished with nothing to show" apart
// from "my SSE connection just broke" (see the identical es.onerror
// handling below), so this presented as "run complete · 0/0 nodes
// succeeded" for a run that had, in fact, fully succeeded and moved real
// money.
//
// Safe to go directly to the backend here specifically because it already
// sets the auth cookie as SameSite=None; Secure whenever its own BASE_URL
// is https (auth.go's setAuthCookie) -- exactly the condition under which a
// direct cross-origin EventSource with withCredentials:true correctly
// carries it. That's true in real production (Railway's BASE_URL is https)
// and was true in the local setup this bug was diagnosed against (BASE_URL
// pointed at an https cloudflared tunnel).
const SSE_BASE = _CONFIGURED;

interface UseRunTranscriptArgs {
  runId: string | null;
  running: boolean;
  onRunComplete: () => void;
  // Scopes the cached last-run transcript, so reopening a workflow shows that
  // workflow's own last result rather than whatever ran most recently.
  workflowId?: string;
  /** Bumped by the caller after a successful resume, on the SAME runId, to
   *  force the SSE-connect effect below to tear down and reconnect -- it's
   *  keyed on runId alone, which doesn't change across a resume. */
  attempt?: number;
}

export interface RunTranscript {
  logs: LogEvent[];
  elapsed: number | null;
  done: boolean;
  /** Lease opened by a Tendril rent step in this run, if any. */
  leaseId: string | null;
  /** The run was stopped from the UI rather than reaching its own end. */
  stopped: boolean;
  deadLetters: DeadLetterRun[];
}

export function useRunTranscript({
  runId,
  running,
  onRunComplete,
  workflowId,
  attempt = 0,
}: UseRunTranscriptArgs): RunTranscript {
  // With no live run, start from this workflow's cached last transcript so
  // reopening it still shows what happened, instead of an empty console.
  const cached = runId ? null : readCachedRun(workflowId);
  const [logs, setLogs] = useState<LogEvent[]>(cached?.logs ?? []);
  const [elapsed, setElapsed] = useState<number | null>(
    cached?.elapsed ?? null,
  );
  const [done, setDone] = useState(!!cached);
  const [stopped, setStopped] = useState(false);
  const [deadLetters, setDeadLetters] = useState<DeadLetterRun[]>(
    cached?.deadLetters ?? [],
  );
  const esRef = useRef<EventSource | null>(null);
  const startRef = useRef<number | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  // Mirrors `elapsed` without joining the SSE effect's dependency array --
  // read inside that effect (below) to continue the clock across a resume
  // instead of re-basing it to zero. Kept in sync via its own effect (right
  // below) rather than written during render: refs can't be safely written
  // during render, only in effects/event handlers.
  const elapsedRef = useRef(0);
  useEffect(() => {
    elapsedRef.current = elapsed ?? 0;
  }, [elapsed]);

  // Captured once, at mount only (a lazy useState initializer, like
  // `cached` above, runs exactly once) -- the cached transcript's own run
  // id, if this hook mounted with no live run at all (a page reload; by
  // the time the render-time block below runs, `cached` itself has already
  // gone back to null because `runId` is no longer null on that render). A
  // ref would read identically here, but reading ref.current during render
  // is exactly what react-hooks/refs exists to flag -- state is the
  // sanctioned way to read a render-time value that must not change again.
  const [cachedRunIdOnMount] = useState(() => cached?.runId ?? null);

  // Reset the transcript the moment a new run starts. This host used to be
  // remounted on key={runId} for exactly this reset, which also tore down
  // and rebuilt everything nested inside it -- including CanvasGraph, wiping
  // the user's pan/zoom on every single Run (see CanvasPage). Doing the
  // reset here, synchronously during render when runId is seen to differ
  // from what this hook's state currently reflects, is React's documented
  // pattern for "adjusting state when a prop changes" -- it avoids both the
  // remount and an extra render (a setState call in the SSE effect below
  // would still work, but trips react-hooks/set-state-in-effect and costs a
  // render the SSE effect doesn't need). A runId going back to null (never
  // happens today -- CanvasPage only ever sets it to a fresh id) is
  // deliberately left alone, matching the SSE effect's own `if (!runId)
  // return` below.
  const [transcriptRunId, setTranscriptRunId] = useState(runId);
  if (runId && runId !== transcriptRunId) {
    // Resuming a dead-letter restored from the cache (no live run existed
    // in this session until CanvasPage's handleResume fell back to the
    // dead-letter's own runId) is NOT a new run starting -- it's the same
    // run this hook already has a transcript for, from `cached` above.
    // Wiping logs/deadLetters here would blank the console for the whole
    // resumed run's duration, exactly the "wiping already-completed steps"
    // behavior this hook exists to avoid. done/stopped still reset: the
    // run is genuinely back in progress, cached or not.
    const resumingCachedRun = runId === cachedRunIdOnMount;
    setTranscriptRunId(runId);
    setDone(false);
    setStopped(false);
    if (!resumingCachedRun) {
      setLogs([]);
      setElapsed(null);
      setDeadLetters([]);
    }
  }

  // onRunComplete is invoked from inside the SSE effect, which must stay keyed
  // on runId alone -- re-running it because the parent passed a fresh closure
  // would tear down and re-open the stream mid-run. Held in a ref so the
  // effect's dependency list is identical to the one LogDrawer had.
  const onRunCompleteRef = useRef(onRunComplete);
  useEffect(() => {
    onRunCompleteRef.current = onRunComplete;
  }, [onRunComplete]);

  // A run finishes once, so its completion callback fires once. The "done"
  // event already marks the run complete and then hands off to reconcile(),
  // whose first poll sees the same terminal status and would fire the
  // callback a second time -- a redundant GET /runs/:id and a duplicate
  // refreshCredits() on every single run. reconcile() itself stays: it is
  // there to fill in rows the DB had not written when the terminal event
  // arrived. Only the double callback is the bug.
  const completedRef = useRef(false);
  const completeOnce = useCallback(() => {
    if (completedRef.current) return;
    completedRef.current = true;
    onRunCompleteRef.current();
  }, []);

  // Connect SSE for the run. Transcript state (logs/elapsed/done/stopped) is
  // reset above, during render; completedRef is a ref, not state, so
  // resetting it here in the effect is fine.
  useEffect(() => {
    if (!runId) return;
    completedRef.current = false;
    // Continue the elapsed clock across a resume (same runId, attempt bumped)
    // rather than re-basing to zero -- a fresh run already has elapsed/
    // elapsedRef at 0 here (reset by the render-time block above), so this
    // resolves correctly to Date.now() for a new run and continues in-place
    // for a resume.
    startRef.current = Date.now() - elapsedRef.current * 1000;
    // A resume (attempt bumped, same runId) re-enters this effect with the
    // run's prior terminal state still in `done`/`stopped` from before --
    // clear it so the dock shows "in progress" again instead of a stale
    // "run stopped"/"run complete" state while the resumed attempt runs.
    // logs/elapsed are deliberately left alone: completed steps stay visible.
    // Intentional synchronous reset, not the derived-state-cascade pattern
    // this lint rule exists to catch: it must land in the one render this
    // effect fires on, not a render later.
    /* eslint-disable react-hooks/set-state-in-effect */
    setDone(false);
    setStopped(false);
    setDeadLetters([]);
    /* eslint-enable react-hooks/set-state-in-effect */

    // Start elapsed timer
    timerRef.current = setInterval(() => {
      setElapsed(Math.floor((Date.now() - startRef.current!) / 100) / 10);
    }, 100);

    const url = SSE_BASE ? `${SSE_BASE}/runs/${runId}/stream` : null;

    // The live stream only delivers events to a client subscribed at the
    // exact moment they're published (broker.go's Publish is a non-blocking,
    // unbuffered-per-subscriber send with no replay) -- a run that finishes
    // a step before this component's SSE connection is fully established
    // drops that step's event silently. Reconciling against the DB-backed
    // /runs/:id record once the stream ends closes that gap: this is what
    // actually fixes "run complete · 0/0 nodes succeeded" for a run that, in
    // truth, ran and failed (or succeeded) with a real, visible reason.
    // The DB is the only reliable source here: the live stream can deliver
    // nothing at all (confirmed 2026-08-02 — a 37s connection that carried a
    // single keepalive and zero events), and even when it works it can miss
    // steps. One shot at stream-close was not enough: a node's row is still
    // `running` until its work finishes and UpdateRunLog lands, so a single
    // fetch fired the moment the stream died filtered that row out and showed
    // an empty console for a run that had really completed and paid. Poll
    // until the run itself reports a terminal status.
    let cancelled = false;
    const TERMINAL = new Set(["success", "failed", "stopped"]);

    const mergeDBLogs = (dbLogs: RunLogRecord[]) => {
      setLogs((prev) => {
        // Mirrors the live "log" event handler's own replace-if-running-
        // else-append rule (below), applied to the DB-reconciliation path
        // too: `logs` intentionally holds one row per ATTEMPT, not one row
        // per node (resolveReply's latestPerNode collapses that down for
        // display) -- a resumed node that fails, then fails or succeeds
        // again, must get its own new row, not have the earlier attempt's
        // row silently reused or dropped as a "duplicate."
        //
        // A DB row replaces an existing entry only when that entry is the
        // still-open "running" placeholder for the SAME node (its terminal
        // SSE event was dropped -- this is that same attempt finally
        // resolving, not a new one). Otherwise it's appended as a new
        // attempt, unless it exactly matches an already-recorded terminal
        // row (same nodeId, status, AND ts) -- that's just a repeat poll of
        // the same unchanged state, not a real new attempt.
        let next = prev;
        let mutated = false;
        for (const l of dbLogs) {
          if (l.status !== "success" && l.status !== "failed") continue;
          if (
            next.some(
              (e) =>
                e.nodeId === l.nodeId &&
                e.status === l.status &&
                e.ts === l.ts,
            )
          ) {
            continue;
          }
          const entry = {
            stepIndex: l.stepIndex,
            nodeId: l.nodeId,
            nodeType: l.nodeType,
            status: l.status as "success" | "failed",
            output: l.output,
            durationMs: l.durationMs ?? 0,
            ts: l.ts,
          };
          const runningIdx = next.findIndex(
            (e) => e.nodeId === l.nodeId && e.status === "running",
          );
          if (!mutated) next = [...next];
          if (runningIdx >= 0) {
            next[runningIdx] = entry;
          } else {
            next.push(entry);
          }
          mutated = true;
        }
        if (!mutated) return prev;
        return next.sort((a, b) => a.stepIndex - b.stepIndex);
      });
    };

    const reconcile = async () => {
      if (!runId) return;
      // Budget generously. A single agent step routinely runs ~70s (LLM
      // round trips plus a real x402 settlement per tool call, confirmed
      // 2026-08-02), and an agent may take several tool iterations — while
      // the SSE stream dies around 35s, so polling starts long before the
      // run ends. A budget that expires first shows an empty console for a
      // run that completed and charged the user, which is exactly the
      // failure this polling exists to prevent. 900 attempts * 2s = 30
      // minutes, well past any observed run length, with real headroom for
      // an agent that chains many tool iterations.
      for (let poll = 0; poll < 900 && !cancelled; poll++) {
        try {
          const {
            run,
            logs: dbLogs,
            deadLetters: dl,
          } = await runsApi.get(runId);
          if (cancelled) return;
          if (dbLogs.length > 0) mergeDBLogs(dbLogs);
          // Preserve the existing reference when nothing actually changed:
          // this poll runs every 2s for up to 30 minutes, and a fresh array
          // reference on every tick (even an unchanged one) re-fires
          // ConsolePanel's auto-scroll effect (keyed on deadLetters) every
          // single poll, repeatedly yanking the view back down even if the
          // user manually scrolled up to reread an earlier entry.
          setDeadLetters((prev) => {
            const next = dl ?? [];
            if (
              prev.length === next.length &&
              prev.every((p, i) => p.id === next[i].id)
            ) {
              return prev;
            }
            return next;
          });
          if (TERMINAL.has(run.status)) {
            // The run itself is finished — this, not a dropped stream, is
            // what "complete" means.
            clearInterval(timerRef.current!);
            setDone(true);
            completeOnce();
            return;
          }
        } catch {
          // Transient failure: keep polling rather than giving up on a run
          // whose result is already recorded server-side.
        }
        await new Promise((r) => setTimeout(r, 2000));
      }
      // Budget exhausted without ever seeing a terminal status. The run may
      // still be going server-side, but the UI must not hang on "working…"
      // forever with no way out -- surface it as done with whatever partial
      // transcript was collected, same as a manual stop, rather than
      // silently returning and leaving the composer locked.
      if (!cancelled) {
        clearInterval(timerRef.current!);
        setDone(true);
        completeOnce();
      }
    };

    // No stream configured at all (NEXT_PUBLIC_API_URL unset -- the frontend's
    // mock mode). Go straight to the polling path a dead stream would fall
    // back to anyway, rather than returning early and leaving the run
    // permanently unfinished. Without this the console, and every chat turn
    // waiting on it, hangs on "working…" forever with no backend attached.
    if (!url) {
      void reconcile();
      return () => {
        cancelled = true;
        clearInterval(timerRef.current!);
      };
    }

    // withCredentials sends the HttpOnly auth cookie automatically.
    const es = new EventSource(url, { withCredentials: true });
    esRef.current = es;

    es.addEventListener("log", (e) => {
      try {
        const ev: LogEvent = JSON.parse((e as MessageEvent).data);
        setLogs((prev) => {
          // Replace running entry for same nodeId, or append
          const idx = prev.findIndex(
            (l) => l.nodeId === ev.nodeId && l.status === "running",
          );
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = ev;
            return next;
          }
          return [...prev, ev];
        });
      } catch {
        /* ignore parse errors */
      }
    });

    es.addEventListener("done", () => {
      clearInterval(timerRef.current!);
      setDone(true);
      completeOnce();
      es.close();
      void reconcile();
    });

    // A dropped stream says nothing about the run. EventSource fires onerror
    // on any transient hiccup (including its own reconnect attempts), and
    // treating that as "finished" reported "run complete · 1/1 nodes
    // succeeded" three seconds into a run whose agent still had ~60s of work
    // left — then closed the stream, guaranteeing the real result never
    // arrived live. Hand off to polling instead and let the run's own status
    // decide when it is over.
    es.onerror = () => {
      es.close();
      void reconcile();
    };

    return () => {
      cancelled = true;
      clearInterval(timerRef.current!);
      es.close();
    };
    // completeOnce is a stable useCallback([]), so listing it keeps the
    // linter happy without re-running this effect -- which must stay keyed on
    // runId and attempt (bumped by the caller to force a resume's
    // reconnect), per the note above.
  }, [runId, attempt, completeOnce]);

  // Stopped externally (the Run button's stop branch). Closing the stream is
  // not enough: the run is over, so the transcript has to say so. Leaving
  // `done` false stranded the chat turn that started it -- and because the
  // composer locks on `running || any pending turn`, that turn kept it
  // disabled until a full page reload.
  useEffect(() => {
    if (running || !esRef.current) return;
    clearInterval(timerRef.current!);
    esRef.current.close();
    esRef.current = null;
    setStopped(true);
    setDone(true);
    completeOnce();
  }, [running, completeOnce]);

  // No client-side settlement recording here any more. The engine writes one
  // run_logs row per settlement as it happens (see runner.go's publish loop),
  // which is both durable and user-scoped through runs -> workflows, so the
  // usage page reads GET /usage/settlements instead of a localStorage copy
  // this hook used to build from the transcript. That copy followed the
  // browser rather than the account.

  // Detect the lease a Tendril rent step in this run opened, so the console
  // can offer a Terminal tab into it. Takes the most recent one — a run
  // renting two machines would need explicit lease-id wiring between nodes,
  // out of scope here (see the plan's "Multiple concurrent leases per run").
  const leaseId = useMemo(() => {
    for (let i = logs.length - 1; i >= 0; i--) {
      const output = logs[i].output;
      if (
        logs[i].nodeType === "tendril" &&
        typeof output === "object" &&
        output !== null &&
        "agentMeshLeaseId" in output
      ) {
        const id = (output as { agentMeshLeaseId?: unknown }).agentMeshLeaseId;
        if (typeof id === "string" && id) return id;
      }
    }
    return null;
  }, [logs]);

  // The backend's dead_letter_runs table is insert-only -- GetDeadLetterRuns
  // returns every row for a run id forever, even after that node has since
  // succeeded on a resumed attempt. Without this filter, a resumed node that
  // has since succeeded would still show a stale "dead-lettered" pill with a
  // live Resume button, right alongside its own "success" row. The node's
  // own execution history in `logs` is the authoritative signal here, not
  // the never-cleared backend table.
  //
  // A node can also dead-letter more than once (resumed, failed again) --
  // GetDeadLetterRuns returns every one of those rows too (ordered oldest
  // first), so without collapsing to one-per-node here a still-failing node
  // would render two "dead-lettered" pills with two live Resume buttons
  // instead of just its latest attempt.
  const visibleDeadLetters = useMemo(() => {
    const latestByNode = new Map<string, DeadLetterRun>();
    for (const dl of deadLetters) latestByNode.set(dl.nodeId, dl);
    return Array.from(latestByNode.values()).filter(
      (dl) =>
        !logs.some((l) => l.nodeId === dl.nodeId && l.status === "success"),
    );
  }, [deadLetters, logs]);

  // Cache the finished transcript for this workflow, including its
  // (already-resolved) dead-letters -- otherwise a reload mid-dead-letter
  // loses the pill and the Resume action entirely, even though the run is
  // still resumable server-side. Runs after `done` so it captures the
  // reconciled set (including any steps the live stream missed), not a
  // partial mid-run snapshot.
  useEffect(() => {
    if (!done || !runId || logs.length === 0) return;
    writeCachedRun(workflowId, {
      runId,
      logs,
      elapsed,
      deadLetters: visibleDeadLetters,
    });
  }, [done, runId, logs, elapsed, workflowId, visibleDeadLetters]);

  return {
    logs,
    elapsed,
    done,
    leaseId,
    stopped,
    deadLetters: visibleDeadLetters,
  };
}
