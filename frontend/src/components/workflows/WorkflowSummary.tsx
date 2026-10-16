"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { Topbar } from "@/components/Topbar";
import { PullToRefresh } from "@/components/PullToRefresh";
import { Skeleton } from "@/components/ui/Skeleton";
import { ghostBtn, primaryBtn } from "@/components/ui/buttons";
import { RunSheet } from "@/components/runs/RunSheet";
import { RunProgressDock } from "@/components/runs/RunProgressDock";
import { useRunDetail } from "@/components/runs/useRunDetail";
import { workflowSteps } from "@/lib/runProgress";
import { nextResponseSeq } from "@/lib/responseOrder";
import { RunStatusPill } from "@/components/runs/RunStatusPill";
import { UpcomingRuns } from "@/components/runs/UpcomingRuns";
import { useNow } from "@/hooks/useNow";
import { usePolling } from "@/hooks/usePolling";
import {
  runs as runsApi,
  workflows as workflowsApi,
  RunsUnavailableError,
} from "@/lib/api";
import type { RunPage, RunStatus, RunSummary, Workflow } from "@/lib/types";
import { groupRunsByDay } from "@/lib/runDays";
import { mergeRuns } from "@/lib/runMerge";
import {
  formatDuration,
  formatRunTime,
  formatSpend,
  formatUntil,
  triggerLabel,
} from "@/lib/runFormat";
import { workflowHref } from "@/lib/routes";
import { describeWorkflow, workflowAgents } from "@/lib/describeWorkflow";
import { describeSchedule } from "@/lib/describeSchedule";
import { statsKnown, UNKNOWN } from "@/lib/workflowMeta";

// A workflow as a phone needs it: is it running, what did its runs do and
// cost, and Run or Stop. The graph itself is not shown here; it is edited on a
// desktop, and a read-only canvas on a small screen answered none of these.

const PAGE_SIZE = 20;
// How often the list refreshes while a run is still going, and while none is.
const POLL_MS = 3_000;
const IDLE_POLL_MS = 10_000;

const WORKFLOW_STATUS: Record<
  string,
  { tone: "ok" | "warm" | "danger" | "default"; label: string }
> = {
  deployed: { tone: "ok", label: "Deployed" },
  paused: { tone: "warm", label: "Paused" },
  error: { tone: "danger", label: "Error" },
  draft: { tone: "default", label: "Draft" },
};

// Status colour by tone, the same dot the phone Workflows list uses.
const TONE_COLOR: Record<string, string> = {
  ok: "var(--accent)",
  warm: "var(--warm)",
  danger: "var(--danger)",
  default: "var(--fg-dim)",
};

const count = new Intl.NumberFormat();

// What the workflow is and what it has done: its description (or, until one
// is written, a summary read off its graph), its run figures, its agents
// and its next scheduled runs.
function WorkflowDetails({ workflow }: { workflow: Workflow }) {
  const agents = workflowAgents(workflow);
  const spent = Number.parseFloat(workflow.spend ?? "");
  // The 30-day pair comes from the same aggregation the list uses, and it
  // can fail on its own while the workflow itself reads fine. Both are then
  // zero for want of an answer, not because nothing ran. `totalRuns` has its
  // own nullable field and already says so by itself.
  const figuresKnown = statsKnown(workflow);
  return (
    <>
      <section aria-label="About this workflow" style={{ marginTop: 24 }}>
        <p className="wfd-desc">
          {workflow.description || describeWorkflow(workflow)}
        </p>
        {!workflow.description && (
          <p className="wfd-note">Summarised from its steps.</p>
        )}
        <dl className="wfd-stats">
          <div>
            <dt>Total runs</dt>
            <dd>
              {workflow.totalRuns !== undefined
                ? count.format(workflow.totalRuns)
                : "-"}
            </dd>
          </div>
          <div>
            <dt>Runs · 30 days</dt>
            <dd>{figuresKnown ? count.format(workflow.runs ?? 0) : UNKNOWN}</dd>
          </div>
          <div>
            <dt>Spent · 30 days</dt>
            <dd>
              {figuresKnown
                ? formatSpend(
                    Number.isFinite(spent) ? Math.round(spent * 1e6) : 0,
                  )
                : UNKNOWN}
            </dd>
          </div>
          <div>
            <dt>Next run</dt>
            <dd>{formatUntil(workflow.scheduleNextRunAt)}</dd>
          </div>
        </dl>
      </section>

      {agents.length > 0 && (
        <section aria-labelledby="wf-summary-agents" style={{ marginTop: 24 }}>
          <h2 id="wf-summary-agents" style={sectionLabel}>
            Agents
          </h2>
          <ul className="wfd-agents">
            {agents.map((a) => (
              <li key={a.id} className="wfd-agent">
                <span className="wfd-agent__name">{a.name}</span>
                <span className="wfd-agent__meta">
                  {[
                    a.model ?? "No model attached",
                    a.tools
                      ? `${a.tools} ${a.tools === 1 ? "tool" : "tools"}`
                      : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {workflow.scheduleCron && (
        <div style={{ marginTop: 24 }}>
          <UpcomingRuns workflowId={workflow.id} limit={3} hideWhenEmpty />
        </div>
      )}
    </>
  );
}

function isChatWorkflow(wf: Workflow): boolean {
  return wf.nodes.some((n) => n.type === "trigger" && n.template === "chat");
}

export function WorkflowSummary({ workflowId }: { workflowId: string }) {
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [runList, setRunList] = useState<RunSummary[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [runsLoaded, setRunsLoaded] = useState(false);
  const [runsUnavailable, setRunsUnavailable] = useState(false);
  const [runsError, setRunsError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  // Set once an older page has been appended, so a refresh of the first page
  // does not reset the cursor back to the second page.
  const pagedRef = useRef(false);

  // The run just started from here, shown before the list has caught up.
  const [pending, setPending] = useState<RunSummary | null>(null);
  const [acting, setActing] = useState(false);
  // The same flag, readable and writable synchronously. See `run` below.
  const actingRef = useRef(false);
  // Where the run list's last answer sits in the order responses landed.
  // Only a successful read sets it: a failed poll tells us nothing newer,
  // and must not make the list look fresher than it is.
  const [runsAnsweredSeq, setRunsAnsweredSeq] = useState<number | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const [selected, setSelected] = useState<RunSummary | null>(null);
  // The run started from here, followed step by step at the bottom of the
  // screen until it finishes or is dismissed.
  const [trackedRunId, setTrackedRunId] = useState<string | null>(null);
  const openerRef = useRef<HTMLElement | null>(null);

  // What a response does to the screen, kept apart from the request so the
  // first load, a pull and the poll all apply it the same way.
  const applyRunPage = useCallback((page: RunPage) => {
    setRunsAnsweredSeq(nextResponseSeq());
    setRunList((prev) => mergeRuns(page.runs, prev));
    if (!pagedRef.current) setNextCursor(page.nextCursor);
    setRunsUnavailable(false);
    setRunsError(null);
    setRunsLoaded(true);
  }, []);
  const applyRunsError = useCallback((e: unknown) => {
    if (e instanceof RunsUnavailableError) setRunsUnavailable(true);
    else setRunsError(e instanceof Error ? e.message : "Could not load runs.");
    setRunsLoaded(true);
  }, []);
  const applyWorkflow = useCallback((wf: Workflow) => {
    setWorkflow(wf);
    setLoadError(null);
  }, []);
  const applyWorkflowError = useCallback((e: unknown) => {
    setLoadError(
      e instanceof Error ? e.message : "Could not load this workflow.",
    );
  }, []);

  // Numbers each first-page request: the first load, a pull, a poll. Only
  // the newest one started may land, so a slow first load cannot replace
  // what a later poll already showed.
  const runsSeq = useRef(0);
  const refreshRuns = useCallback(() => {
    const seq = ++runsSeq.current;
    return runsApi.listForWorkflow(workflowId, { limit: PAGE_SIZE }).then(
      (page) => {
        if (seq === runsSeq.current) applyRunPage(page);
      },
      (e: unknown) => {
        if (seq === runsSeq.current) applyRunsError(e);
      },
    );
  }, [workflowId, applyRunPage, applyRunsError]);
  // The workflow is read from three places -- the first load, a pull, and a
  // change in run activity -- and they can overlap. Numbered the same way, so
  // a slow read (say, the one a run starting triggered) cannot land after a
  // newer one and put back figures from before the run finished.
  //
  // "Newer" means newer and successful. A read that fails changes nothing on
  // screen, so it must not stop an older one from landing: the quiet re-read
  // after a run starts can fail while the first load is still in flight, and
  // when that outranked the first load the screen stayed on its skeleton.
  const workflowSeq = useRef(0);
  const shownWorkflowSeq = useRef(0);
  const landWorkflow = useCallback(
    (seq: number, wf: Workflow) => {
      if (seq <= shownWorkflowSeq.current) return;
      shownWorkflowSeq.current = seq;
      applyWorkflow(wf);
    },
    [applyWorkflow],
  );
  // An error is shown only while nothing newer has succeeded.
  const landWorkflowError = useCallback(
    (seq: number, e: unknown, onError?: (e: unknown) => void) => {
      if (seq > shownWorkflowSeq.current) onError?.(e);
    },
    [],
  );
  const readWorkflow = useCallback(
    (onError?: (e: unknown) => void) => {
      const seq = ++workflowSeq.current;
      return workflowsApi.get(workflowId).then(
        (wf) => landWorkflow(seq, wf),
        (e: unknown) => landWorkflowError(seq, e, onError),
      );
    },
    [workflowId, landWorkflow, landWorkflowError],
  );
  const loadWorkflow = useCallback(
    () => readWorkflow(applyWorkflowError),
    [readWorkflow, applyWorkflowError],
  );

  // The first load. State is only set once a response lands, and not at all
  // if the screen has gone by then.
  useEffect(() => {
    let cancelled = false;
    const unlessGone =
      <T,>(apply: (value: T) => void) =>
      (value: T) => {
        if (!cancelled) apply(value);
      };
    // Reads still in flight for a previous workflow must never land here.
    shownWorkflowSeq.current = workflowSeq.current;
    const wfSeq = ++workflowSeq.current;
    workflowsApi.get(workflowId).then(
      unlessGone((wf: Workflow) => landWorkflow(wfSeq, wf)),
      unlessGone((e: unknown) =>
        landWorkflowError(wfSeq, e, applyWorkflowError),
      ),
    );
    const seq = ++runsSeq.current;
    const unlessSuperseded =
      <T,>(apply: (value: T) => void) =>
      (value: T) => {
        if (seq === runsSeq.current) apply(value);
      };
    runsApi
      .listForWorkflow(workflowId, { limit: PAGE_SIZE })
      .then(
        unlessGone(unlessSuperseded(applyRunPage)),
        unlessGone(unlessSuperseded(applyRunsError)),
      );
    return () => {
      cancelled = true;
    };
  }, [
    workflowId,
    landWorkflow,
    landWorkflowError,
    applyWorkflowError,
    applyRunPage,
    applyRunsError,
  ]);

  const pendingShown =
    pending && !runList.some((r) => r.id === pending.id) ? pending : null;
  const shown = pendingShown ? [pendingShown, ...runList] : runList;
  const anyRunning = shown.some((r) => r.status === "running");
  // The milestones the dock draws, from the graph rather than from the logs:
  // logs arrive per attempt and carry a topological level, not a position.
  const steps = useMemo(
    () => (workflow ? workflowSteps(workflow) : []),
    [workflow],
  );
  const trackedRow = shown.find((r) => r.id === trackedRunId) ?? null;
  // The row as the SERVER has it. `shown` may lead with the optimistic row
  // put up the moment Run was pressed, which says "running" because that is
  // what was asked for, not because anything has confirmed it.
  const trackedServerRow = runList.find((r) => r.id === trackedRunId) ?? null;
  // The same 2s poll the run sheet uses. It stops by itself once the run is
  // no longer running, so a finished dock costs nothing -- unless the list
  // says this run is going again, which is how a Resume reaches it.
  const trackedDetail = useRunDetail(
    trackedRunId,
    trackedServerRow?.status === "running",
  );
  // The list and the detail are two readings of one run, and each can be the
  // fresher one:
  //
  //   - A GET /runs/{id} that keeps failing leaves the detail with nothing,
  //     and assuming "running" left a dock that never finished and had no
  //     Dismiss. The list polls separately and usually knows the answer.
  //   - useRunDetail stops polling once it reads a terminal status, so a
  //     Resume under the same run id leaves the detail on "failed" for good
  //     while the list moves back to "running".
  //
  // Neither source is reliably the fresher one, so whichever answered last
  // wins. Taking the row unconditionally broke the mirror image of the
  // Resume case: the list says running, the detail then reads terminal, and
  // the next list poll fails. useRunDetail has stopped polling by then, so
  // the dock would have sat on a stale "running" for good, with no way out.
  //
  // The optimistic row stays the last resort either way -- it is the
  // request, not an answer.
  const detailStatus = trackedDetail.run?.status ?? null;
  const rowStatus = trackedServerRow?.status ?? null;
  // Both sides take their number from one counter, so there is no tie to
  // break: whichever answer landed second has the larger one.
  const detailIsNewer =
    (trackedDetail.answeredSeq ?? 0) > (runsAnsweredSeq ?? 0);
  const trackedStatus =
    (rowStatus !== null && detailStatus !== null
      ? detailIsNewer
        ? detailStatus
        : rowStatus
      : (rowStatus ?? detailStatus)) ??
    trackedRow?.status ??
    "running";
  // Only worth saying while nothing else can answer. The optimistic row does
  // not count: it is the request, not an answer, and a detail that never
  // loads would otherwise leave the dock on a "running" nothing confirmed.
  const trackedDetailError =
    !trackedServerRow && !detailStatus ? trackedDetail.error : null;
  const newestRunning = shown[0]?.status === "running";

  // Keep refreshing while the screen is visible, and at once on coming back
  // to it. A backgrounded app has nobody to show a status change to, but this
  // workflow can be run from the website or by its trigger at any time, so an
  // idle list is polled too, only more slowly than one with a run going.
  const pendingId = pendingShown?.id ?? null;
  // Returns its requests, so usePolling waits for them before the next poll.
  const poll = useCallback(() => {
    const requests: Promise<unknown>[] = [refreshRuns()];
    // A run the list has not picked up yet is asked about directly, so it
    // still settles when the list is slow to include it.
    if (pendingId) {
      const pendingRequest = runsApi
        .get(pendingId)
        .then(({ run }) => {
          if (run.status === "running") return;
          setPending((p) =>
            p && p.id === pendingId
              ? {
                  ...p,
                  status: run.status as RunStatus,
                  finishedAt: run.finishedAt,
                }
              : p,
          );
        })
        .catch(() => {});
      requests.push(pendingRequest);
    }
    return Promise.all(requests);
  }, [pendingId, refreshRuns]);
  usePolling(poll, anyRunning ? POLL_MS : IDLE_POLL_MS);

  // Total runs, the 30-day figures and the next scheduled run come with the
  // workflow, which is otherwise read once. So it is read again whenever the
  // runs move: a new run appears (started here, elsewhere or by the
  // schedule, which also advances the next run) or a running one finishes.
  // Quietly -- a failed refresh keeps the figures already shown.
  const runActivity = [
    shown[0]?.id ?? "",
    ...shown.filter((r) => r.status === "running").map((r) => r.id),
  ].join("|");
  const seenActivity = useRef<string | null>(null);
  useEffect(() => {
    if (!runsLoaded) return;
    const previous = seenActivity.current;
    seenActivity.current = runActivity;
    if (previous === null || previous === runActivity) return;
    void readWorkflow();
  }, [runActivity, runsLoaded, readWorkflow]);

  const now = useNow(anyRunning);

  const loadMore = async () => {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await runsApi.listForWorkflow(workflowId, {
        cursor: nextCursor,
        limit: PAGE_SIZE,
      });
      pagedRef.current = true;
      setRunList((prev) => mergeRuns(page.runs, prev));
      setNextCursor(page.nextCursor);
    } catch (e) {
      setRunsError(e instanceof Error ? e.message : "Could not load runs.");
    } finally {
      setLoadingMore(false);
    }
  };

  const run = async () => {
    if (!workflow) return;
    // setActing is a state update: React has not committed it by the time a
    // second activation arrives in the same tick, and `acting` on the button
    // is therefore still false. Two quick taps on Run, or on the dock's Run
    // again, started two runs and billed for both. The ref is written now.
    if (actingRef.current) return;
    actingRef.current = true;
    setActing(true);
    setActionError(null);
    try {
      const { runId } = await workflowsApi.run(workflowId);
      // The run started from this screen is the one the dock follows.
      setTrackedRunId(runId);
      setPending({
        id: runId,
        workflowId,
        workflowName: workflow.name,
        triggeredBy: "manual",
        status: "running",
        startedAt: new Date().toISOString(),
        spendUsdMicros: 0,
      });
      void refreshRuns();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "Could not start a run.");
    } finally {
      actingRef.current = false;
      setActing(false);
    }
  };

  const stop = async () => {
    if (actingRef.current) return;
    actingRef.current = true;
    setActing(true);
    setActionError(null);
    try {
      await workflowsApi.stop(workflowId);
      await refreshRuns();
    } catch (e) {
      setActionError(
        e instanceof Error ? e.message : "Could not stop the run.",
      );
    } finally {
      actingRef.current = false;
      setActing(false);
    }
  };

  const refreshAll = useCallback(
    () => Promise.all([loadWorkflow(), refreshRuns()]),
    [loadWorkflow, refreshRuns],
  );

  const chat = workflow ? isChatWorkflow(workflow) : false;
  // A handheld can neither build nor deploy, so whatever is keeping an
  // undeployed workflow from running, the next step is the desktop app.
  const blocked = workflow
    ? chat
      ? "This workflow starts from a chat message: run it from the AgentMesh desktop app"
      : workflow.status === "paused"
        ? "Paused: resume it in the AgentMesh desktop app"
        : workflow.status !== "deployed"
          ? "Not deployed yet: finish and deploy it in the AgentMesh desktop app"
          : null
    : null;
  const status =
    WORKFLOW_STATUS[workflow?.status ?? "draft"] ?? WORKFLOW_STATUS.draft;
  // The screen waits for BOTH the workflow and its runs before it draws
  // anything but the skeleton. The header's height depends on the runs -- a
  // running one turns Run into Stop and takes the "not deployed" line away --
  // so drawing it first moved the runs below it twice as each fetch landed.
  const ready =
    workflow !== null && (runsLoaded || runsUnavailable || runsError !== null);
  const hasZone = workflow?.geofenceLat !== undefined;

  // The sheet follows the row, not the copy taken when it was tapped, so a
  // refresh that brings new spend or a new status reaches the open sheet.
  const selectedRun = selected
    ? (shown.find((r) => r.id === selected.id) ?? selected)
    : null;

  return (
    <div
      className="am-viewport"
      style={{
        height: "100dvh",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background: "var(--bg)",
      }}
    >
      <Topbar />
      <PullToRefresh
        onRefresh={refreshAll}
        style={{ flex: 1, minHeight: 0, background: "var(--bg)" }}
      >
        <main style={page}>
          <Link
            href="/workflows"
            className="wf-summary-action"
            style={{ ...ghostBtn, minHeight: 44, textDecoration: "none" }}
          >
            ← Workflows
          </Link>

          {loadError ? (
            <p role="alert" style={{ ...copy, marginTop: 20 }}>
              {loadError}
            </p>
          ) : !ready ? (
            <div
              aria-busy="true"
              style={{ marginTop: 20, display: "grid", gap: "var(--s-3)" }}
            >
              <Skeleton width="60%" height={22} />
              <Skeleton width="40%" height={14} />
              <Skeleton width="100%" height={44} radius="var(--r-2)" />
            </div>
          ) : (
            <header style={{ marginTop: 20 }}>
              <div style={titleRow}>
                <h1 style={title}>{workflow.name}</h1>
                <span className="wfd-status">
                  <span
                    className="wfd-status__dot"
                    style={{ background: TONE_COLOR[status.tone] }}
                    aria-hidden
                  />
                  {status.label}
                </span>
              </div>

              <p style={{ ...copy, marginTop: 6 }}>
                {chat
                  ? "Starts from a chat message"
                  : workflow.scheduleCron
                    ? "Runs on a schedule · "
                    : "Runs when started"}
                {/* In words and the reader's own time. The cron itself stays
                    in the tooltip for whoever needs the exact expression. */}
                {!chat && workflow.scheduleCron && (
                  <span style={schedule} title={workflow.scheduleCron}>
                    {describeSchedule(workflow.scheduleCron)}
                  </span>
                )}
              </p>

              {workflow.status === "deployed" && (
                <Link
                  href={workflowHref(workflowId, { geofence: true })}
                  className="wf-summary-action"
                  style={zoneLink}
                >
                  {hasZone
                    ? "Location zone set · Change"
                    : "Add a location zone"}
                </Link>
              )}

              <div style={{ marginTop: 16 }}>
                {newestRunning ? (
                  <button
                    type="button"
                    className="wf-summary-action"
                    style={{ ...ghostBtn, ...fullWidth }}
                    onClick={stop}
                    disabled={acting}
                  >
                    {acting ? "Stopping…" : "Stop"}
                  </button>
                ) : (
                  <button
                    type="button"
                    className="wf-summary-action"
                    style={{
                      ...primaryBtn,
                      ...fullWidth,
                      opacity: blocked ? 0.5 : 1,
                    }}
                    onClick={run}
                    disabled={acting || blocked !== null}
                    aria-describedby={
                      blocked ? "wf-summary-blocked" : undefined
                    }
                  >
                    {acting ? "Starting…" : "Run"}
                  </button>
                )}
                {blocked && !newestRunning && (
                  <p
                    id="wf-summary-blocked"
                    style={{ ...copy, marginTop: 8, fontSize: "var(--t-2)" }}
                  >
                    {blocked}
                  </p>
                )}
                {actionError && (
                  <p
                    role="alert"
                    style={{
                      ...copy,
                      marginTop: 8,
                      fontSize: "var(--t-2)",
                      color: "var(--danger)",
                    }}
                  >
                    {actionError}
                  </p>
                )}
              </div>
            </header>
          )}

          {ready && workflow && <WorkflowDetails workflow={workflow} />}

          {ready && (
            <section
              aria-labelledby="wf-summary-runs"
              style={{ marginTop: 28 }}
            >
              <h2 id="wf-summary-runs" style={sectionLabel}>
                Recent runs
              </h2>

              {runsUnavailable ? (
                <p style={copy}>
                  Run history is not available on this server yet.
                </p>
              ) : !runsLoaded ? (
                <div aria-busy="true" style={{ display: "grid", gap: "var(--s-3)" }}>
                  <Skeleton width="100%" height={56} radius="var(--r-2)" />
                  <Skeleton width="100%" height={56} radius="var(--r-2)" />
                </div>
              ) : shown.length === 0 ? (
                <p style={copy}>
                  {runsError ?? "No runs yet. Runs show up here as they start."}
                </p>
              ) : (
                <>
                  {runsError && (
                    <p
                      role="alert"
                      style={{
                        ...copy,
                        marginBottom: 8,
                        color: "var(--danger)",
                      }}
                    >
                      {runsError}
                    </p>
                  )}
                  {groupRunsByDay(shown).map((group) => (
                    <div key={group.key} style={{ marginBottom: 16 }}>
                      <h3 style={dayLabel}>{group.label}</h3>
                      <ul style={list}>
                        {group.runs.map((r) => (
                          <li key={r.id}>
                            <button
                              type="button"
                              className="wf-summary-row"
                              style={row}
                              onClick={(e) => {
                                openerRef.current = e.currentTarget;
                                setSelected(r);
                              }}
                            >
                              <span style={rowMain}>
                                <RunStatusPill status={r.status} />
                                <span style={rowMeta}>
                                  {triggerLabel(r.triggeredBy)} ·{" "}
                                  {formatRunTime(r.startedAt)}
                                </span>
                              </span>
                              <span style={rowFigures}>
                                <span>{formatSpend(r.spendUsdMicros)}</span>
                                <span style={{ color: "var(--fg-dim)" }}>
                                  {formatDuration(
                                    r.startedAt,
                                    r.finishedAt,
                                    now,
                                  )}
                                </span>
                              </span>
                            </button>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ))}
                  {nextCursor && (
                    <button
                      type="button"
                      className="wf-summary-action"
                      style={{ ...ghostBtn, ...fullWidth }}
                      onClick={loadMore}
                      disabled={loadingMore}
                    >
                      {loadingMore ? "Loading…" : "Show older runs"}
                    </button>
                  )}
                </>
              )}
            </section>
          )}
        </main>
      </PullToRefresh>

      <style>{SUMMARY_CSS}</style>
      {trackedRunId && workflow && (
        <RunProgressDock
          steps={steps}
          logs={trackedDetail.logs}
          runStatus={trackedStatus}
          detailError={trackedDetailError}
          busy={acting}
          onDetails={() => {
            if (trackedRow) setSelected(trackedRow);
          }}
          onRunAgain={() => {
            setTrackedRunId(null);
            void run();
          }}
          onDismiss={() => setTrackedRunId(null)}
        />
      )}
      {selectedRun && (
        <RunSheet
          run={selectedRun}
          onClose={() => setSelected(null)}
          returnFocusTo={openerRef}
        />
      )}
    </div>
  );
}

// Press feedback and focus rings, which inline styles cannot express.
const SUMMARY_CSS = `
.wf-summary-row, .wf-summary-action {
  transition: background 0.12s var(--ease), transform 0.12s var(--ease);
}
.wf-summary-row:active, .wf-summary-action:active { transform: scale(0.98); }
.wf-summary-row:focus-visible, .wf-summary-action:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
@media (prefers-reduced-motion: reduce) {
  .wf-summary-row, .wf-summary-action { transition: none; }
  .wf-summary-row:active, .wf-summary-action:active { transform: none; }
}
`;

const page: React.CSSProperties = {
  maxWidth: 560,
  margin: "0 auto",
  padding: "16px 16px 40px",
};

const titleRow: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "var(--s-3)",
  flexWrap: "wrap",
};

const title: React.CSSProperties = {
  font: "600 20px/1.3 var(--font-sans)",
  color: "var(--fg)",
  margin: 0,
  minWidth: 0,
  overflowWrap: "anywhere",
};

const copy: React.CSSProperties = {
  font: "400 13px/1.6 var(--font-sans)",
  color: "var(--fg-muted)",
  maxWidth: "60ch",
  margin: 0,
};

const schedule: React.CSSProperties = {
  color: "var(--fg)",
};

const zoneLink: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  minHeight: 44,
  font: "500 13px/1 var(--font-sans)",
  color: "var(--accent)",
  textDecoration: "none",
};

const fullWidth: React.CSSProperties = {
  width: "100%",
  minHeight: 44,
  justifyContent: "center",
};

// Sentence case, like the Upcoming heading beside it on this screen.
const sectionLabel: React.CSSProperties = {
  margin: "0 0 8px",
  font: "600 13px/1.3 var(--font-sans)",
  color: "var(--fg)",
};

const dayLabel: React.CSSProperties = {
  margin: "0 0 6px",
  font: "500 12px/1 var(--font-sans)",
  color: "var(--fg-muted)",
};

const list: React.CSSProperties = {
  listStyle: "none",
  margin: 0,
  padding: 0,
  display: "grid",
  gap: "var(--s-2)",
};

const row: React.CSSProperties = {
  width: "100%",
  minHeight: 56,
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: "var(--s-4)",
  padding: "8px 12px",
  borderRadius: "var(--r-2)",
  border: "1px solid var(--border)",
  background: "var(--bg-elev-1)",
  color: "var(--fg)",
  textAlign: "left",
  cursor: "pointer",
};

const rowMain: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "var(--s-3)",
  minWidth: 0,
  flexWrap: "wrap",
};

const rowMeta: React.CSSProperties = {
  font: "400 12px/1.4 var(--font-sans)",
  color: "var(--fg-muted)",
};

const rowFigures: React.CSSProperties = {
  display: "grid",
  justifyItems: "end",
  gap: "var(--s-0)",
  flexShrink: 0,
  font: "500 12px/1.3 var(--font-mono)",
  fontVariantNumeric: "tabular-nums",
};
