"use client";
import { useState, useMemo, useEffect, useCallback, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { WorkflowNode, Workflow } from "@/lib/types";
import { decodePendingNode } from "@/lib/bazaar";
import {
  Toast,
  Logo,
  Pill,
  Hairline,
  IconPlay,
  IconStop,
} from "@/components/ui";
import { workflows as workflowsApi, runs as runsApi } from "@/lib/api";
import {
  useCredits,
  refreshBalance as refreshCredits,
} from "@/lib/credits/store";
import { LOW_BALANCE_THRESHOLD_USD } from "@/lib/credits/fx";
import { CanvasGraph } from "./CanvasGraph";
import { PalettePanel } from "./PalettePanel";
import { Inspector } from "./Inspector";
import { ConsolePanel } from "./ConsolePanel";
import { ResizeHandle } from "./ResizeHandle";
import { ChatRail } from "./chat/ChatRail";
import { useChatConsole, type ChatConsole } from "./chat/useChatConsole";
import { can } from "@/lib/readonly";
import { ghostBtnSm, primaryBtnSm } from "@/components/ui/buttons";
import { useIsCompact } from "@/hooks/useIsCompact";
import { runBlockedMessage } from "./runBlocked";
import { useReadOnly } from "@/hooks/useReadOnly";
import {
  PALETTE,
  INSPECTOR,
  clampWidth,
  loadWidths,
  saveWidths,
} from "./panelSizing";

interface CanvasPageProps {
  workflowId: string;
}

export function CanvasPage({ workflowId }: CanvasPageProps) {
  const router = useRouter();
  const compact = useIsCompact();
  const readOnly = useReadOnly();
  // Whether the narrow-screen wall applies at all. It is about EDITING, not
  // width: dragging nodes really does not work at 375px, so an editor on a
  // narrow window is told so. A viewer has nothing to drag -- viewing is the
  // entire reason the native shell exists -- and gets the touch canvas below.
  const canEdit = can("workflow.editGraph", readOnly);

  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [logOpen, setLogOpen] = useState(false);
  const [manualBuildMode, setManualBuildMode] = useState(false);
  // Below the compact breakpoint the studio stacks instead of sitting in
  // three columns, and the rail becomes a sheet. Closed by default: the
  // reader came to look at the graph, so the graph gets the screen until
  // they ask for something else.
  const [sheetOpen, setSheetOpen] = useState(false);
  // Filled in by CanvasGraph. Tap-to-add has no cursor position to place
  // against, and the centre of the current view is only known down there.
  const addAtCentre = useRef<((meta: Partial<WorkflowNode>) => void) | null>(
    null,
  );
  const [deployed, setDeployed] = useState(false);
  const [running, setRunning] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [saveLabel, setSaveLabel] = useState("");
  const [runId, setRunId] = useState<string | null>(null);
  const [resumeAttempt, setResumeAttempt] = useState(0);
  // A single boolean, not a counter: it can only suppress ONE upcoming
  // autosave cycle at a time, regardless of which of the two trusted writes
  // below (initial load, Bazaar auto-add) set it. Both origins collapse into
  // this one "skip next autosave" signal deliberately — there's no current
  // need to distinguish them or to suppress more than one cycle in a row. A
  // future feature needing genuine "was this the initial page load" or
  // "skip N cycles" semantics needs its own mechanism, not a reuse of this
  // one.
  const skipNextAutosave = useRef(true);

  // Below the breakpoint the editor can't be operated (drag-and-drop canvas,
  // mouse-resizable panels), so it must not be mounted at all there -- not just
  // visually covered by the notice, which would leave the graph, its data fetch
  // and the chat/SSE host running under a screen the reader is told plainly they
  // can't use. Starts false (matching what the server -- and the client's own
  // hydration pass -- both render with no way to know the viewport yet) and is
  // corrected in the mount effect below, same as the panel widths above.
  // Reading matchMedia here directly would disagree with the server on the
  // very first client render and trip a hydration mismatch.
  const [isNarrow, setIsNarrow] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 768px)");
    const update = () => setIsNarrow(mq.matches);
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);

  // -- Resizable side panels ------------------------------------------------
  // Widths start at defaults (so SSR and the first client render match), then a
  // mount effect loads any persisted values. The row is measured via a ref so
  // clamping can reserve MIN_CANVAS and the opposite panel's width.
  const [paletteW, setPaletteW] = useState(PALETTE.default);
  const [paletteCollapsed, setPaletteCollapsed] = useState(false);
  const [inspectorW, setInspectorW] = useState(INSPECTOR.default);
  const panelRowRef = useRef<HTMLDivElement | null>(null);
  const rowObserver = useRef<ResizeObserver | null>(null);
  // Latest widths for the async ResizeObserver callback (avoids stale closures).
  const widthsRef = useRef({ paletteW, inspectorW });
  useEffect(() => {
    widthsRef.current = { paletteW, inspectorW };
  }, [paletteW, inspectorW]);

  const rowWidth = () =>
    panelRowRef.current?.getBoundingClientRect().width ?? 0;

  // Callback ref: the panel row is gated behind the loading screen, so a mount
  // effect would run before it exists. Attaching the ResizeObserver here starts
  // observation exactly when the node mounts. Persisted widths are seeded before
  // observing, so the observer's first (async) callback clamps and applies them
  // -- keeping the canvas above MIN_CANVAS and all setState off the SSR path.
  const attachRow = useCallback((el: HTMLDivElement | null) => {
    rowObserver.current?.disconnect();
    rowObserver.current = null;
    panelRowRef.current = el;
    if (!el) return;
    const saved = loadWidths();
    if (saved) widthsRef.current = saved;
    const reflow = () => {
      const cw = el.getBoundingClientRect().width;
      if (cw <= 0) return;
      const { paletteW: pw, inspectorW: iw } = widthsRef.current;
      setPaletteW(clampWidth(pw, PALETTE, cw, iw));
      setInspectorW(clampWidth(iw, INSPECTOR, cw, pw));
    };
    // Clamp immediately (getBoundingClientRect forces layout) so the initial
    // fit never depends on the observer's async first delivery, then observe
    // for subsequent window resizes.
    reflow();
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(reflow);
      ro.observe(el);
      rowObserver.current = ro;
    }
  }, []);

  const resizePalette = useCallback((req: number) => {
    setPaletteW(
      clampWidth(req, PALETTE, rowWidth(), widthsRef.current.inspectorW),
    );
  }, []);
  const resizeInspector = useCallback((req: number) => {
    setInspectorW(
      clampWidth(req, INSPECTOR, rowWidth(), widthsRef.current.paletteW),
    );
  }, []);
  const persistWidths = useCallback(() => {
    saveWidths(widthsRef.current);
  }, []);

  // No state resets here: the route passes key={workflowId}, so navigating to
  // a different workflow remounts this component and every piece of state
  // returns to its initial value (loading=true, selectedId=null, …).
  useEffect(() => {
    // Skip only when the wall below will actually show. A viewer renders a
    // real canvas on a narrow screen, so it needs the workflow fetched.
    if (isNarrow && canEdit) return;

    // Guards against a stale response overwriting fresher state: React 18
    // Strict Mode double-invokes this effect in dev (mount → cleanup →
    // remount), firing two real GETs with nothing to cancel the first. If
    // the first (meant to be discarded) resolves AFTER something else has
    // already changed `workflow` in between — e.g. a Bazaar-added node —
    // its unconditional setWorkflow silently wipes that change out. The
    // same race can happen for real (not just in dev) if workflowId changes
    // quickly. Confirmed live: this is exactly what made a Bazaar-added
    // node "appear then vanish" a second later.
    let cancelled = false;
    if (workflowId === "new") {
      // Nothing to create here in read-only mode -- send the visitor back to
      // the list rather than letting the route sit on "creating workflow…".
      if (!can("workflow.create", readOnly)) {
        router.replace("/workflows");
        return;
      }
      workflowsApi
        .create("Untitled workflow")
        .then((wf) => {
          if (!cancelled) router.replace(`/workflows/${wf.id}`);
        })
        .catch(() => {
          if (!cancelled) setLoading(false);
        });
      return () => {
        cancelled = true;
      };
    }

    workflowsApi
      .get(workflowId)
      .then((wf) => {
        if (cancelled) return;
        skipNextAutosave.current = true;
        setWorkflow(wf);
        // Deployment state comes from the workflow's own status. It used to
        // be inferred from an agent node having a wallet address, which no
        // longer exists now that agents are funded by the platform wallets.
        if (wf.status === "deployed") {
          setDeployed(true);
        }
        setLoading(false);
      })
      .catch(() => {
        if (!cancelled) router.push("/workflows");
      });
    return () => {
      cancelled = true;
    };
  }, [workflowId, router, isNarrow, readOnly, canEdit]);

  // Auto-save: debounce 1.5s after any change, skip on initial load.
  // pendingSave holds the graph the debounce timer is still sitting on, and
  // inFlightSave the request already on the wire, so flushPendingSave can
  // force both to land before something else reads the graph server-side.
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingSave = useRef<Workflow | null>(null);
  const inFlightSave = useRef<Promise<void> | null>(null);

  const saveWorkflow = useCallback((wf: Workflow) => {
    const p = workflowsApi
      .update(wf.id, { name: wf.name, nodes: wf.nodes, edges: wf.edges })
      .then(() => {
        const now = new Date();
        setSaveLabel(
          `saved · ${now.getHours()}:${String(now.getMinutes()).padStart(2, "0")}`,
        );
      })
      .catch(() => setSaveLabel("save failed"))
      .finally(() => {
        if (inFlightSave.current === p) inFlightSave.current = null;
      });
    inFlightSave.current = p;
    return p;
  }, []);

  // Settles whatever the autosave still owes the server. Anything that makes
  // the backend re-read the graph from the DB (build mode) must await this
  // first, or it edits a stale copy and its response overwrites the newer
  // client state -- silently reverting the edit that was mid-debounce.
  const flushPendingSave = useCallback(async () => {
    if (saveTimer.current !== null) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    const pending = pendingSave.current;
    pendingSave.current = null;
    await inFlightSave.current;
    if (pending) await saveWorkflow(pending);
  }, [saveWorkflow]);

  useEffect(() => {
    if (!workflow) return;
    // Never arm the autosave in read-only mode. Every editing control is
    // gone, but this effect answers to any change in `workflow` at all --
    // left armed, one stray state update would PUT the graph back.
    if (!can("workflow.editGraph", readOnly)) return;
    if (skipNextAutosave.current) {
      skipNextAutosave.current = false;
      return;
    }
    setSaveLabel("saving…");
    pendingSave.current = workflow;
    const t = setTimeout(() => {
      saveTimer.current = null;
      pendingSave.current = null;
      void saveWorkflow(workflow);
    }, 1500);
    saveTimer.current = t;
    return () => clearTimeout(t);
  }, [workflow, saveWorkflow, readOnly]);

  const selected = useMemo(
    () => workflow?.nodes.find((n) => n.id === selectedId) ?? null,
    [workflow, selectedId],
  );

  const attachedSummaries = useMemo(() => {
    const out: Record<string, { model: string | null; tools: number }> = {};
    if (!workflow) return out;
    for (const n of workflow.nodes) {
      if (n.type !== "agent") continue;
      let modelName: string | null = null;
      let toolsCount = 0;
      for (const e of workflow.edges) {
        if (e.kind !== "attach" || e.to !== n.id) continue;
        const src = workflow.nodes.find((x) => x.id === e.from);
        if (!src) continue;
        if (e.toPort === "model" && src.type === "provider")
          modelName = src.name ?? null;
        if (
          e.toPort === "tools" &&
          (src.type === "tool" || src.type === "tool402")
        )
          toolsCount++;
      }
      out[n.id] = { model: modelName, tools: toolsCount };
    }
    return out;
  }, [workflow]);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(null), 2400);
  }, []);

  const handleResume = useCallback(
    async (deadLetterRunId: string) => {
      // A dead-letter row can be restored from a cached transcript (see
      // useRunTranscript's CachedRun.deadLetters) with no live runId in this
      // session -- `runId` state is reset to null on every page load and
      // only ever set by starting a fresh run. deadLetterRunId (the row's
      // own runId, always populated by the backend) is the fallback so
      // resuming after a reload actually resumes the right run instead of
      // silently no-opping while the button still looks clickable.
      const targetRunId = runId ?? deadLetterRunId;
      if (!targetRunId) {
        showToast("Nothing to resume — start the workflow again to retry it.");
        return;
      }
      try {
        await runsApi.resume(targetRunId);
        setRunId(targetRunId);
        setRunning(true);
        setResumeAttempt((a) => a + 1);
        showToast("Run resumed");
      } catch (err) {
        showToast(
          `Resume failed · ${err instanceof Error ? err.message : "unknown error"}`,
        );
      }
    },
    [runId, showToast],
  );

  const onUpdate = useCallback((n: WorkflowNode) => {
    setWorkflow((wf) =>
      wf ? { ...wf, nodes: wf.nodes.map((x) => (x.id === n.id ? n : x)) } : wf,
    );
  }, []);

  // Guarded here rather than at the call sites. This is the single node-
  // deletion path -- the Delete/Backspace handler below and the Inspector
  // both route through it -- so the gate belongs on the mutation itself,
  // not on each caller. A viewer can reach it because selecting a node is
  // allowed (that is how the read-only inspector opens); only the removal
  // is withheld.
  const onDelete = useCallback(() => {
    if (!can("workflow.editGraph", readOnly)) return;
    if (!selectedId) return;
    setWorkflow((wf) =>
      wf
        ? {
            ...wf,
            nodes: wf.nodes.filter((n) => n.id !== selectedId),
            edges: wf.edges.filter(
              (e) => e.from !== selectedId && e.to !== selectedId,
            ),
          }
        : wf,
    );
    setSelectedId(null);
  }, [selectedId, readOnly]);

  // Delete/Backspace removes the selected node -- ignored while typing in a field.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      if (!selectedId) return;
      const el = document.activeElement as HTMLElement | null;
      if (
        el &&
        (el.tagName === "INPUT" ||
          el.tagName === "TEXTAREA" ||
          el.isContentEditable)
      )
        return;
      e.preventDefault();
      onDelete();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [selectedId, onDelete]);

  const onDeploy = useCallback(async () => {
    if (!workflow) return;
    if (deployed) {
      showToast("Re-deployed");
      return;
    }
    try {
      const res = await workflowsApi.deploy(workflow.id);
      setDeployed(true);
      showToast(
        `Deployed · ${res.agents.length} agent${res.agents.length !== 1 ? "s" : ""} ready · paid calls draw from your credits`,
      );
    } catch (err: unknown) {
      showToast(
        `Deploy failed · ${err instanceof Error ? err.message : "unknown error"}`,
      );
    }
  }, [deployed, workflow, showToast]);

  const hasChatTrigger = useMemo(
    () =>
      workflow?.nodes.some(
        (n) => n.type === "trigger" && n.template === "chat",
      ) ?? false,
    [workflow],
  );

  // No provider node yet means there is nothing to run -- chat always
  // builds in that state. Once one exists, the Build/Run pill decides.
  const hasProviderNode = useMemo(
    () => workflow?.nodes.some((n) => n.type === "provider") ?? false,
    [workflow],
  );

  // Null when a run can proceed. Naming the real obstacle matters most to a
  // viewer, who cannot deploy and so cannot act on "deploy first" at all.
  const runBlocked = useMemo(
    () =>
      runBlockedMessage({
        deployed,
        hasProviderNode,
        canDeploy: can("workflow.deploy", readOnly),
      }),
    [deployed, hasProviderNode, readOnly],
  );

  const buildMode =
    can("workflow.buildFromChat", readOnly) &&
    (!hasProviderNode || manualBuildMode);

  // Returns the new run's id, or null when no run started. Callers that own a
  // chat turn need that signal: a failure here only raises a toast, and
  // without an answer the turn would sit on "working…" forever.
  //
  // The guard lives here, not only in onRun: the chat panel calls startRun
  // directly, so keeping it upstream silently dropped the explanation for
  // every message sent from the console.
  const startRun = useCallback(
    async (input?: Record<string, unknown>): Promise<string | null> => {
      if (!workflow) return null;
      if (runBlocked) {
        showToast(runBlocked);
        return null;
      }
      try {
        const res = await workflowsApi.run(workflow.id, input);
        setRunId(res.runId);
        setRunning(true);
        setLogOpen(true);
        showToast(`Run started · ${res.runId.slice(0, 8)}…`);
        return res.runId;
      } catch (err: unknown) {
        showToast(
          `Run failed · ${err instanceof Error ? err.message : "unknown error"}`,
        );
        return null;
      }
    },
    [workflow, runBlocked, showToast],
  );

  const startBuild = useCallback(
    async (text: string): Promise<{ ok: boolean; reply?: string }> => {
      if (!workflow) return { ok: false };
      // Latch build mode on for the rest of the session. Without this, the
      // provider node this very call is about to add flips hasProviderNode
      // true, and the next message would route to a run instead of
      // continuing the conversation. Latching here (rather than defaulting
      // manualBuildMode to true) keeps an already-populated workflow that is
      // merely being reopened in run mode until the user actually builds.
      setManualBuildMode(true);
      try {
        // The backend loads the graph fresh from the DB, so a drag still
        // sitting in the autosave debounce would be invisible to it and lost
        // when the build response replaces local state.
        await flushPendingSave();
        const res = await workflowsApi.build(workflow.id, text);
        setWorkflow((wf) =>
          wf
            ? { ...wf, nodes: res.workflow.nodes, edges: res.workflow.edges }
            : wf,
        );
        return { ok: true, reply: res.reply };
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : "unknown error";
        showToast(`Build failed · ${message}`);
        return {
          ok: false,
          reply: `Could not update the workflow: ${message}`,
        };
      }
    },
    [workflow, showToast, flushPendingSave],
  );

  const onRun = useCallback(async () => {
    if (!workflow) return;
    if (runBlocked) {
      showToast(runBlocked);
      return;
    }
    if (running) {
      try {
        await workflowsApi.stop(workflow.id);
      } catch {
        /* ignore */
      }
      setRunning(false);
      return;
    }
    if (hasChatTrigger) {
      // A chat workflow is run by talking to it, not by a Run button: open
      // the console so the conversation is where the message gets typed.
      setLogOpen(true);
      return;
    }
    await startRun();
  }, [workflow, running, hasChatTrigger, startRun, showToast, runBlocked]);

  const onDragNodeStart = useCallback(
    (e: React.DragEvent, meta: Partial<WorkflowNode>) => {
      e.dataTransfer.setData("application/agentmesh", JSON.stringify(meta));
      e.dataTransfer.effectAllowed = "move";
    },
    [],
  );

  // A node handed over from the Bazaar page (/workflows/{id}?add=…). The
  // canvas otherwise only gains nodes by drag-and-drop, which cannot cross a
  // page boundary — see encodePendingNode in lib/bazaar.ts.
  const searchParams = useSearchParams();
  const pendingAdd = searchParams.get("add");
  // Tracks the specific `add` value already consumed, not just whether any
  // add has ever happened -- CanvasPage doesn't remount between two Bazaar
  // visits to the SAME workflow, so a plain boolean latch would silently
  // drop every add after the first for that workflow's whole session.
  // router.replace below clears the URL param immediately after consuming
  // it, so pendingAdd returns to null before a genuinely new value can
  // arrive -- this only needs to survive React's own re-render/Strict Mode
  // double-invoke for the SAME value, not distinguish a history of values.
  const consumedAdd = useRef<string | null>(null);
  useEffect(() => {
    if (!pendingAdd || consumedAdd.current === pendingAdd || !workflow) return;
    const meta = decodePendingNode(pendingAdd);
    // Consume the param either way: a malformed value must not re-trigger on
    // every render, and must not survive a refresh as a phantom pending node.
    consumedAdd.current = pendingAdd;
    router.replace(`/workflows/${workflow.id}`);
    if (!meta) return;
    // Drop it slightly off-centre so it never lands exactly on an existing
    // node when several are added in a row. Wraps every 8 nodes instead of
    // growing with workflow.nodes.length forever -- otherwise a workflow
    // that already has many nodes places the next Bazaar add far outside
    // the visible viewport with no pan-to-node to bring it back into view.
    const offset = (workflow.nodes.length % 8) * 24;
    const node = {
      ...meta,
      id: `n_${Date.now()}`,
      x: 220 + offset,
      y: 180 + offset,
    } as WorkflowNode;
    // Reuse the same skip-once mechanism the initial load uses: a Bazaar
    // visit should never silently mutate a workflow the user didn't
    // explicitly choose to save, but this auto-add DOES change `workflow`
    // state, which the auto-save effect below would otherwise persist on its
    // very next debounce cycle (~1.5s) — including onto an already-deployed
    // workflow. Setting skipNextAutosave here makes the auto-save effect treat this
    // change exactly like the initial load: skip once. The user's next
    // INTENTIONAL edit (drag, wire, field change) triggers a real save
    // normally.
    skipNextAutosave.current = true;
    // This is a one-shot sync from an external source (the URL) into React
    // state, gated by consumedAdd so it can only fire once per mount — not
    // the derived-state-cascade pattern this rule exists to catch.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setWorkflow((wf) => (wf ? { ...wf, nodes: [...wf.nodes, node] } : wf));
    showToast(`Added ${meta.name ?? "endpoint"} to the canvas`);
  }, [pendingAdd, workflow, router, setWorkflow, showToast]);

  // Wrapper typed as non-null so child components don't need to change.
  // Safe because children only render after the null guard above.
  const setWorkflowNN = useCallback(
    (val: Workflow | ((prev: Workflow) => Workflow)) => {
      setWorkflow((wf) => {
        if (wf === null) return wf;
        return typeof val === "function" ? val(wf) : val;
      });
    },
    [setWorkflow],
  ) as React.Dispatch<React.SetStateAction<Workflow>>;

  // Below the breakpoint, an EDITOR stops here: no graph, no chat/SSE host.
  // A viewer does not -- it falls through to the stacked studio and bottom
  // sheet below, which is what makes a workflow readable on a phone at all.
  if (isNarrow && canEdit) {
    return (
      <div style={{ height: "100dvh", background: "var(--bg)" }}>
        <div className="canvas-narrow">
          <p className="canvas-narrow__title">
            The editor needs a wider screen.
          </p>
          <p className="canvas-narrow__body">
            Building a workflow means dragging nodes across a canvas and
            resizing side panels. That does not work on a phone yet.
          </p>
          <button onClick={() => router.push("/workflows")}>
            ← Back to workflows
          </button>
        </div>
      </div>
    );
  }

  if (loading || !workflow) {
    return (
      <div
        className="am-viewport"
        style={{
          height: "100dvh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "var(--bg)",
          color: "var(--fg-dim)",
          fontFamily: "var(--font-mono)",
          fontSize: 12,
        }}
      >
        {workflowId === "new" ? "creating workflow…" : "loading…"}
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
      <CanvasTopbar
        workflow={workflow}
        setWorkflow={setWorkflowNN}
        deployed={deployed}
        running={running}
        onDeploy={onDeploy}
        onRun={onRun}
        runBlocked={runBlocked}
        saveLabel={saveLabel}
        onBack={() => router.push("/workflows")}
      />

      <div
        ref={attachRow}
        className="am-studio-row"
        data-compact={compact ? "true" : "false"}
      >
        {/* The palette exists to drag new nodes onto the canvas, so in
            read-only mode it has no job -- and dropping it gives the graph
            back ~280px, which is what makes the studio fit a narrow window
            at all (PALETTE.min + MIN_CANVAS + INSPECTOR.min was 780px). */}
        {/* The palette lives in exactly one place at a time: its own column
            when there is room, otherwise a pane in the bottom sheet (see
            paletteNode below). Rendering it here while the row is stacked
            gave it a full-height column of its own and squeezed the canvas
            to zero. */}
        {/* Collapsed: the column and its resize handle give way to a thin
            rail, so the canvas gets the full ~280px back without the palette
            disappearing with no way to bring it back. */}
        {!compact && can("workflow.editGraph", readOnly) && paletteCollapsed && (
          <button
            type="button"
            onClick={() => setPaletteCollapsed(false)}
            title="Expand the library"
            aria-label="Expand the library"
            style={{
              flexShrink: 0,
              width: 26,
              alignSelf: "stretch",
              background: "var(--bg-elev-1)",
              border: "none",
              borderRight: "1px solid var(--border)",
              color: "var(--fg-muted)",
              cursor: "pointer",
              fontSize: 12,
            }}
          >
            ›
          </button>
        )}

        {!compact && can("workflow.editGraph", readOnly) && !paletteCollapsed && (
          <>
            <PalettePanel
              onDragNodeStart={onDragNodeStart}
              onAddNode={(meta) => addAtCentre.current?.(meta)}
              width={paletteW}
              onCollapse={() => setPaletteCollapsed(true)}
            />
            <ResizeHandle
              side="left"
              value={paletteW}
              min={PALETTE.min}
              max={PALETTE.max}
              ariaLabel="Resize palette panel"
              onChange={resizePalette}
              onCommit={persistWidths}
              onReset={() => {
                setPaletteW(PALETTE.default);
                persistWidths();
              }}
            />
          </>
        )}

        <ChatConsoleHost
          runId={runId}
          running={running}
          workflowId={workflow?.id}
          buildMode={buildMode}
          attempt={resumeAttempt}
          onBuildMessage={startBuild}
          onSendMessage={async (msg) =>
            (await startRun({ message: msg })) !== null
          }
          onRunComplete={() => {
            setRunning(false);
            // A run that paid for an x402 call has just debited credits;
            // re-read the balance so the topbar reflects the spend instead
            // of the pre-run figure.
            void refreshCredits();
          }}
        >
          {(chat) => (
            <>
              <div
                style={{
                  flex: 1,
                  minWidth: 0,
                  position: "relative",
                  display: "flex",
                  flexDirection: "column",
                }}
              >
                <CanvasGraph
                  addAtCentreRef={addAtCentre}
                  workflow={workflow}
                  setWorkflow={setWorkflowNN}
                  selectedId={selectedId}
                  setSelectedId={setSelectedId}
                  deployed={deployed}
                  running={running}
                  attachedSummaries={attachedSummaries}
                />
                <ConsolePanel
                  open={logOpen}
                  onToggle={() => setLogOpen((o) => !o)}
                  runId={runId}
                  running={running}
                  logs={chat.logs}
                  elapsed={chat.elapsed}
                  done={chat.done}
                  deadLetters={chat.deadLetters}
                  onResume={handleResume}
                />
              </div>

              {compact ? (
                <div
                  className="am-studio-sheet am-safe-bottom"
                  data-open={sheetOpen ? "true" : "false"}
                >
                  <button
                    className="am-sheet-grip"
                    onClick={() => setSheetOpen((o) => !o)}
                    aria-expanded={sheetOpen}
                    aria-label={
                      sheetOpen ? "Collapse the panel" : "Expand the panel"
                    }
                  >
                    {sheetOpen ? "" : selected ? "node selected" : "chat"}
                  </button>
                  {sheetOpen && (
                    <ChatRail
                      session={chat.session}
                      onSend={chat.handleSend}
                      busy={chat.busy}
                      onShowLogs={() => setLogOpen(true)}
                      width="100%"
                      buildMode={buildMode}
                      canToggleBuildMode={
                        can("workflow.buildFromChat", readOnly) &&
                        hasProviderNode
                      }
                      onToggleBuildMode={() => setManualBuildMode((v) => !v)}
                      inspectorNode={
                        <Inspector
                          selected={selected}
                          workflowId={workflow.id}
                          onUpdate={onUpdate}
                          onDelete={onDelete}
                          onClose={() => setSelectedId(null)}
                          width="100%"
                          embedded
                        />
                      }
                      hasSelection={selected !== null}
                      leaseId={chat.leaseId}
                      forceTab={selected ? "inspector" : null}
                      // Only when this client may author: a stacked layout
                      // on a computer. A handheld gets no palette at all,
                      // and a desktop gives it its own column instead.
                      paletteNode={
                        can("workflow.editGraph", readOnly) ? (
                          <PalettePanel
                            onDragNodeStart={onDragNodeStart}
                            onAddNode={(meta) => addAtCentre.current?.(meta)}
                            width="100%"
                          />
                        ) : undefined
                      }
                    />
                  )}
                </div>
              ) : (
                <>
                  <ResizeHandle
                    side="right"
                    value={inspectorW}
                    min={INSPECTOR.min}
                    max={INSPECTOR.max}
                    ariaLabel="Resize chat panel"
                    onChange={resizeInspector}
                    onCommit={persistWidths}
                    onReset={() => {
                      setInspectorW(INSPECTOR.default);
                      persistWidths();
                    }}
                  />
                  <ChatRail
                    session={chat.session}
                    onSend={chat.handleSend}
                    busy={chat.busy}
                    onShowLogs={() => setLogOpen(true)}
                    width={inspectorW}
                    buildMode={buildMode}
                    canToggleBuildMode={
                      can("workflow.buildFromChat", readOnly) && hasProviderNode
                    }
                    onToggleBuildMode={() => setManualBuildMode((v) => !v)}
                    inspectorNode={
                      <Inspector
                        selected={selected}
                        workflowId={workflow.id}
                        onUpdate={onUpdate}
                        onDelete={onDelete}
                        onClose={() => setSelectedId(null)}
                        width="100%"
                        embedded
                      />
                    }
                    hasSelection={selected !== null}
                    leaseId={chat.leaseId}
                  />
                </>
              )}
            </>
          )}
        </ChatConsoleHost>
      </div>

      {toast && <Toast message={toast} />}
    </div>
  );
}

// ── Chat/console state host ──────────────────────────────────────────────
// Owns the single useChatConsole call (SSE + chat session) that the chat
// rail and the bottom dock's log list both read from, so they share one
// subscription instead of racing two independent ones. Deliberately never
// keyed on runId: this used to remount per run, which also tore down and
// rebuilt everything nested inside it -- including CanvasGraph, wiping the
// user's pan/zoom on every single Run. useRunTranscript now resets its own
// per-run state explicitly instead (see its SSE-connect effect).
function ChatConsoleHost({
  runId,
  running,
  onRunComplete,
  workflowId,
  onSendMessage,
  buildMode,
  onBuildMessage,
  attempt,
  children,
}: {
  runId: string | null;
  running: boolean;
  onRunComplete: () => void;
  workflowId?: string;
  onSendMessage?: (text: string) => Promise<boolean>;
  buildMode?: boolean;
  onBuildMessage?: (text: string) => Promise<{ ok: boolean; reply?: string }>;
  attempt?: number;
  children: (chat: ChatConsole) => React.ReactNode;
}) {
  const chat = useChatConsole({
    runId,
    running,
    onRunComplete,
    workflowId,
    onSendMessage,
    buildMode,
    onBuildMessage,
    attempt,
  });
  return <>{children(chat)}</>;
}

// ── Topbar ─────────────────────────────────────────────────────────────────
// Shared by the editable field and its read-only twin so the workflow name
// occupies exactly the same space in both modes.
const nameFieldStyle: React.CSSProperties = {
  background: "transparent",
  border: "none",
  outline: "none",
  color: "var(--fg)",
  fontSize: 13,
  fontWeight: 500,
  fontFamily: "var(--font-sans)",
  // flex-basis "auto", not a fixed px: a fixed basis caps the field at that
  // width even when the topbar has room to spare, which is what truncated
  // an ordinary-length name ("Demo: Prism Code Review Pipeline") into "…"
  // on an otherwise empty row. auto sizes to the name's own text up to
  // maxWidth, and still shrinks (flex-shrink 1) once the row actually
  // runs out of space.
  flex: "0 1 auto",
  maxWidth: 480,
  // A floor, not 0. With minWidth:0 the field collapsed to 12px on a narrow
  // topbar -- the workflow name was simply gone. 120px keeps enough to read
  // and to recognise, and the text ellipsizes from there.
  minWidth: 120,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  padding: "4px 6px",
  borderRadius: 4,
};

function CanvasTopbar({
  workflow,
  setWorkflow,
  deployed,
  running,
  onDeploy,
  onRun,
  runBlocked,
  saveLabel,
  onBack,
}: {
  workflow: Workflow;
  setWorkflow: React.Dispatch<React.SetStateAction<Workflow>>;
  deployed: boolean;
  running: boolean;
  onDeploy: () => void;
  onRun: () => void;
  /** Why the Run button is disabled, or null when a run can proceed --
   *  computed once in CanvasPage (runBlockedMessage) so this component
   *  doesn't need its own copy of hasProviderNode/canDeploy to derive it. */
  runBlocked: string | null;
  saveLabel: string;
  onBack: () => void;
}) {
  const readOnly = useReadOnly();
  // Wallet balance is global (not per-node), so it lives in the topbar's
  // financial cluster. The value comes from the backend (the same row the
  // engine debits), so it is only meaningful once that fetch has landed —
  // hence balanceKnown, which separates a real $0 from "not asked yet".
  const { balanceUSD, balanceKnown, refreshBalance } =
    useCredits();
  const lowBalance = balanceKnown && balanceUSD < LOW_BALANCE_THRESHOLD_USD;

  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  return (
    <div
      style={{
        height: 52,
        flexShrink: 0,
        background: "var(--bg-elev-1)",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        alignItems: "center",
        padding: "0 14px",
        gap: 14,
      }}
    >
      <button
        onClick={onBack}
        style={{
          background: "transparent",
          border: "none",
          cursor: "pointer",
          padding: 0,
          display: "inline-flex",
        }}
      >
        <Logo size={16} />
      </button>
      <Hairline vertical length={20} />
      <button
        onClick={onBack}
        style={{ ...ghostBtnSm, flexShrink: 0, whiteSpace: "nowrap" }}
      >
        ← Workflows
      </button>
      <span style={{ color: "var(--fg-dim)" }}>/</span>
      {can("workflow.editGraph", readOnly) ? (
        <input
          value={workflow.name}
          onChange={(e) =>
            setWorkflow((wf) => ({ ...wf, name: e.target.value }))
          }
          style={{
            ...nameFieldStyle,
            // flex-basis:auto (nameFieldStyle) sizes a plain element to its
            // text content, but NOT a form control: an <input>'s intrinsic
            // size is a fixed UA default (~20 characters) regardless of its
            // value, so the read-only span above grows with the name and
            // this input would not. An explicit ch-based width, clamped to
            // the same floor/cap nameFieldStyle already enforces in px,
            // makes the editable field track what's actually typed.
            width: `${Math.min(Math.max(workflow.name.length + 2, 15), 60)}ch`,
          }}
        />
      ) : (
        <span
          style={{
            ...nameFieldStyle,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {workflow.name}
        </span>
      )}
      {saveLabel && <Pill mono>{saveLabel}</Pill>}
      {!can("workflow.editGraph", readOnly) && (
        <span title="Editing happens in the AgentMesh desktop app.">
          <Pill mono dot tone="warm">
            viewing only
          </Pill>
        </span>
      )}

      <div style={{ flex: 1 }} />

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 14,
          padding: "0 14px",
          borderLeft: "1px solid var(--border)",
          borderRight: "1px solid var(--border)",
          height: 36,
          flexShrink: 0,
        }}
      >
        <Stat
          label="credits"
          value={balanceKnown ? `$${balanceUSD.toFixed(2)}` : "-"}
          color={lowBalance ? "var(--danger)" : "var(--accent)"}
        />
        <span className="am-studio-stats-extra">
          <Hairline vertical length={18} />
        </span>
        <span className="am-studio-stats-extra">
          <Stat
            label="agents"
            value={workflow.nodes.filter((n) => n.type === "agent").length}
          />
        </span>
        <span className="am-studio-stats-extra">
          <Stat
            label="tools"
            value={
              workflow.nodes.filter(
                (n) => n.type === "tool" || n.type === "tool402",
              ).length
            }
          />
        </span>
        <span className="am-studio-stats-extra">
          <Stat
            label="x402"
            value={workflow.nodes.filter((n) => n.type === "tool402").length}
            color="#E879F9"
          />
        </span>
      </div>

      {can("workflow.deploy", readOnly) && (
        <>
          <button style={ghostBtnSm}>Share</button>
          <button onClick={onDeploy} style={btnStyle}>
            {deployed ? "Re-deploy" : "Deploy"}
          </button>
        </>
      )}
      <button
        onClick={onRun}
        disabled={!!runBlocked}
        title={runBlocked ?? "Run workflow"}
        style={{
          ...primaryBtnSm,
          minWidth: 86,
          justifyContent: "center",
          opacity: runBlocked ? 0.5 : 1,
        }}
      >
        {running ? (
          <>
            <IconStop size={10} /> Stop
          </>
        ) : (
          <>
            <IconPlay size={12} /> Run
          </>
        )}
      </button>
      <Hairline vertical length={20} />
      <div
        style={{
          width: 28,
          height: 28,
          borderRadius: 999,
          background: "var(--accent)",
          color: "var(--accent-fg)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 11,
          fontWeight: 700,
        }}
      >
        AC
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  unit,
  color,
}: {
  label: string;
  value: string | number;
  unit?: string;
  color?: string;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 1 }}>
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          color: "var(--fg-dim)",
          textTransform: "uppercase",
          letterSpacing: "0.06em",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontFamily: "var(--font-sans)",
          fontSize: 13,
          fontWeight: 500,
          color: color ?? "var(--fg)",
        }}
      >
        {value}
        {unit && (
          <span style={{ color: "var(--fg-dim)", fontSize: 10, marginLeft: 3 }}>
            {unit}
          </span>
        )}
      </span>
    </div>
  );
}
const btnStyle: React.CSSProperties = {
  height: 28,
  padding: "0 12px",
  fontSize: 12,
  fontWeight: 500,
  background: "var(--bg-elev-2)",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-2)",
  color: "var(--fg)",
  cursor: "pointer",
  fontFamily: "var(--font-sans)",
  display: "inline-flex",
  alignItems: "center",
  gap: 4,
  whiteSpace: "nowrap",
  flexShrink: 0,
};
