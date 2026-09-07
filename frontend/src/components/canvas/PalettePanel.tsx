"use client";
import {
  useState,
  useRef,
  useEffect,
  useLayoutEffect,
  useMemo,
  useCallback,
} from "react";
import { WorkflowNode } from "@/lib/types";
import { BrandLogo } from "./nodes/brandLogos";
import { PALETTE_TWO_COL_MIN } from "./panelSizing";
import {
  TRIGGER_TEMPLATES,
  AGENT_TEMPLATES,
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  ACTION_TEMPLATES,
  STATE_TEMPLATES,
  ACTION_CATEGORIES,
  END_TEMPLATES,
  TENDRIL_TEMPLATES,
  GOOGLE_TEMPLATES,
} from "@/lib/data";
import { IconSearch } from "@/components/ui";

const PALETTE_TABS = [
  {
    id: "triggers",
    label: "Triggers",
    items: () => TRIGGER_TEMPLATES,
    type: "trigger",
    dotColor: "mute" as const,
    map: (it: (typeof TRIGGER_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "trigger",
      template: it.id,
      label: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "agents",
    label: "Agents",
    items: () => AGENT_TEMPLATES,
    type: "agent",
    dotColor: "accent" as const,
    map: (it: (typeof AGENT_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "agent",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "providers",
    label: "Providers",
    items: () => PROVIDER_TEMPLATES,
    type: "provider",
    dotColor: "accent" as const,
    map: (it: (typeof PROVIDER_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "provider",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.model,
      model: it.model,
    }),
  },
  {
    id: "tools",
    label: "Tools",
    items: () => TOOL_TEMPLATES,
    type: "tool",
    dotColor: "mute" as const,
    map: (it: (typeof TOOL_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "tool",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    // No preset list: every x402 endpoint is different money moving to a
    // different real place, so this tab offers only the "New x402
    // Endpoint" custom creator below (paste URL -> Discover probes the
    // live 402 challenge for method/price). A prior preset list here
    // (Tavily/Firecrawl/etc.) pointed at invented hostnames nothing
    // real answers to -- removed rather than fixed in place.
    id: "x402",
    label: "x402",
    items: () => [] as never[],
    type: "tool402",
    dotColor: "magenta" as const,
    map: (): Partial<WorkflowNode> => ({ type: "tool402" }),
  },
  {
    id: "tendril",
    label: "Tendril",
    items: () => TENDRIL_TEMPLATES,
    type: "tendril",
    dotColor: "magenta" as const,
    map: (it: (typeof TENDRIL_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "tendril",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
      tendrilAction: it.action,
      tendrilHours: "1",
      tendrilAmount: "10",
    }),
  },
  {
    id: "actions",
    label: "Actions",
    items: () => ACTION_TEMPLATES,
    type: "action",
    dotColor: "mute" as const,
    map: (it: (typeof ACTION_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "action",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "state",
    label: "State",
    items: () => STATE_TEMPLATES,
    type: "state",
    dotColor: "info" as const,
    map: (it: (typeof STATE_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "state",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
      // The dropped node already knows its operation -- the palette entry
      // IS the choice of operation, so the inspector opens on a node that
      // only needs a key, not a mode decision first.
      stateOp: it.id as NonNullable<WorkflowNode["stateOp"]>,
    }),
  },
  {
    id: "google",
    label: "Google",
    items: () => GOOGLE_TEMPLATES,
    type: "google",
    dotColor: "accent" as const,
    map: (it: (typeof GOOGLE_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "google",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "end",
    label: "End",
    items: () => END_TEMPLATES,
    type: "end",
    dotColor: "mute" as const,
    map: (it: (typeof END_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "end",
      template: it.id,
      label: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
] as const;

type TabId = (typeof PALETTE_TABS)[number]["id"];

// ── Actions-tab search & grouping ───────────────────────────────────────────
// Scoped entirely to the Actions tab (see `isActions` below) -- the rest of
// the palette keeps its original flat, unranked `.includes()` filter.

interface ActionEntry {
  meta: Partial<WorkflowNode>;
  category?: string;
}

// Ranks a name-starts-with match above a name-contains match above a
// sub/category-only match, so e.g. typing "tel" surfaces "Telegram Message"
// before something whose description merely mentions it.
function scoreEntry(e: ActionEntry, q: string): number {
  if (!q) return 0;
  const name = (e.meta.name ?? e.meta.label ?? "").toLowerCase();
  const sub = (e.meta.sub ?? "").toLowerCase();
  const category = (e.category ?? "").toLowerCase();
  if (name.startsWith(q)) return 3;
  if (name.includes(q)) return 2;
  if (sub.includes(q) || category.includes(q)) return 1;
  return 0;
}

// Wraps the first case-insensitive match of `q` in `text` with the app's own
// accent-soft chip token -- reuses the existing accent pairing (already used
// by CreateRow's dashed box) instead of introducing a new highlight color.
function highlightMatch(text: string, q: string): React.ReactNode {
  if (!q) return text;
  const idx = text.toLowerCase().indexOf(q.toLowerCase());
  if (idx === -1) return text;
  return (
    <>
      {text.slice(0, idx)}
      <span
        style={{
          background: "var(--accent-soft)",
          color: "var(--accent)",
          borderRadius: 2,
        }}
      >
        {text.slice(idx, idx + q.length)}
      </span>
      {text.slice(idx + q.length)}
    </>
  );
}

const CREATE_META: Record<string, Partial<WorkflowNode>> = {
  triggers: {
    type: "trigger",
    custom: true,
    label: "Custom Trigger",
    icon: "✳",
    sub: "define your own",
  },
  agents: {
    type: "agent",
    custom: true,
    name: "Custom Agent",
    icon: "◇",
    sub: "your own agent",
    systemPrompt: "You are a helpful agent.",
  },
  providers: {
    type: "provider",
    custom: true,
    name: "Custom Provider",
    icon: "+",
    sub: "model + API key",
    model: "custom-model",
  },
  tools: {
    type: "tool",
    custom: true,
    name: "Custom Tool",
    icon: "⟶",
    sub: "any HTTP endpoint",
  },
  x402: {
    type: "tool402",
    custom: true,
    name: "New x402 Endpoint",
    icon: "✦",
    sub: "paste URL · auto-price",
  },
  tendril: {
    type: "tendril",
    custom: true,
    name: "Custom Tendril",
    icon: "▣",
    sub: "rent · run · release",
    tendrilAction: "rent",
    tendrilHours: "1",
    tendrilAmount: "10",
  },
  actions: {
    type: "action",
    custom: true,
    name: "Custom Action",
    icon: "✦",
    sub: "your own action",
  },
  // Google has no "custom operation" concept -- all 11 real ones (Gmail/
  // Sheets/Calendar/Drive) are already listed individually below. This
  // create-row defaults to the most common one (send) purely so dragging
  // the dashed "+" card behaves consistently with every other tab; picking
  // a different Google row below is the normal way to get a different op.
  google: {
    type: "google",
    custom: true,
    name: "Custom Gmail Send",
    icon: "✉",
    sub: "or drag a specific op below",
    template: "gmail_send",
  },
  end: {
    type: "end",
    custom: true,
    label: "Custom End",
    icon: "■",
    sub: "define your own",
  },
};

interface PalettePanelProps {
  onDragNodeStart: (e: React.DragEvent, meta: Partial<WorkflowNode>) => void;
  /** Adds the item without a drag -- a tap, Enter, or Space. Drag is a
   *  mouse-only API, so on a touch device it is the ONLY way to get a node
   *  onto the canvas; it also makes the palette keyboard-operable. */
  onAddNode?: (meta: Partial<WorkflowNode>) => void;
  width?: number | string;
  /** Collapses the palette column. Omitted where the palette is not in a
   *  column of its own (the compact bottom sheet), which has its own
   *  open/close control and would end up with two. */
  onCollapse?: () => void;
}

export function PalettePanel({
  onDragNodeStart,
  onAddNode,
  width = 280,
  onCollapse,
}: PalettePanelProps) {
  const [tab, setTab] = useState<TabId>("triggers");
  const [q, setQ] = useState("");

  // Only consulted when `width` is not a number. Starts null, and one column
  // is the pre-measurement default: narrow is the failing case, so erring
  // that way costs a single reflow instead of a frame of squeezed rows.
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [measuredWidth, setMeasuredWidth] = useState<number | null>(null);

  // A zero width is not a measurement, it is a hidden element. The rail keeps
  // every pane mounted and toggles `display`, so the palette measures 0x0
  // while its tab is inactive -- latching that would peg the list to one
  // column for the rest of the session.
  const syncWidth = useCallback(() => {
    const el = rootRef.current;
    if (!el) return;
    const w = el.getBoundingClientRect().width;
    if (w > 0) setMeasuredWidth((prev) => (prev === w ? prev : w));
  }, []);

  // Runs after every render, which is what catches display:none -> visible
  // when the Build tab is selected. Cheap (one rect read) and self-limiting:
  // it only sets state when the number actually changed.
  useLayoutEffect(() => {
    if (typeof width !== "number") syncWidth();
  });

  // And this catches the window being resized while the tab is already open.
  useEffect(() => {
    const el = rootRef.current;
    if (!el || typeof width === "number") return;
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(syncWidth);
    ro.observe(el);
    return () => ro.disconnect();
  }, [width, syncWidth]);

  const tabDef = PALETTE_TABS.find((t) => t.id === tab)!;
  const items = tabDef.items() as unknown[];
  const mapped = useMemo(
    () =>
      (items as Parameters<typeof tabDef.map>[0][]).map(
        tabDef.map as (
          it: Parameters<typeof tabDef.map>[0],
        ) => Partial<WorkflowNode>,
      ),
    [items, tabDef],
  );
  const filtered = useMemo(
    () =>
      mapped.filter(
        (i) =>
          ((i.name ?? i.label ?? "") as string)
            .toLowerCase()
            .includes(q.toLowerCase()) ||
          (i.sub ?? "").toLowerCase().includes(q.toLowerCase()),
      ),
    [mapped, q],
  );

  // The Actions tab is the one palette tab that's actually a connector
  // library (23 real integrations) -- everything below is scoped to it via
  // `isActions` so every other tab keeps using `filtered` above, untouched.
  const isActions = tab === "actions";
  const q_ = q.trim().toLowerCase();
  const actionEntries: ActionEntry[] = useMemo(
    () =>
      isActions
        ? (items as { category?: string }[]).map((raw, i) => ({
            meta: mapped[i],
            category: raw.category,
          }))
        : [],
    [isActions, items, mapped],
  );
  const actionSearched = useMemo(
    () =>
      q_
        ? actionEntries
            .filter((e) => scoreEntry(e, q_) > 0)
            .sort((a, b) => {
              const s = scoreEntry(b, q_) - scoreEntry(a, q_);
              if (s !== 0) return s;
              return (a.meta.name ?? "").localeCompare(b.meta.name ?? "");
            })
        : actionEntries,
    [actionEntries, q_],
  );
  // One pass over actionEntries grouped by category, instead of one filter
  // pass per ACTION_CATEGORIES entry -- avoids re-scanning the whole list
  // once per category on every render of the (non-searching) browsing view.
  const actionsByCategory = useMemo(() => {
    const groups = new Map<string, ActionEntry[]>();
    for (const e of actionEntries) {
      const cat = e.category ?? "";
      const group = groups.get(cat);
      if (group) group.push(e);
      else groups.set(cat, [e]);
    }
    return groups;
  }, [actionEntries]);

  // Reflow the item list into two columns once the panel is wide enough.
  //
  // A numeric width is authoritative -- the resizable desktop column knows
  // exactly how wide it is, so it needs no measuring and never flashes.
  //
  // A non-numeric width means the panel is filling its container (the bottom
  // sheet), and that has to be MEASURED rather than assumed. An earlier
  // version hardcoded two columns here on the reasoning that no desktop
  // window gets near PALETTE_TWO_COL_MIN -- which stopped being true once
  // read-only became device-based: a laptop with a 400px-wide window is now
  // an editor whose palette renders in the sheet at 400px, and two columns
  // there squeezes every row.
  const cols =
    typeof width === "number"
      ? width >= PALETTE_TWO_COL_MIN
        ? 2
        : 1
      : measuredWidth !== null && measuredWidth >= PALETTE_TWO_COL_MIN
        ? 2
        : 1;

  // FLIP: when the column count changes, glide each item from its old position
  // to its new one instead of letting the grid snap. Rects are captured every
  // render so the frame just before the reflow is the animation's start point.
  const listRef = useRef<HTMLDivElement | null>(null);
  const prevRects = useRef<DOMRect[]>([]);
  const prevCols = useRef(cols);
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const items = Array.from(list.children) as HTMLElement[];
    const newRects = items.map((el) => el.getBoundingClientRect());

    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    if (prevCols.current !== cols && !reduced) {
      items.forEach((el, i) => {
        const oldR = prevRects.current[i];
        const newR = newRects[i];
        if (!oldR || !newR) return;
        const dx = oldR.left - newR.left;
        const dy = oldR.top - newR.top;
        if (dx || dy) {
          el.animate(
            [
              { transform: `translate(${dx}px, ${dy}px)` },
              { transform: "translate(0, 0)" },
            ],
            { duration: 280, easing: "cubic-bezier(0.22, 1, 0.36, 1)" },
          );
        }
      });
    }
    prevRects.current = newRects;
    prevCols.current = cols;
  });

  return (
    <div
      ref={rootRef}
      style={{
        width,
        flexShrink: 0,
        borderRight: "1px solid var(--border)",
        background: "var(--bg-elev-1)",
        display: "flex",
        flexDirection: "column",
        height: "100%",
        overflow: "hidden",
      }}
    >
      <div style={{ padding: "14px 14px 8px" }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 8,
            marginBottom: 10,
          }}
        >
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
              color: "var(--fg-dim)",
            }}
          >
            library
          </span>
          {onCollapse && (
            <button
              type="button"
              onClick={onCollapse}
              title="Collapse the library"
              aria-label="Collapse the library"
              style={{
                width: 22,
                height: 22,
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
                background: "transparent",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-1)",
                color: "var(--fg-muted)",
                cursor: "pointer",
                fontSize: 12,
                lineHeight: 1,
              }}
            >
              ‹
            </button>
          )}
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, 1fr)",
            gap: 4,
            background: "var(--bg)",
            padding: 3,
            borderRadius: "var(--r-2)",
            border: "1px solid var(--border)",
          }}
        >
          {PALETTE_TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id as TabId)}
              style={{
                height: 26,
                border: "none",
                cursor: "pointer",
                background: tab === t.id ? "var(--bg-elev-3)" : "transparent",
                color: tab === t.id ? "var(--fg)" : "var(--fg-muted)",
                borderRadius: 5,
                fontSize: 11,
                fontWeight: 500,
                fontFamily: "var(--font-sans)",
              }}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      <div style={{ padding: "6px 14px 10px" }}>
        <div style={{ position: "relative" }}>
          <span
            style={{
              position: "absolute",
              left: 10,
              top: 10,
              color: "var(--fg-dim)",
            }}
          >
            <IconSearch size={12} />
          </span>
          <input
            style={{
              height: 32,
              paddingLeft: 30,
              paddingRight: 10,
              width: "100%",
              background: "var(--bg-elev-2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-2)",
              color: "var(--fg)",
              fontFamily: "var(--font-sans)",
              fontSize: 12,
              outline: "none",
            }}
            placeholder={`search ${tabDef.label.toLowerCase()}…`}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        {isActions && (
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--fg-dim)",
              marginTop: 6,
              paddingLeft: 2,
            }}
          >
            {q_
              ? `${actionSearched.length} of ${actionEntries.length} connectors`
              : `${actionEntries.length} connectors`}
          </div>
        )}
      </div>

      <div
        ref={listRef}
        style={{
          padding: "4px 10px",
          display: "grid",
          gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
          alignContent: "start",
          gap: 6,
          overflowY: "auto",
          flex: 1,
        }}
      >
        {/* Create row -- always spans the full width, above the grid */}
        <div style={{ gridColumn: "1 / -1" }}>
          <CreateRow
            meta={CREATE_META[tab]}
            onDragStart={(e) => onDragNodeStart(e, CREATE_META[tab])}
            onActivate={
              onAddNode ? () => onAddNode(CREATE_META[tab]) : undefined
            }
            label={(CREATE_META[tab].label ?? "node") as string}
            isX402={tab === "x402"}
          />
        </div>

        {isActions
          ? q_
            ? // Searching: flat, ranked, matched text highlighted.
              actionSearched.map((e, i) => {
                const title = (e.meta.name ?? e.meta.label ?? "") as string;
                const sub = (e.meta.sub ?? "") as string;
                return (
                  <DraggableRow
                    key={i}
                    icon={(e.meta.icon ?? "") as string}
                    template={(e.meta.template ?? "") as string}
                    title={title}
                    titleNode={highlightMatch(title, q_)}
                    sub={sub}
                    subNode={highlightMatch(sub, q_)}
                    dotColor={tabDef.dotColor}
                    onDragStart={(e2) => onDragNodeStart(e2, e.meta)}
                    onActivate={onAddNode ? () => onAddNode(e.meta) : undefined}
                    label={(e.meta.name ?? e.meta.label ?? "node") as string}
                  />
                );
              })
            : // Browsing: grouped under category headers, mirroring the
              // backend's own connectors_{messaging,productivity,...}.go split.
              // Note: each group renders as one grid-spanning wrapper so its
              // header never breaks the column flow -- as a side effect, the
              // column-reflow FLIP animation above glides per-group rather
              // than per-row while this view is showing (every other tab, and
              // the searching view above, keep the original per-row glide).
              ACTION_CATEGORIES.map((cat) => {
                const group = actionsByCategory.get(cat) ?? [];
                if (group.length === 0) return null;
                return (
                  <div key={cat} style={{ gridColumn: "1 / -1" }}>
                    <div
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 10,
                        textTransform: "uppercase",
                        letterSpacing: "0.08em",
                        color: "var(--fg-dim)",
                        padding: "10px 2px 6px",
                      }}
                    >
                      {cat} · {group.length}
                    </div>
                    <div
                      style={{
                        display: "grid",
                        gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
                        gap: 6,
                      }}
                    >
                      {group.map((e, i) => (
                        <DraggableRow
                          key={i}
                          icon={(e.meta.icon ?? "") as string}
                          template={(e.meta.template ?? "") as string}
                          title={(e.meta.name ?? e.meta.label ?? "") as string}
                          sub={(e.meta.sub ?? "") as string}
                          dotColor={tabDef.dotColor}
                          onDragStart={(e2) => onDragNodeStart(e2, e.meta)}
                          onActivate={
                            onAddNode ? () => onAddNode(e.meta) : undefined
                          }
                          label={
                            (e.meta.name ?? e.meta.label ?? "node") as string
                          }
                        />
                      ))}
                    </div>
                  </div>
                );
              })
          : filtered.map((it, i) => (
              <DraggableRow
                key={i}
                icon={(it.icon ?? "") as string}
                template={(it.template ?? "") as string}
                title={(it.name ?? it.label ?? "") as string}
                sub={(it.sub ?? "") as string}
                dotColor={tabDef.dotColor}
                onDragStart={(e) => onDragNodeStart(e, it)}
                onActivate={onAddNode ? () => onAddNode(it) : undefined}
                label={(it.name ?? it.label ?? "node") as string}
              />
            ))}

        {((isActions && q_ && actionSearched.length === 0) ||
          (!isActions && filtered.length === 0)) && (
          <div
            style={{
              gridColumn: "1 / -1",
              padding: "24px 8px",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--fg-dim)",
              textAlign: "center",
            }}
          >
            no presets, drag the + above to build your own
          </div>
        )}
      </div>

      <div
        style={{
          marginTop: "auto",
          padding: 14,
          borderTop: "1px solid var(--border)",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--fg-dim)",
          lineHeight: 1.5,
        }}
      >
        drag onto canvas →<br />
        wires snap to compatible ports.
      </div>
    </div>
  );
}

function CreateRow({
  meta,
  onDragStart,
  onActivate,
  label,
  isX402,
}: {
  meta: Partial<WorkflowNode>;
  onDragStart: (e: React.DragEvent) => void;
  onActivate?: () => void;
  label?: string;
  isX402: boolean;
}) {
  const accent = isX402 ? "#E879F9" : "var(--accent)";
  const bg = isX402 ? "rgba(232, 121, 249, 0.06)" : "var(--accent-soft)";
  const bgHover = isX402 ? "rgba(232, 121, 249, 0.12)" : "var(--accent-soft)";

  return (
    <div
      draggable
      onDragStart={onDragStart}
      role={onActivate ? "button" : undefined}
      tabIndex={onActivate ? 0 : undefined}
      aria-label={onActivate && label ? `Add ${label}` : undefined}
      onClick={onActivate}
      onKeyDown={(e) => {
        if (!onActivate) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onActivate();
        }
      }}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "9px 10px",
        background: bg,
        border: `1px dashed ${accent}`,
        borderRadius: "var(--r-2)",
        cursor: "grab",
      }}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLElement).style.background = bgHover;
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.background = bg;
      }}
    >
      <span
        style={{
          width: 22,
          height: 22,
          borderRadius: 6,
          background: "var(--bg)",
          color: accent,
          border: `1px solid ${accent}`,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 14,
          flexShrink: 0,
          fontWeight: 600,
        }}
      >
        +
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: accent }}>
          {(meta.name ?? meta.label) as string}
        </div>
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--fg-muted)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {meta.sub as string}
        </div>
      </div>
    </div>
  );
}

function DraggableRow({
  icon,
  template,
  title,
  titleNode,
  sub,
  subNode,
  dotColor,
  onDragStart,
  onActivate,
  label,
}: {
  icon: string;
  template?: string;
  title: string;
  // Optional pre-rendered override for title/sub, used by the Actions tab's
  // search view to show the matched substring highlighted. Falls back to the
  // plain string every other caller already passes.
  titleNode?: React.ReactNode;
  sub: string;
  subNode?: React.ReactNode;
  dotColor: "mute" | "accent" | "magenta" | "info";
  onDragStart: (e: React.DragEvent) => void;
  onActivate?: () => void;
  label?: string;
}) {
  const dotBg =
    dotColor === "magenta"
      ? "rgba(232, 121, 249, 0.14)"
      : dotColor === "accent"
        ? "var(--accent-soft)"
        : dotColor === "info"
          ? "var(--info-soft)"
          : "var(--bg-elev-3)";
  const dotFg =
    dotColor === "magenta"
      ? "#E879F9"
      : dotColor === "accent"
        ? "var(--accent)"
        : dotColor === "info"
          ? "var(--info)"
          : "var(--fg)";

  return (
    <div
      draggable
      onDragStart={onDragStart}
      role={onActivate ? "button" : undefined}
      tabIndex={onActivate ? 0 : undefined}
      aria-label={onActivate && label ? `Add ${label}` : undefined}
      onClick={onActivate}
      onKeyDown={(e) => {
        if (!onActivate) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onActivate();
        }
      }}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "8px 10px",
        background: "var(--bg-elev-2)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-2)",
        cursor: "grab",
      }}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLElement).style.borderColor =
          "var(--border-strong)";
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.borderColor = "var(--border)";
      }}
    >
      <span
        style={{
          width: 22,
          height: 22,
          borderRadius: 6,
          background: dotBg,
          color: dotFg,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 12,
          flexShrink: 0,
          fontWeight: 600,
        }}
      >
        <BrandLogo template={template} fallback={icon} size={14} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, fontWeight: 500, color: "var(--fg)" }}>
          {titleNode ?? title}
        </div>
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--fg-muted)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {subNode ?? sub}
        </div>
      </div>
    </div>
  );
}
