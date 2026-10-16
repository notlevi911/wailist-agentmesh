"use client";
import {
  useState,
  useMemo,
  useEffect,
  useCallback,
  useRef,
  useSyncExternalStore,
} from "react";
import { useRouter } from "next/navigation";
import {
  Pill,
  Tag,
  IconSearch,
  IconGrid,
  Card,
  ghostBtnSm,
} from "@/components/ui";
import { Topbar } from "@/components/Topbar";
import { PullToRefresh } from "@/components/PullToRefresh";
import { WorkflowListSkeleton } from "@/components/ui/Skeleton";
import { Workflow } from "@/lib/types";
import { workflows as workflowsApi } from "@/lib/api";
import { useCredits } from "@/lib/credits/store";
import { usageHrefForWorkflow } from "@/lib/usageScope";
import { TENDRIL_WORKFLOW } from "@/lib/data";
import { loadTemplateWorkflow } from "@/lib/templateWorkflow";
import { can } from "@/lib/readonly";
import { workflowHref } from "@/lib/routes";
import { filterWorkflows, type StatusFilter } from "@/lib/workflowList";
import { ImportModal } from "./ImportModal";
import { ShareModal } from "./ShareModal";
import { WorkflowsPhoneList } from "./phone/WorkflowsPhoneList";
import { WorkflowOverview } from "./WorkflowOverview";
import { ghostBtn, primaryBtn } from "@/components/ui/buttons";
import { useReadOnly } from "@/hooks/useReadOnly";
import { useIsCompact } from "@/hooks/useIsCompact";
import { IS_NATIVE } from "@/lib/nativeAuth";
import { DesktopRowMenu } from "./DesktopRowMenu";
import { scheduleErrorText } from "@/lib/scheduleError";
import {
  cadenceToCron,
  cronToCadence,
  selectedDaysOfWeek,
  type Cadence,
  type CadenceValue,
} from "@/lib/cronCadence";

const subscribeToHydration = () => () => {};

export function WorkflowsPage() {
  const router = useRouter();
  const readOnly = useReadOnly();
  const hydrated = useSyncExternalStore(
    subscribeToHydration,
    () => true,
    () => false,
  );
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [view, setView] = useState<"rows" | "grid">("rows");
  const [wfList, setWfList] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [creatingTendril, setCreatingTendril] = useState(false);
  // Tagged by source so the banner always shows the most recent failure --
  // two separate error strings with a fixed `a || b` precedence would let
  // a stale error from one action permanently mask a newer one from the
  // other. A success only clears the error if it's the one that owns it,
  // so it never wipes an unrelated action's still-relevant error.
  const [pageError, setPageError] = useState<{
    source: "list" | "tendril" | "delete" | "schedule";
    message: string;
  } | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  // The workflow being shared -- also doubles as ShareModal's open flag
  // (non-null = open), the same "id doubles as open state" shape RowMenu's
  // own view state already uses.
  const [shareWorkflowId, setShareWorkflowId] = useState<string | null>(null);
  const { refreshBalance } = useCredits();

  // Extracted from the mount effect so pull-to-refresh can run the same fetch
  // rather than a second copy of it that could drift.
  //
  // The two callers want different recoveries from the same failure, which is
  // the one thing sharing the function must not flatten. On mount there is
  // nothing to lose, so an empty list is the honest result. On a refresh there
  // is a list already on screen, and emptying it turns a dropped request on a
  // phone network into "you have no workflows" -- worse than the refresh
  // simply not happening, because the user pulled expecting the list to be
  // updated, not removed.
  //
  // Either way the failure is now said out loud through the same tagged banner
  // the delete and schedule paths use, rather than being silently rendered as
  // an empty state. UsagePage's settlements fetch already keeps whatever
  // loaded last for the same reason (UsagePage.tsx:322).
  const reload = useCallback(
    (opts?: { keepOnError?: boolean }) =>
      workflowsApi
        .list()
        .then((rows) => {
          setWfList(rows);
          setPageError((prev) => (prev?.source === "list" ? null : prev));
        })
        .catch((e: unknown) => {
          if (!opts?.keepOnError) setWfList([]);
          setPageError({
            source: "list",
            message:
              e instanceof Error ? e.message : "could not load your workflows",
          });
        })
        .finally(() => setLoading(false)),
    [],
  );

  useEffect(() => {
    void reload();
  }, [reload]);

  // What the pull gesture runs. The balance goes with it: the two are read
  // together on mount for the same reason, and a refresh that updated the list
  // while leaving a stale figure above it would look like a bug.
  const refreshAll = useCallback(
    () => Promise.all([reload({ keepOnError: true }), refreshBalance()]),
    [reload, refreshBalance],
  );

  // Same authoritative balance the engine spends against, re-read on mount so
  // this page never shows a figure left over from before the last run.
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  const filtered = useMemo(
    () => filterWorkflows(wfList, { query: q, status }),
    [wfList, q, status],
  );

  const handleNewWorkflow = useCallback(async () => {
    if (creating) return;
    setCreating(true);
    try {
      const wf = await workflowsApi.create("Untitled workflow");
      router.push(workflowHref(wf.id));
    } catch {
      setCreating(false);
    }
  }, [creating, router]);

  // Loads TENDRIL_WORKFLOW (lib/data.ts) into a brand-new workflow row every
  // click -- a template is just a starting point the user immediately edits,
  // so there's no "the one shared copy" identity to preserve and a fresh
  // copy each time is correct. loadTemplateWorkflow (lib/templateWorkflow.ts)
  // owns the create()-then-update()-then-rollback-on-failure sequence,
  // shared with each partner ConsoleCard's "try a workflow" icon.
  const handleLoadTendrilWorkflow = useCallback(async () => {
    if (creatingTendril) return;
    setCreatingTendril(true);
    setPageError((prev) => (prev?.source === "tendril" ? null : prev));
    try {
      const id = await loadTemplateWorkflow(TENDRIL_WORKFLOW);
      router.push(workflowHref(id));
    } catch (e) {
      setPageError({
        source: "tendril",
        message:
          e instanceof Error ? e.message : "could not load demo workflow",
      });
      setCreatingTendril(false);
    }
  }, [creatingTendril, router]);

  // Deletion is permanent, so the row only calls this after its own in-menu
  // confirm step. The backend refuses (409) for workflows with Tendril lease
  // history; that message is shown rather than leaving the row silently intact.
  const handleDelete = useCallback(async (id: string) => {
    setPageError((prev) => (prev?.source === "delete" ? null : prev));
    try {
      await workflowsApi.remove(id);
      setWfList((prev) => prev.filter((w) => w.id !== id));
    } catch (e) {
      setPageError({
        source: "delete",
        message: e instanceof Error ? e.message : "could not delete workflow",
      });
    }
  }, []);

  // Schedule editors own their inline errors, so failures must not also
  // expose an unsanitized message in the page banner.
  const handleSetSchedule = useCallback(async (id: string, cron: string) => {
    const { cron: savedCron, nextRunAt } = await workflowsApi.setSchedule(id, cron);
    setWfList((prev) =>
      prev.map((w) =>
        w.id === id
          ? { ...w, scheduleCron: savedCron, scheduleNextRunAt: nextRunAt }
          : w,
      ),
    );
  }, []);

  const handleClearSchedule = useCallback(async (id: string) => {
    await workflowsApi.clearSchedule(id);
    setWfList((prev) =>
      prev.map((w) =>
        w.id === id
          ? { ...w, scheduleCron: undefined, scheduleNextRunAt: undefined }
          : w,
      ),
    );
  }, []);

  // A phone gets its own thin list: no create, import, schedule or share
  // actions (those are desktop-only), no view toggle, and the status tabs
  // folded into the filter menu. It shares the fetch and the pull above.
  if (readOnly) {
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
          <WorkflowsPhoneList
            workflows={wfList}
            loading={loading}
            error={pageError?.message ?? null}
            onRetry={() => void reload({ keepOnError: true })}
          />
        </PullToRefresh>
      </div>
    );
  }

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

      {/* Main. PullToRefresh owns the scrolling, because the gesture has to
          know the scroll position to tell a pull from an ordinary drag. On
          desktop it is a plain overflow container and adds no listeners. */}
      <PullToRefresh
        onRefresh={refreshAll}
        style={{ flex: 1, minHeight: 0, background: "var(--bg)" }}
      >
        <div
          style={{
            maxWidth: 1280,
            margin: "0 auto",
            padding: "var(--wf-page-pad)",
          }}
        >
          {/* Header */}
          <div className="wf-header" style={{ marginBottom: 28 }}>
            <div>
              <Tag>your workspace</Tag>
              <h1
                style={{
                  margin: "12px 0 4px",
                  fontSize: "var(--wf-h1)",
                  fontWeight: 500,
                  letterSpacing: "-0.025em",
                }}
              >
                Workflows
              </h1>
              <p style={{ margin: 0, color: "var(--fg-muted)", fontSize: 14 }}>
                Design, deploy, and monitor agent pipelines.
              </p>
            </div>
            <div
              className="wf-actions"
              data-readonly={readOnly}
              // The server cannot classify the device; reserve space until it can.
              style={{ visibility: hydrated ? undefined : "hidden" }}
            >
              {can("workflow.create", readOnly) && (
                <button style={ghostBtn} onClick={() => setImportOpen(true)}>
                  Import
                </button>
              )}
              {can("workflow.create", readOnly) && (
                <button
                  onClick={handleLoadTendrilWorkflow}
                  disabled={creatingTendril}
                  style={{
                    ...ghostBtn,
                    opacity: creatingTendril ? 0.6 : 1,
                    position: "relative",
                  }}
                  title="Rents a real Tendril machine for up to 15 minutes, probes its hardware, runs a multi-core benchmark on it, has a Gemini analyst write up the results, then releases the machine and refunds unused time. Tops up Tendril credit only when yours is short of the rent (at least $2 plus a $1.50 fee). If any step fails the machine is still released. $7.54 per run (rent gate $1.51, two jobs at $3.00 each, analyst $0.03) plus a few cents of metered machine time."
                >
                  {creatingTendril ? "Loading…" : "Run demo workflow"}
                  <span style={{ marginLeft: 6 }}>
                    <Pill tone="accent" mono>
                      $7.54+/run
                    </Pill>
                  </span>
                </button>
              )}
              {can("workflow.create", readOnly) && (
                <button
                  onClick={handleNewWorkflow}
                  disabled={creating}
                  style={{ ...primaryBtn, opacity: creating ? 0.6 : 1 }}
                >
                  {creating ? "Creating…" : "+ New workflow"}
                </button>
              )}
            </div>
          </div>

          <WorkflowOverview />

          {pageError && (
            <div
              style={{
                marginBottom: 16,
                padding: "10px 14px",
                borderRadius: "var(--r-2)",
                border: "1px solid var(--danger)",
                background: "var(--bg-elev-1)",
                color: "var(--danger)",
                fontSize: 12.5,
              }}
            >
              {pageError.message}
            </div>
          )}

          {/* Controls */}
          <div className="wf-controls" style={{ marginBottom: 12 }}>
            <div className="wf-search" style={{ position: "relative" }}>
              <span
                style={{
                  position: "absolute",
                  left: 12,
                  top: 12,
                  color: "var(--fg-dim)",
                }}
              >
                <IconSearch size={12} />
              </span>
              <input
                style={{
                  height: 36,
                  paddingLeft: 32,
                  paddingRight: 12,
                  width: "100%",
                  background: "var(--bg-elev-1)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-2)",
                  color: "var(--fg)",
                  fontFamily: "var(--font-sans)",
                  fontSize: 13,
                  outline: "none",
                }}
                placeholder="Search workflows, tags…"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
            <div
              style={{
                display: "flex",
                gap: 2,
                background: "var(--bg-elev-1)",
                padding: 3,
                borderRadius: "var(--r-2)",
                border: "1px solid var(--border)",
              }}
            >
              {(["all", "active", "paused", "draft"] as const).map((s) => (
                <button
                  key={s}
                  className="wf-filter"
                  onClick={() => setStatus(s)}
                  style={{
                    border: "none",
                    background:
                      status === s ? "var(--bg-elev-3)" : "transparent",
                    color: status === s ? "var(--fg)" : "var(--fg-muted)",
                    padding: "6px 12px",
                    fontSize: 12,
                    fontWeight: 500,
                    borderRadius: 5,
                    cursor: "pointer",
                    textTransform: "capitalize",
                    fontFamily: "var(--font-sans)",
                  }}
                >
                  {s}
                </button>
              ))}
            </div>
            <div style={{ flex: 1 }} />
            <div
              style={{
                display: "flex",
                gap: 2,
                background: "var(--bg-elev-1)",
                padding: 3,
                borderRadius: "var(--r-2)",
                border: "1px solid var(--border)",
              }}
            >
              <button
                className="wf-view-toggle"
                onClick={() => setView("rows")}
                style={{
                  ...ghostBtnSm,
                  height: 26,
                  background:
                    view === "rows" ? "var(--bg-elev-3)" : "transparent",
                  border: "none",
                }}
              >
                ☰ Rows
              </button>
              <button
                className="wf-view-toggle"
                onClick={() => setView("grid")}
                style={{
                  ...ghostBtnSm,
                  height: 26,
                  background:
                    view === "grid" ? "var(--bg-elev-3)" : "transparent",
                  border: "none",
                  display: "flex",
                  alignItems: "center",
                  gap: 4,
                }}
              >
                <IconGrid size={11} /> Grid
              </button>
            </div>
          </div>

          {/* List */}
          {loading ? (
            <WorkflowListSkeleton />
          ) : view === "rows" ? (
            <WorkflowRows
              items={filtered}
              onOpen={(id) => router.push(workflowHref(id))}
              onGeofence={(id) =>
                router.push(workflowHref(id, { geofence: true }))
              }
              onDelete={handleDelete}
              onSetSchedule={handleSetSchedule}
              onClearSchedule={handleClearSchedule}
              onShare={setShareWorkflowId}
              onViewUsage={(id) => router.push(usageHrefForWorkflow(id))}
            />
          ) : (
            <WorkflowGrid
              items={filtered}
              onOpen={(id) => router.push(workflowHref(id))}
            />
          )}

          {!loading && filtered.length === 0 && (
            <div
              style={{
                padding: 48,
                textAlign: "center",
                border: "1px dashed var(--border)",
                borderRadius: "var(--r-3)",
                color: "var(--fg-dim)",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
            >
              {wfList.length === 0
                ? "no workflows yet, create one to get started"
                : "no workflows match"}
            </div>
          )}
        </div>
      </PullToRefresh>
      {importOpen && (
        <ImportModal
          onClose={() => setImportOpen(false)}
          onImported={(id) => {
            setImportOpen(false);
            router.push(workflowHref(id));
          }}
        />
      )}
      {shareWorkflowId && (
        <ShareModal
          workflowId={shareWorkflowId}
          onClose={() => setShareWorkflowId(null)}
        />
      )}
    </div>
  );
}

function StatusBadge({ status }: { status?: string }) {
  const map: Record<
    string,
    { tone: "ok" | "warm" | "default" | "danger"; label: string }
  > = {
    active: { tone: "ok", label: "Active" },
    paused: { tone: "warm", label: "Paused" },
    draft: { tone: "default", label: "Draft" },
    // The real backend enum (models.WorkflowStatus) is draft/deployed/error --
    // "active"/"paused" above predate that and don't match anything the
    // backend actually stores. These two were missing entirely, so every
    // real deployed (or errored) workflow silently fell through to the
    // "draft" default and showed the wrong badge.
    deployed: { tone: "ok", label: "Deployed" },
    error: { tone: "danger", label: "Error" },
  };
  const s = map[status ?? "draft"] ?? map.draft;
  return (
    <Pill tone={s.tone} dot mono>
      {s.label}
    </Pill>
  );
}

function WorkflowIcon({ name }: { name: string }) {
  const seed = name.charCodeAt(0) + name.length;
  const dotCount = 3 + (seed % 3);
  const dots = Array.from({ length: dotCount }, (_, i) => {
    const a = (i / dotCount) * Math.PI * 2 + seed * 0.3;
    return { x: 14 + Math.cos(a) * 8, y: 14 + Math.sin(a) * 8 };
  });
  return (
    <div
      style={{
        width: 36,
        height: 36,
        borderRadius: 8,
        background: "var(--bg-elev-3)",
        border: "1px solid var(--border-strong)",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
      }}
    >
      <svg width="28" height="28" viewBox="0 0 28 28">
        {dots.map((d, i) =>
          dots.map((d2, j) =>
            j > i ? (
              <line
                key={`${i}_${j}`}
                x1={d.x}
                y1={d.y}
                x2={d2.x}
                y2={d2.y}
                stroke="var(--fg-dim)"
                strokeWidth="0.5"
              />
            ) : null,
          ),
        )}
        {dots.map((d, i) => (
          <circle
            key={i}
            cx={d.x}
            cy={d.y}
            r="2"
            fill={i === 0 ? "var(--accent)" : "var(--fg-muted)"}
          />
        ))}
      </svg>
    </div>
  );
}

// RowMenu is the ⋯ menu on a workflow row. Delete is permanent, so it asks for
// a second click ("Delete permanently?") in place rather than firing on the
// first — and rather than a browser confirm() dialog, which the rest of the app
// doesn't use. Compact viewports and the native shell only: desktop renders
// DesktopRowMenu instead (see WorkflowRows).
function RowMenu({
  workflowId,
  onDelete,
  onShare,
  deployed,
  scheduleCron,
  onSetSchedule,
  onClearSchedule,
}: {
  workflowId: string;
  /** Used by DesktopRowMenu; accepted here so the two are interchangeable. */
  workflowName?: string;
  onDelete: () => void;
  onShare: () => void;
  deployed: boolean;
  scheduleCron?: string;
  onSetSchedule: (cron: string) => Promise<void>;
  onClearSchedule: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [view, setView] = useState<"menu" | "schedule">("menu");
  // Re-read on open so edits made since list hydration are not overwritten.
  const [freshSchedule, setFreshSchedule] = useState<{
    cron?: string;
    nextRunAt?: string;
  } | null>(null);
  const [scheduleLoading, setScheduleLoading] = useState(false);
  const [scheduleFetchError, setScheduleFetchError] = useState<string | null>(
    null,
  );
  // The rows list scrolls horizontally (overflow-x: auto), which clips absolutely
  // positioned children in both axes — so the menu is position:fixed, anchored to
  // the button's viewport rect, and closes on scroll/resize rather than drifting.
  const [anchor, setAnchor] = useState<{
    top?: number;
    bottom?: number;
    right: number;
    maxHeight: number;
  } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  // Guards the schedule fetch below against a stale response landing after
  // the popover was closed/reopened (or this row was deleted) before it
  // resolved -- resetSchedule bumps this so a resolved-but-stale request's
  // setState calls are dropped rather than clobbering newer state or firing
  // after unmount.
  const scheduleFetchIdRef = useRef(0);

  const resetSchedule = useCallback(() => {
    scheduleFetchIdRef.current += 1;
    setFreshSchedule(null);
    setScheduleLoading(false);
    setScheduleFetchError(null);
  }, []);

  const fetchSchedule = useCallback(() => {
    setFreshSchedule(null);
    setScheduleFetchError(null);
    setScheduleLoading(true);
    // Bumped (not just read) on every open, not only by resetSchedule/
    // unmount: the "back" button returns to the menu view without calling
    // resetSchedule, so two Schedule opens in the same popover session
    // (open -> back -> open again) would otherwise capture the SAME
    // fetchId and a stale first response could still clobber the second
    // fetch's state. Bumping here guarantees every open gets an id no
    // earlier in-flight request can match.
    const fetchId = ++scheduleFetchIdRef.current;
    workflowsApi
      .get(workflowId)
      .then((wf) => {
        if (scheduleFetchIdRef.current !== fetchId) return;
        setFreshSchedule({
          cron: wf.scheduleCron,
          nextRunAt: wf.scheduleNextRunAt,
        });
      })
      .catch((e) => {
        if (scheduleFetchIdRef.current !== fetchId) return;
        setScheduleFetchError(
          scheduleErrorText(e, "load"),
        );
      })
      .finally(() => {
        if (scheduleFetchIdRef.current !== fetchId) return;
        setScheduleLoading(false);
      });
  }, [workflowId]);

  // Invalidate any in-flight fetch on unmount too (e.g. this row's workflow
  // was deleted while its schedule request was still pending).
  useEffect(() => {
    return () => {
      scheduleFetchIdRef.current += 1;
    };
  }, []);

  const close = useCallback(() => {
    setOpen(false);
    setConfirming(false);
    setView("menu");
    resetSchedule();
  }, [resetSchedule]);

  // Close on any click outside, so an open menu can't be left hanging over a
  // row the user has moved on from.
  useEffect(() => {
    if (!open) return;
    const onDocClick = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        close();
        btnRef.current?.focus({ preventScroll: true });
      }
    };
    const onScroll = (e: Event) => {
      if (e.target instanceof Node && ref.current?.contains(e.target)) return;
      close();
    };
    document.addEventListener("mousedown", onDocClick);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("mousedown", onDocClick);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", close);
    };
  }, [open, close]);

  return (
    <div
      ref={ref}
      // The row itself navigates on click; nothing inside this menu should.
      onClick={(e) => e.stopPropagation()}
    >
      <button
        ref={btnRef}
        aria-label="Workflow actions"
        aria-haspopup="menu"
        aria-expanded={open}
        onMouseDown={(e) => {
          if (e.button !== 0) return;
          // Focusing a clipped row button must not scroll and dismiss its menu.
          e.preventDefault();
          e.currentTarget.focus({ preventScroll: true });
        }}
        style={{
          ...ghostBtnSm,
          width: 28,
          padding: 0,
          justifyContent: "center",
          background: open ? "var(--bg-elev-3)" : undefined,
        }}
        onClick={() => {
          if (open) {
            close();
            return;
          }
          const rect = btnRef.current?.getBoundingClientRect();
          if (rect) {
            const above = rect.top > window.innerHeight / 2;
            setAnchor({
              ...(above
                ? { bottom: window.innerHeight - rect.top + 6 }
                : { top: rect.bottom + 6 }),
              right: Math.max(8, window.innerWidth - rect.right),
              maxHeight: Math.max(
                0,
                (above ? rect.top : window.innerHeight - rect.bottom) - 14,
              ),
            });
          }
          setConfirming(false);
          setView("menu");
          resetSchedule();
          setOpen(true);
        }}
      >
        ⋯
      </button>
      {open && anchor && (
        <div
          role="menu"
          style={{
            position: "fixed",
            top: anchor.top,
            bottom: anchor.bottom,
            right: anchor.right,
            maxHeight: anchor.maxHeight,
            overflowY: "auto",
            zIndex: 40,
            minWidth: 168,
            padding: 4,
            background: "var(--bg-elev-2)",
            border: "1px solid var(--border-strong)",
            borderRadius: "var(--r-2)",
            boxShadow: "0 12px 32px rgba(0,0,0,0.45)",
          }}
        >
          {view === "menu" && (
            <>
              <button
                role="menuitem"
                disabled={!deployed}
                title={deployed ? undefined : "Deploy this workflow first"}
                onClick={() => {
                  if (!deployed) return;
                  setView("schedule");
                  fetchSchedule();
                }}
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "8px 10px",
                  border: "none",
                  borderRadius: 5,
                  background: "transparent",
                  color: deployed ? "var(--fg)" : "var(--fg-dim)",
                  fontSize: 12.5,
                  fontWeight: 500,
                  fontFamily: "var(--font-sans)",
                  cursor: deployed ? "pointer" : "not-allowed",
                }}
                onMouseEnter={(e) => {
                  if (deployed)
                    e.currentTarget.style.background = "var(--bg-elev-3)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = "transparent";
                }}
              >
                {scheduleCron ? "Edit schedule" : "Schedule"}
              </button>
              <button
                role="menuitem"
                onClick={() => {
                  close();
                  onShare();
                }}
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "8px 10px",
                  border: "none",
                  borderRadius: 5,
                  background: "transparent",
                  color: "var(--fg)",
                  fontSize: 12.5,
                  fontWeight: 500,
                  fontFamily: "var(--font-sans)",
                  cursor: "pointer",
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = "var(--bg-elev-3)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = "transparent";
                }}
              >
                Share
              </button>
              <div
                style={{
                  height: 1,
                  background: "var(--border)",
                  margin: "4px 0",
                }}
              />
              <button
                role="menuitem"
                onClick={() => {
                  if (!confirming) {
                    setConfirming(true);
                    return;
                  }
                  close();
                  onDelete();
                }}
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "8px 10px",
                  border: "none",
                  borderRadius: 5,
                  background: confirming
                    ? "var(--danger-soft, transparent)"
                    : "transparent",
                  color: "var(--danger)",
                  fontSize: 12.5,
                  fontWeight: confirming ? 600 : 500,
                  fontFamily: "var(--font-sans)",
                  cursor: "pointer",
                }}
                onMouseEnter={(e) =>
                  (e.currentTarget.style.background = "var(--bg-elev-3)")
                }
                onMouseLeave={(e) =>
                  (e.currentTarget.style.background = confirming
                    ? "var(--danger-soft, transparent)"
                    : "transparent")
                }
              >
                {confirming ? "Delete permanently?" : "Delete workflow"}
              </button>
              {confirming && (
                <button
                  role="menuitem"
                  onClick={() => setConfirming(false)}
                  style={{
                    display: "block",
                    width: "100%",
                    textAlign: "left",
                    padding: "8px 10px",
                    border: "none",
                    borderRadius: 5,
                    background: "transparent",
                    color: "var(--fg-muted)",
                    fontSize: 12.5,
                    fontFamily: "var(--font-sans)",
                    cursor: "pointer",
                  }}
                >
                  Cancel
                </button>
              )}
            </>
          )}
          {view === "schedule" && scheduleLoading && (
            <div style={{ padding: 10, width: 220 }}>
              <button
                onClick={() => setView("menu")}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--fg-muted)",
                  cursor: "pointer",
                  fontSize: 11,
                  padding: 0,
                  marginBottom: 8,
                }}
              >
                ← back
              </button>
              <div style={{ fontSize: 11, color: "var(--fg-dim)" }}>
                Loading…
              </div>
            </div>
          )}
          {view === "schedule" && !scheduleLoading && scheduleFetchError && (
            // The list snapshot may be stale. Require a fresh read before
            // exposing a form that could overwrite the saved schedule.
            <div style={{ padding: 10, width: 220 }}>
              <button
                onClick={() => setView("menu")}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--fg-muted)",
                  cursor: "pointer",
                  fontSize: 11,
                  padding: 0,
                  marginBottom: 8,
                }}
              >
                ← back
              </button>
              <div
                style={{
                  fontSize: 11,
                  color: "var(--danger)",
                  marginBottom: 8,
                }}
              >
                Couldn&apos;t load the schedule: {scheduleFetchError}
              </div>
              <button
                onClick={fetchSchedule}
                style={{
                  ...ghostBtnSm,
                  width: "100%",
                  justifyContent: "center",
                }}
              >
                Retry
              </button>
            </div>
          )}
          {view === "schedule" && !scheduleLoading && !scheduleFetchError && (
            <SchedulePopover
              scheduleCron={freshSchedule?.cron}
              scheduleNextRunAt={freshSchedule?.nextRunAt}
              onBack={() => setView("menu")}
              onSave={async (cron) => {
                await onSetSchedule(cron);
                close();
              }}
              onRemove={async () => {
                await onClearSchedule();
                close();
              }}
            />
          )}
        </div>
      )}
    </div>
  );
}

// SchedulePopover renders inside RowMenu's existing anchored floating
// panel (view === "schedule") rather than opening a second popover, so
// there's one open/close/outside-click state machine, not two.
function SchedulePopover({
  scheduleCron,
  scheduleNextRunAt,
  onBack,
  onSave,
  onRemove,
}: {
  scheduleCron?: string;
  scheduleNextRunAt?: string;
  onBack: () => void;
  onSave: (cron: string) => Promise<void>;
  onRemove: () => Promise<void>;
}) {
  const storedValue = useMemo(
    () => (scheduleCron ? cronToCadence(scheduleCron) : null),
    [scheduleCron],
  );
  const custom = !!scheduleCron && !storedValue;
  const initial: CadenceValue = storedValue ?? {
    cadence: "daily",
    time: "09:00",
    dayOfWeek: 1,
    dayOfMonth: 1,
  };
  const [cadence, setCadence] = useState<Cadence>(initial.cadence);
  const [time, setTime] = useState(initial.time);
  const [daysOfWeek, setDaysOfWeek] = useState(() => selectedDaysOfWeek(initial));
  const [dayOfMonth, setDayOfMonth] = useState(initial.dayOfMonth ?? 1);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [replacingCustom, setReplacingCustom] = useState(false);
  const editing = !custom || replacingCustom;
  // Click-to-arm, click-again-to-confirm -- same pattern as RowMenu's
  // delete-workflow button, since Remove here is just as irreversible
  // (deletes the live cron schedule) and shouldn't fire on a single
  // misclick the way it did before.
  const [removeConfirming, setRemoveConfirming] = useState(false);

  const DOW_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const selectStyle: React.CSSProperties = {
    width: "100%",
    padding: "5px 6px",
    marginBottom: 8,
    fontSize: 12,
    fontFamily: "var(--font-mono)",
    background: "var(--bg-elev-1)",
    border: "1px solid var(--border)",
    borderRadius: 4,
    color: "var(--fg)",
  };
  const labelStyle: React.CSSProperties = {
    display: "block",
    fontSize: 10,
    color: "var(--fg-dim)",
    marginBottom: 3,
  };

  return (
    <div style={{ padding: 10, width: 220 }}>
      <button
        onClick={onBack}
        disabled={saving}
        style={{
          background: "none",
          border: "none",
          color: "var(--fg-muted)",
          cursor: "pointer",
          fontSize: 11,
          padding: 0,
          marginBottom: 8,
        }}
      >
        ← back
      </button>
      {custom && (
        <div role="status" style={{ fontSize: 12, lineHeight: 1.5, marginBottom: 10 }}>
          This workflow has a custom schedule. It stays unchanged unless you
          choose to replace or remove it.
          {!editing && (
            <button
              type="button"
              style={{ ...ghostBtnSm, marginTop: 8 }}
              onClick={() => setReplacingCustom(true)}
            >
              Replace schedule
            </button>
          )}
        </div>
      )}
      {editing && <fieldset disabled={saving} style={{ margin: 0, padding: 0, border: 0, minWidth: 0 }}><div style={{ display: "flex", gap: 4, marginBottom: 8 }}>
        {(["daily", "weekly", "monthly"] as Cadence[]).map((c) => (
          <button
            key={c}
            aria-pressed={cadence === c}
            onClick={() => setCadence(c)}
            style={{
              flex: 1,
              padding: "5px 0",
              fontSize: 11,
              fontFamily: "var(--font-sans)",
              borderRadius: 4,
              border: "1px solid var(--border)",
              background: cadence === c ? "var(--accent-soft)" : "transparent",
              color: cadence === c ? "var(--accent)" : "var(--fg-muted)",
              cursor: "pointer",
              textTransform: "capitalize",
            }}
          >
            {c}
          </button>
        ))}
      </div>
      <label style={labelStyle}>Time (your timezone)</label>
      <input
        type="time"
        aria-label="Time (your timezone)"
        value={time}
        onChange={(e) => setTime(e.target.value)}
        style={{
          width: "100%",
          padding: "5px 6px",
          marginBottom: 8,
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          background: "var(--bg-elev-1)",
          border: "1px solid var(--border)",
          borderRadius: 4,
          color: "var(--fg)",
        }}
      />
      <div style={{ fontSize: 11, color: "var(--fg-dim)", marginBottom: 8 }}>
        Stored in UTC. May shift by an hour across daylight saving.
      </div>
      {cadence === "weekly" && (
        <fieldset style={{ margin: "0 0 8px", padding: 0, border: 0 }}>
          <legend style={labelStyle}>Days of week</legend>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 4 }}>
            {DOW_LABELS.map((label, day) => {
              const selected = daysOfWeek.includes(day);
              return (
                <button
                  key={day}
                  type="button"
                  aria-pressed={selected}
                  aria-disabled={selected && daysOfWeek.length === 1}
                  onClick={() => setDaysOfWeek((days) =>
                    days.includes(day)
                      ? days.length === 1 ? days : days.filter((d) => d !== day)
                      : [...days, day].sort((a, b) => a - b),
                  )}
                  style={{
                    padding: "7px 0",
                    fontSize: 11,
                    fontFamily: "var(--font-sans)",
                    borderRadius: 4,
                    border: "1px solid var(--border)",
                    background: selected ? "var(--accent-soft)" : "transparent",
                    color: selected ? "var(--accent)" : "var(--fg-muted)",
                    cursor: "pointer",
                  }}
                >
                  {label}
                </button>
              );
            })}
          </div>
          <div style={{ fontSize: 10, color: "var(--fg-dim)", marginTop: 4 }}>
            Select one or more days.
          </div>
        </fieldset>
      )}
      {cadence === "monthly" && (
        <>
          <label style={labelStyle}>Day of month</label>
          <select
            aria-label="Day of month"
            value={dayOfMonth}
            onChange={(e) => setDayOfMonth(Number(e.target.value))}
            style={selectStyle}
          >
            {Array.from({ length: 28 }, (_, i) => i + 1).map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </>
      )}
      </fieldset>}
      {scheduleNextRunAt && (
        <div style={{ fontSize: 10, color: "var(--fg-dim)", marginBottom: 8 }}>
          Next run: {new Date(scheduleNextRunAt).toLocaleString()}
        </div>
      )}
      {error && (
        <div
          role="alert"
          style={{ fontSize: 10.5, color: "var(--danger)", marginBottom: 8 }}
        >
          {error}
        </div>
      )}
      <div style={{ display: "flex", gap: 6 }}>
        <button
          disabled={saving || !editing || !time}
          onClick={async () => {
            if (saving || !editing || !time) return;
            setSaving(true);
            setError(null);
            try {
              await onSave(
                cadenceToCron({ cadence, time, daysOfWeek, dayOfMonth }),
              );
            } catch (e) {
              setError(scheduleErrorText(e));
              setSaving(false);
            }
          }}
          style={{
            flex: 1,
            padding: "6px 0",
            fontSize: 11.5,
            fontWeight: 600,
            borderRadius: 4,
            border: "none",
            background: "var(--accent)",
            color: "var(--bg)",
            cursor: saving || !editing || !time ? "default" : "pointer",
            opacity: saving || !editing || !time ? 0.6 : 1,
          }}
        >
          Save
        </button>
        {scheduleCron && (
          <button
            disabled={saving}
            onClick={async () => {
              if (!removeConfirming) {
                setRemoveConfirming(true);
                return;
              }
              setSaving(true);
              setError(null);
              try {
                await onRemove();
              } catch (e) {
                setError(scheduleErrorText(e, "remove"));
                setSaving(false);
                setRemoveConfirming(false);
              }
            }}
            onBlur={() => setRemoveConfirming(false)}
            style={{
              padding: "6px 10px",
              fontSize: 11.5,
              borderRadius: 4,
              border: "1px solid var(--border-strong)",
              background: removeConfirming
                ? "var(--danger-soft, transparent)"
                : "transparent",
              color: "var(--danger)",
              fontWeight: removeConfirming ? 600 : 500,
              cursor: saving ? "default" : "pointer",
            }}
          >
            {removeConfirming ? "Remove permanently?" : "Remove"}
          </button>
        )}
      </div>
    </div>
  );
}

function WorkflowRows({
  items,
  onOpen,
  onGeofence,
  onDelete,
  onSetSchedule,
  onClearSchedule,
  onShare,
  onViewUsage,
}: {
  items: Workflow[];
  onOpen: (id: string) => void;
  onGeofence: (id: string) => void;
  onDelete: (id: string) => void;
  onSetSchedule: (id: string, cron: string) => Promise<void>;
  onClearSchedule: (id: string) => Promise<void>;
  onShare: (id: string) => void;
  onViewUsage: (id: string) => void;
}) {
  const readOnly = useReadOnly();
  // Desktop gets the shadcn/ui menu (DesktopRowMenu); compact viewports and
  // the native shell use RowMenu below, subject to the device policy.
  const desktop = !useIsCompact() && !IS_NATIVE;
  const Menu = desktop ? DesktopRowMenu : RowMenu;
  return (
    <Card style={{ padding: 0, overflowX: "auto" }}>
      <div
        className="hide-md"
        style={{
          display: "grid",
          gridTemplateColumns: "var(--wf-row-cols)",
          gap: 12,
          padding: "10px 16px",
          background: "var(--bg-elev-2)",
          borderBottom: "1px solid var(--border)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          color: "var(--fg-dim)",
        }}
      >
        <span>Name</span>
        <span>Status</span>
        <span>Agents</span>
        <span>Runs · 30d</span>
        <span>Spend · 30d</span>
        <span>Updated</span>
        <span></span>
      </div>
      {items.map((wf, i) => (
        <div
          key={wf.id}
          className="wf-row"
          onClick={() => onOpen(wf.id)}
          style={{
            display: "grid",
            gridTemplateColumns: "var(--wf-row-cols)",
            // Tokenised for the same reason as the padding: an inline value
            // beats the stylesheet, so a media query could never have reached
            // it. There are five gaps per card on a phone.
            // Fallbacks are not decoration. `var(--x)` with no fallback and no
            // definition resolves to nothing, and `padding: <nothing>` collapses to
            // ZERO -- the card goes from spacious to clamped with no error anywhere.
            // The desktop values are the fallback, so the worst case is desktop
            // spacing on a phone rather than none at all.
            gap: "var(--wf-row-gap, 12px)",
            padding: "var(--wf-row-pad, 14px 16px)",
            alignItems: "center",
            borderBottom:
              i < items.length - 1 ? "1px solid var(--border-soft)" : "none",
            cursor: "pointer",
            transition: "background .12s",
          }}
          // Mouse only: a tap fires the enter event too, and the highlight
          // then stays on the row after the finger lifts.
          onPointerEnter={(e) => {
            if (e.pointerType === "mouse")
              e.currentTarget.style.background = "var(--bg-elev-2)";
          }}
          onMouseLeave={(e) =>
            (e.currentTarget.style.background = "transparent")
          }
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 12,
              minWidth: 0,
            }}
          >
            <WorkflowIcon name={wf.name ?? ""} />
            <div style={{ minWidth: 0 }}>
              <div
                style={{
                  fontSize: 14,
                  fontWeight: 500,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {wf.name}
              </div>
              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  gap: "2px 5px",
                  marginTop: 4,
                }}
              >
                {wf.tags?.map((t) => (
                  <span
                    key={t}
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      color: "var(--fg-dim)",
                      textTransform: "uppercase",
                      letterSpacing: "0.06em",
                    }}
                  >
                    #{t}
                  </span>
                ))}
              </div>
            </div>
          </div>
          <span data-label="Status" style={{ display: "inline-flex" }}>
            <StatusBadge status={wf.status} />
          </span>
          <span
            data-label="Agents"
            style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}
          >
            {wf.agents ??
              wf.nodes?.filter((n) => n.type === "agent").length ??
              0}
          </span>
          <span
            data-label="Runs · 30d"
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: "var(--fg-muted)",
            }}
          >
            {wf.runs?.toLocaleString() ?? "-"}
          </span>
          <span
            data-label="Spend · 30d"
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: "var(--accent)",
            }}
          >
            {wf.spend ? `$${wf.spend}` : "-"}
          </span>
          <span
            data-label="Updated"
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--fg-muted)",
            }}
          >
            {fmtDate(wf.updatedAt ?? wf.updated)}
          </span>
          <div className="wf-row-actions" style={{ display: "flex", gap: 4 }}>
            <button
              style={ghostBtnSm}
              onClick={(e) => {
                e.stopPropagation();
                onOpen(wf.id);
              }}
            >
              Open
            </button>
            {/* Usage stays independent of the delete-gated RowMenu because it
                is read-only. The phone list exposes the same action from its
                compact row (see phone/WorkflowPhoneRow.tsx). */}
            <button
              style={ghostBtnSm}
              onClick={(e) => {
                e.stopPropagation();
                onViewUsage(wf.id);
              }}
              title="Usage scoped to this workflow"
            >
              Usage
            </button>
            {/* Its own button rather than an item in RowMenu below, because
                that menu is gated on "workflow.delete" and so never appears on
                a phone -- which is the one device this screen is for. Shown
                only for a deployed workflow: SetGeofence answers 409 until
                then, and an entry point that always fails is worse than none.
                The capability, not the device, decides -- see lib/readonly.ts. */}
            {can("workflow.geofence", readOnly) && wf.status === "deployed" && (
              <button
                style={ghostBtnSm}
                onClick={(e) => {
                  e.stopPropagation();
                  onGeofence(wf.id);
                }}
                title={
                  wf.geofenceRadiusM !== undefined
                    ? "Location trigger is set"
                    : "Set a location trigger"
                }
              >
                {wf.geofenceRadiusM !== undefined ? "Zone ·" : "Zone"}
              </button>
            )}
            {can("workflow.delete", readOnly) && (
              <Menu
                workflowId={wf.id}
                workflowName={wf.name}
                onDelete={() => onDelete(wf.id)}
                onShare={() => onShare(wf.id)}
                deployed={wf.status === "deployed"}
                scheduleCron={wf.scheduleCron}
                onSetSchedule={(cron) => onSetSchedule(wf.id, cron)}
                onClearSchedule={() => onClearSchedule(wf.id)}
              />
            )}
          </div>
        </div>
      ))}
    </Card>
  );
}

function WorkflowGrid({
  items,
  onOpen,
}: {
  items: Workflow[];
  onOpen: (id: string) => void;
}) {
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "var(--wf-card-cols)",
        gap: 16,
      }}
    >
      {items.map((wf) => (
        <Card
          key={wf.id}
          onClick={() => onOpen(wf.id)}
          style={{
            cursor: "pointer",
            transition: "border-color .15s, transform .15s",
          }}
          // Mouse only, as on the row above: after a tap the card would stay
          // lifted.
          onPointerEnter={(e) => {
            if (e.pointerType !== "mouse") return;
            (e.currentTarget as HTMLElement).style.borderColor =
              "var(--border-strong)";
            (e.currentTarget as HTMLElement).style.transform =
              "translateY(-2px)";
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLElement).style.borderColor =
              "var(--border)";
            (e.currentTarget as HTMLElement).style.transform = "translateY(0)";
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <WorkflowIcon name={wf.name ?? ""} />
            <StatusBadge status={wf.status} />
          </div>
          <div
            style={{
              marginTop: 16,
              fontSize: 16,
              fontWeight: 500,
              letterSpacing: "-0.015em",
            }}
          >
            {wf.name}
          </div>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: "2px 6px",
              marginTop: 6,
            }}
          >
            {wf.tags?.map((t) => (
              <span
                key={t}
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  color: "var(--fg-dim)",
                  textTransform: "uppercase",
                  letterSpacing: "0.06em",
                }}
              >
                #{t}
              </span>
            ))}
          </div>
          <div
            style={{
              marginTop: 16,
              paddingTop: 12,
              borderTop: "1px solid var(--border-soft)",
              display: "grid",
              gridTemplateColumns: "var(--wf-cardmeta-cols)",
              gap: 8,
              fontFamily: "var(--font-mono)",
              fontSize: 11,
            }}
          >
            {[
              {
                label: "Agents",
                val: String(
                  wf.agents ??
                    wf.nodes?.filter((n) => n.type === "agent").length ??
                    0,
                ),
              },
              { label: "Runs", val: wf.runs?.toLocaleString() ?? "-" },
              { label: "Spend", val: wf.spend ?? "-", accent: true },
            ].map((s) => (
              <div key={s.label}>
                <div
                  style={{
                    color: "var(--fg-dim)",
                    fontSize: 11,
                    textTransform: "uppercase",
                    letterSpacing: "0.06em",
                  }}
                >
                  {s.label}
                </div>
                <div
                  style={{
                    color: s.accent ? "var(--accent)" : "var(--fg)",
                    marginTop: 2,
                  }}
                >
                  {s.val}
                </div>
              </div>
            ))}
          </div>
        </Card>
      ))}
    </div>
  );
}

function fmtDate(iso?: string): string {
  if (!iso) return "-";
  try {
    return new Intl.DateTimeFormat("en", {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

// Shared styles
