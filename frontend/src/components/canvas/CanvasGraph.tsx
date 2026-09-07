"use client";
import { useState, useRef, useEffect, useCallback } from "react";
import { can } from "@/lib/readonly";
import { ctrlBtn } from "@/components/ui/buttons";
import { useReadOnly } from "@/hooks/useReadOnly";
import {
  DEFAULT_VIEW,
  distance,
  midpoint,
  zoomAbout,
  zoomByFactor,
  fitToRects,
  screenToWorld,
  type Point,
  type ViewState,
} from "./viewport";
import { WorkflowNode, Workflow, PortName } from "@/lib/types";
import { NODE_TYPES } from "@/lib/data";
import {
  portWorld,
  portForFrom,
  portForTo,
  isValidConnection,
} from "@/lib/portUtils";
import { IconBackspace } from "@/components/ui";
import { CanvasNode } from "./nodes";

interface CanvasGraphProps {
  workflow: Workflow;
  setWorkflow: React.Dispatch<React.SetStateAction<Workflow>>;
  selectedId: string | null;
  setSelectedId: (id: string | null) => void;
  deployed: boolean;
  running: boolean;
  attachedSummaries: Record<string, { model: string | null; tools: number }>;
  /** Populated with a function that drops a node in the middle of whatever
   *  the canvas is currently showing. The palette uses it for tap-to-add,
   *  which has no cursor position to place against. */
  addAtCentreRef?: React.MutableRefObject<
    ((meta: Partial<WorkflowNode>) => void) | null
  >;
}

interface WireState {
  fromId: string;
  fromPort: PortName;
  x: number;
  y: number;
}
interface HoverPort {
  nodeId: string;
  port: PortName;
}

export function CanvasGraph({
  workflow,
  setWorkflow,
  selectedId,
  setSelectedId,
  deployed,
  running,
  attachedSummaries,
  addAtCentreRef,
}: CanvasGraphProps) {
  // Read-only turns the graph into a diagram: pan, zoom, and selection all
  // still work, because reading a workflow means moving around it and
  // opening a node to see how it is configured. Only the gestures that
  // CHANGE the graph -- dropping a node, moving one, drawing or cutting an
  // edge -- come off.
  const editable = can("workflow.editGraph", useReadOnly());

  const wrapRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<ViewState>(DEFAULT_VIEW);
  // Mirrors `view` for addNodeAt below to read without depending on it: `view`
  // updates on every pointer-move during a pan/zoom/drag, and a callback that
  // depends on it gets a new identity on every one of those ticks.
  const viewRef = useRef(view);
  useEffect(() => {
    viewRef.current = view;
  }, [view]);
  const [panning, setPanning] = useState(false);
  const panRef = useRef({ active: false, sx: 0, sy: 0, ox: 0, oy: 0 });
  const dragRef = useRef<{
    id: string;
    sx: number;
    sy: number;
    ox: number;
    oy: number;
  } | null>(null);
  const wireRef = useRef<{ fromId: string; fromPort: PortName } | null>(null);
  const [wire, setWire] = useState<WireState | null>(null);
  const [hoverPort, setHoverPort] = useState<HoverPort | null>(null);
  // Value intentionally unread: the tick's only job is forcing re-renders
  // while a run is active so the animated edges advance.
  const [, setAnimTick] = useState(0);

  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => setAnimTick((t) => t + 1), 90);
    return () => clearInterval(id);
  }, [running]);

  // Wheel zoom
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const mx = e.clientX - rect.left,
        my = e.clientY - rect.top;
      setView((v) => zoomAbout(v, v.k * (1 - e.deltaY * 0.0015), mx, my));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const onBgMouseDown = (e: React.MouseEvent) => {
    if (e.button !== 0 && e.button !== 1) return;
    const target = e.target as HTMLElement;
    const onNode = !!(
      target.closest("[data-node]") || target.closest("[data-port]")
    );
    if (e.button === 0) {
      // Left button pans only from empty background, and deselects.
      if (onNode) return;
      setSelectedId(null);
    } else {
      // Middle (scroll) button pans anywhere; suppress the OS autoscroll.
      e.preventDefault();
    }
    panRef.current = {
      active: true,
      sx: e.clientX,
      sy: e.clientY,
      ox: view.x,
      oy: view.y,
    };
    setPanning(true);
  };

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (panRef.current.active) {
        const dx = e.clientX - panRef.current.sx;
        const dy = e.clientY - panRef.current.sy;
        setView((v) => ({
          ...v,
          x: panRef.current.ox + dx,
          y: panRef.current.oy + dy,
        }));
      }
      if (dragRef.current) {
        const { id, sx, sy, ox, oy } = dragRef.current;
        const dx = (e.clientX - sx) / view.k;
        const dy = (e.clientY - sy) / view.k;
        setWorkflow((wf) => ({
          ...wf,
          nodes: wf.nodes.map((n) =>
            n.id === id ? { ...n, x: ox + dx, y: oy + dy } : n,
          ),
        }));
      }
      if (wireRef.current && wrapRef.current) {
        const rect = wrapRef.current.getBoundingClientRect();
        setWire((w) =>
          w
            ? {
                ...w,
                x: (e.clientX - rect.left - view.x) / view.k,
                y: (e.clientY - rect.top - view.y) / view.k,
              }
            : w,
        );
      }
    };

    const onUp = () => {
      panRef.current.active = false;
      setPanning(false);
      dragRef.current = null;

      if (
        wireRef.current &&
        hoverPort &&
        hoverPort.nodeId !== wireRef.current.fromId
      ) {
        const fromNode = workflow.nodes.find(
          (n) => n.id === wireRef.current!.fromId,
        );
        const toNode = workflow.nodes.find((n) => n.id === hoverPort.nodeId);
        if (
          fromNode &&
          toNode &&
          isValidConnection(
            fromNode,
            wireRef.current.fromPort,
            toNode,
            hoverPort.port,
          )
        ) {
          const kind =
            hoverPort.port === "model" || hoverPort.port === "tools"
              ? "attach"
              : "flow";
          setWorkflow((wf) => ({
            ...wf,
            edges: [
              ...wf.edges,
              {
                id: `e_${Date.now()}`,
                from: fromNode.id,
                to: toNode.id,
                kind,
                toPort: hoverPort.port,
              },
            ],
          }));
        }
      }
      wireRef.current = null;
      setWire(null);
    };

    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, [view, hoverPort, workflow, setWorkflow]);

  const removeEdge = (id: string) =>
    setWorkflow((wf) => ({
      ...wf,
      edges: wf.edges.filter((e) => e.id !== id),
    }));

  const nodeRects = useCallback(
    () =>
      workflow.nodes.map((n) => {
        const t = NODE_TYPES[n.type];
        return { x: n.x, y: n.y, w: t?.w ?? 180, h: t?.h ?? 60 };
      }),
    [workflow.nodes],
  );

  const fitView = useCallback(() => {
    const el = wrapRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    setView(fitToRects(nodeRects(), rect.width, rect.height));
  }, [nodeRects]);

  // The +/- buttons magnify the middle of what is on screen. Anchoring on the
  // world origin instead would slide the graph away as you zoom, which is what
  // the raw `k` multiply used to do.
  const zoomFromCentre = useCallback((factor: number) => {
    const el = wrapRef.current;
    const rect = el?.getBoundingClientRect();
    const cx = rect ? rect.width / 2 : 0;
    const cy = rect ? rect.height / 2 : 0;
    setView((v) => zoomByFactor(v, factor, cx, cy));
  }, []);

  // ── Touch ───────────────────────────────────────────────────────────────
  // The mouse path above is untouched; this runs alongside it. Without it the
  // graph is completely inert on a phone -- there is no mousedown, so there is
  // no way to pan or zoom, and a workflow whose nodes start off-screen simply
  // cannot be looked at.
  //
  // Touch events rather than pointer events on purpose: a pinch needs two
  // simultaneous contacts, and `e.touches` hands them over directly where
  // pointer events would mean maintaining a live map of active pointer ids.
  const touchRef = useRef<{
    mode: "none" | "pan" | "pinch" | "node";
    sx: number;
    sy: number;
    startView: ViewState;
    startDist: number;
    // "node" mode only: which node is moving, and where it started.
    nodeId: string;
    ox: number;
    oy: number;
  }>({
    mode: "none",
    sx: 0,
    sy: 0,
    startView: DEFAULT_VIEW,
    startDist: 0,
    nodeId: "",
    ox: 0,
    oy: 0,
  });

  // Touch coordinates relative to the canvas box, which is the space the view
  // transform is expressed in.
  const localPoint = (t: React.Touch, rect: DOMRect): Point => ({
    x: t.clientX - rect.left,
    y: t.clientY - rect.top,
  });

  const onTouchStart = (e: React.TouchEvent) => {
    const el = wrapRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();

    if (e.touches.length >= 2) {
      const a = localPoint(e.touches[0], rect);
      const b = localPoint(e.touches[1], rect);
      touchRef.current = {
        ...touchRef.current,
        mode: "pinch",
        sx: 0,
        sy: 0,
        startView: view,
        startDist: distance(a, b),
      };
      return;
    }

    if (e.touches.length !== 1) return;
    const t = e.touches[0];
    const target = e.target as HTMLElement;
    const nodeEl = target.closest("[data-node]") as HTMLElement | null;
    const onPort = !!target.closest("[data-port]");

    // A tap on a node always selects it, editable or not -- same as
    // startNodeDrag's mouse path: "Selecting still opens the inspector; only
    // the move is withheld." While the graph is editable a drag that starts
    // on a node belongs to that node, exactly as it does for the mouse. With
    // nothing to drag, panning from anywhere is what a viewer expects --
    // otherwise a graph whose nodes fill the screen has no background left
    // to grab.
    if (nodeEl && !onPort) {
      const id = nodeEl.getAttribute("data-node");
      const n = id ? workflow.nodes.find((x) => x.id === id) : undefined;
      if (n) {
        setSelectedId(n.id);
        touchRef.current = editable
          ? {
              mode: "node",
              sx: t.clientX,
              sy: t.clientY,
              startView: view,
              startDist: 0,
              nodeId: n.id,
              ox: n.x,
              oy: n.y,
            }
          : { ...touchRef.current, mode: "none" };
        return;
      }
    }
    // Drawing an edge by touch is not implemented, so a touch on a port does
    // nothing rather than panning the canvas out from under the finger.
    if (editable && onPort) return;

    // Same as onBgMouseDown's left-button pan: a tap on empty background
    // deselects. Without this, the natural "tap outside to dismiss the
    // inspector" gesture that panning-from-anywhere otherwise supports left
    // the previously-selected node stuck selected and the inspector open.
    setSelectedId(null);

    touchRef.current = {
      ...touchRef.current,
      mode: "pan",
      sx: t.clientX,
      sy: t.clientY,
      startView: view,
      startDist: 0,
    };
  };

  const onTouchMove = (e: React.TouchEvent) => {
    const st = touchRef.current;
    const el = wrapRef.current;
    if (!el || st.mode === "none") return;

    if (st.mode === "pinch") {
      if (e.touches.length < 2 || st.startDist <= 0) return;
      const rect = el.getBoundingClientRect();
      const a = localPoint(e.touches[0], rect);
      const b = localPoint(e.touches[1], rect);
      const m = midpoint(a, b);
      // Recomputed from the gesture's starting view every frame rather than
      // accumulated, so a long pinch cannot drift.
      setView(
        zoomAbout(
          st.startView,
          st.startView.k * (distance(a, b) / st.startDist),
          m.x,
          m.y,
        ),
      );
      return;
    }

    if (e.touches.length !== 1) return;
    const t = e.touches[0];

    if (st.mode === "node") {
      // Same arithmetic the mouse drag uses: screen delta divided by the
      // zoom, so a finger and a cursor move a node by the same amount.
      const dx = (t.clientX - st.sx) / st.startView.k;
      const dy = (t.clientY - st.sy) / st.startView.k;
      setWorkflow((wf) => ({
        ...wf,
        nodes: wf.nodes.map((n) =>
          n.id === st.nodeId ? { ...n, x: st.ox + dx, y: st.oy + dy } : n,
        ),
      }));
      return;
    }

    setView({
      ...st.startView,
      x: st.startView.x + (t.clientX - st.sx),
      y: st.startView.y + (t.clientY - st.sy),
    });
  };

  const onTouchEnd = (e: React.TouchEvent) => {
    // A lifted finger fires a browser-emulated click on whatever element sat
    // under it -- harmless over empty background, but a tap that landed on an
    // edge (edges aren't [data-node], so onTouchStart's node branch doesn't
    // catch them; they fall to the generic pan case) would otherwise trigger
    // that edge's onClick={editable ? () => removeEdge(e.id) : undefined} a
    // moment after our own touch handling already ran, silently deleting it.
    // Suppressed only when we actually handled this as a gesture of our own
    // (mode !== "none"); React registers touchstart/touchmove/wheel as
    // passive listeners by default (preventDefault there is a no-op) but
    // touchend is not one of them, so this call has real effect.
    if (touchRef.current.mode !== "none") e.preventDefault();

    if (e.touches.length === 0) {
      touchRef.current.mode = "none";
      return;
    }
    // Lifting one finger out of a pinch hands the gesture back to pan rather
    // than ending it, so the graph does not jump.
    if (e.touches.length === 1) {
      const t = e.touches[0];
      touchRef.current = {
        ...touchRef.current,
        mode: "pan",
        sx: t.clientX,
        sy: t.clientY,
        startView: view,
        startDist: 0,
      };
    }
  };

  // Frame the whole graph on first paint. The stored view is whatever the last
  // editor left behind, and on a narrow screen that regularly puts every node
  // off-canvas -- the page would open on empty grid with no clue which way to
  // drag. Runs once, and only once the box has actually been measured.
  const didFit = useRef(false);
  useEffect(() => {
    if (didFit.current) return;
    if (workflow.nodes.length === 0) return;
    const rect = wrapRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) return;
    didFit.current = true;
    fitView();
  }, [workflow.nodes, fitView]);

  // Places a node so its CENTRE sits at the given point in the canvas box.
  // Shared by the drop (point = cursor) and the palette tap (point = middle
  // of the view), so the two cannot drift apart.
  const addNodeAt = useCallback(
    (meta: Partial<WorkflowNode>, px: number, py: number) => {
      const t = NODE_TYPES[meta.type!];
      const w = screenToWorld(viewRef.current, px, py);
      const id = `n_${Date.now()}`;
      const node = {
        id,
        x: w.x - (t ? t.w / 2 : 90),
        y: w.y - (t ? t.h / 2 : 30),
        ...meta,
      } as WorkflowNode;
      setWorkflow((wf) => ({ ...wf, nodes: [...wf.nodes, node] }));
      setSelectedId(id);
    },
    [setWorkflow, setSelectedId],
  );

  useEffect(() => {
    if (!addAtCentreRef) return;
    addAtCentreRef.current = (meta) => {
      const rect = wrapRef.current?.getBoundingClientRect();
      if (!rect) return;
      addNodeAt(meta, rect.width / 2, rect.height / 2);
    };
    return () => {
      addAtCentreRef.current = null;
    };
  }, [addAtCentreRef, addNodeAt]);

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    if (!editable) return;
    const data = e.dataTransfer.getData("application/agentmesh");
    if (!data || !wrapRef.current) return;
    const rect = wrapRef.current.getBoundingClientRect();
    addNodeAt(
      JSON.parse(data) as Partial<WorkflowNode>,
      e.clientX - rect.left,
      e.clientY - rect.top,
    );
  };

  const startNodeDrag = (e: React.MouseEvent, n: WorkflowNode) => {
    if (e.button !== 0) return; // only the left button moves a node; middle-button falls through to pan
    if ((e.target as HTMLElement).closest("[data-port]")) return;
    e.stopPropagation();
    setSelectedId(n.id);
    // Selecting still opens the inspector; only the move is withheld.
    if (!editable) return;
    dragRef.current = {
      id: n.id,
      sx: e.clientX,
      sy: e.clientY,
      ox: n.x,
      oy: n.y,
    };
  };

  const startWire = (
    e: React.MouseEvent,
    nodeId: string,
    fromPort: PortName,
  ) => {
    e.stopPropagation();
    if (!editable) return;
    const n = workflow.nodes.find((x) => x.id === nodeId);
    if (!n) return;
    const p = portWorld(n, fromPort);
    wireRef.current = { fromId: nodeId, fromPort };
    setWire({ fromId: nodeId, fromPort, x: p.x, y: p.y });
  };

  return (
    <div
      ref={wrapRef}
      onMouseDown={onBgMouseDown}
      onTouchStart={onTouchStart}
      onTouchMove={onTouchMove}
      onTouchEnd={onTouchEnd}
      onTouchCancel={onTouchEnd}
      onDragOver={(e) => e.preventDefault()}
      onDrop={onDrop}
      className="canvas-bg"
      style={{
        position: "relative",
        flex: 1,
        overflow: "hidden",
        background: "var(--bg)",
        backgroundSize: `${20 * view.k}px ${20 * view.k}px`,
        backgroundPosition: `${view.x}px ${view.y}px`,
        cursor: panning ? "grabbing" : "default",
        userSelect: "none",
        // Without this the browser takes the drag for page scroll and the
        // pinch for its own page zoom, and the handlers above never see a
        // usable gesture.
        touchAction: "none",
      }}
    >
      <div
        style={{
          position: "absolute",
          top: 0,
          left: 0,
          transform: `translate(${view.x}px, ${view.y}px) scale(${view.k})`,
          transformOrigin: "0 0",
          width: 0,
          height: 0,
        }}
      >
        {/* Edges */}
        <svg
          style={{
            position: "absolute",
            overflow: "visible",
            pointerEvents: "none",
          }}
          width="4000"
          height="3000"
        >
          {workflow.edges.map((e) => {
            const a = workflow.nodes.find((n) => n.id === e.from);
            const b = workflow.nodes.find((n) => n.id === e.to);
            if (!a || !b) return null;
            const fromPort = portForFrom(a, e.kind);
            const toPort = e.toPort ?? portForTo(b);
            const p1 = portWorld(a, fromPort);
            const p2 = portWorld(b, toPort);
            return (
              <EdgePath
                key={e.id}
                x1={p1.x}
                y1={p1.y}
                x2={p2.x}
                y2={p2.y}
                kind={e.kind}
                running={running}
                onClick={editable ? () => removeEdge(e.id) : undefined}
              />
            );
          })}
          {wire &&
            (() => {
              const a = workflow.nodes.find((n) => n.id === wire.fromId);
              if (!a) return null;
              const p = portWorld(a, wire.fromPort);
              const kind =
                hoverPort?.port === "model" || hoverPort?.port === "tools"
                  ? "attach"
                  : "flow";
              return (
                <EdgePath
                  x1={p.x}
                  y1={p.y}
                  x2={wire.x}
                  y2={wire.y}
                  kind={kind}
                  ghost
                />
              );
            })()}
        </svg>

        {/* Nodes */}
        {workflow.nodes.map((n) => (
          <CanvasNode
            key={n.id}
            node={n}
            selected={selectedId === n.id}
            deployed={deployed}
            onMouseDown={(e) => startNodeDrag(e, n)}
            onStartWire={(e, port) => startWire(e, n.id, port)}
            onPortHover={(port) => setHoverPort({ nodeId: n.id, port })}
            onPortLeave={() => setHoverPort(null)}
            attachedSummary={attachedSummaries[n.id]}
          />
        ))}
      </div>

      {/* Controls */}
      <div
        style={{
          position: "absolute",
          bottom: 44,
          right: 16,
          zIndex: 4,
          display: "flex",
          flexDirection: "column",
          gap: 4,
          background: "var(--bg-elev-2)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-2)",
          padding: 4,
        }}
      >
        <button
          onClick={() => zoomFromCentre(1.15)}
          style={ctrlBtn}
          aria-label="Zoom in"
          title="Zoom in"
        >
          +
        </button>
        <div
          style={{
            textAlign: "center",
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--fg-dim)",
          }}
        >
          {Math.round(view.k * 100)}%
        </div>
        <button
          onClick={() => zoomFromCentre(1 / 1.15)}
          style={ctrlBtn}
          aria-label="Zoom out"
          title="Zoom out"
        >
          −
        </button>
        <div
          style={{ height: 1, background: "var(--border)", margin: "2px 0" }}
        />
        <button
          onClick={fitView}
          style={ctrlBtn}
          aria-label="Fit workflow to screen"
          title="Fit to screen"
        >
          ⊡
        </button>
      </div>

      {/* Hints */}
      <div
        style={{
          position: "absolute",
          top: 14,
          left: 16,
          zIndex: 4,
          // Load-bearing at top-left: the row sits over the default view
          // origin, so without this it would eat the first node's mousedown
          // on nearly every workflow. The keycaps have no click behavior.
          pointerEvents: "none",
          display: "flex",
          flexWrap: "wrap",
          columnGap: 12,
          rowGap: 6,
          alignItems: "center",
          // Nothing sits top-right, unlike the zoom controls the old
          // bottom-left placement had to wrap away from.
          maxWidth: "calc(100% - 32px)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--fg-dim)",
        }}
      >
        {(
          [
            ["drag bg", "pan"],
            ["scroll", "zoom"],
            ["drag port", "connect"],
            [<IconBackspace key="bksp" size={12} />, "delete node"],
          ] as [React.ReactNode, string][]
        ).map(([k, v]) => (
          <span
            key={v}
            // The visible text no longer spells the key, so name it for
            // screen readers and anyone who doesn't recognise the glyph.
            title={v === "delete node" ? "Backspace" : undefined}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 5,
              whiteSpace: "nowrap",
              flexShrink: 0,
              // The row disables pointer events so it doesn't steal the
              // first node's mousedown; re-enable them just here so the
              // title tooltip is actually reachable by hover.
              pointerEvents: "auto",
            }}
          >
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                minWidth: 18,
                height: 18,
                padding: "0 4px",
                borderRadius: 4,
                border: "1px solid var(--border-strong)",
                background: "var(--bg-elev-1)",
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--fg-muted)",
                whiteSpace: "nowrap",
                flexShrink: 0,
              }}
            >
              {k}
            </span>
            {v}
          </span>
        ))}
      </div>

      {workflow.nodes.length === 0 && (
        <div
          style={{
            position: "absolute",
            inset: 0,
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            pointerEvents: "none",
            gap: 12,
          }}
        >
          <div
            style={{
              width: 64,
              height: 64,
              borderRadius: 12,
              border: "1px dashed var(--border-strong)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              color: "var(--fg-dim)",
              fontSize: 24,
            }}
          >
            +
          </div>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: "var(--fg-dim)",
            }}
          >
            empty canvas · drag a trigger from the left to begin
          </div>
        </div>
      )}
    </div>
  );
}

// ── Edge path ──────────────────────────────────────────────────────────────
function EdgePath({
  x1,
  y1,
  x2,
  y2,
  kind,
  running,
  ghost,
  onClick,
}: {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  kind?: string;
  running?: boolean;
  ghost?: boolean;
  onClick?: () => void;
}) {
  const isAttach = kind === "attach";
  const color = isAttach ? "#E879F9" : "var(--accent)";
  let d: string;
  if (isAttach) {
    const off = Math.max(30, Math.abs(y2 - y1) * 0.45);
    d = `M ${x1} ${y1} C ${x1} ${y1 - off}, ${x2} ${y2 + off}, ${x2} ${y2}`;
  } else {
    const off = Math.max(40, Math.abs(x2 - x1) * 0.4);
    d = `M ${x1} ${y1} C ${x1 + off} ${y1}, ${x2 - off} ${y2}, ${x2} ${y2}`;
  }

  return (
    <g style={{ pointerEvents: ghost ? "none" : "auto" }}>
      <path
        d={d}
        fill="none"
        stroke="transparent"
        strokeWidth="14"
        onClick={onClick}
        style={{ cursor: "pointer" }}
      />
      <path
        d={d}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeDasharray={isAttach ? "4 3" : ghost ? "3 4" : undefined}
        opacity={ghost ? 0.6 : 0.78}
      />
      {running && !ghost && (
        <circle r="3" fill={color}>
          <animateMotion dur="1.4s" repeatCount="indefinite" path={d} />
        </circle>
      )}
      <circle cx={x2} cy={y2} r="3" fill={color} opacity="0.95" />
    </g>
  );
}
