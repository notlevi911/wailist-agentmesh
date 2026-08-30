"use client";
import { useState, useRef, useLayoutEffect } from "react";
import { WorkflowNode } from "@/lib/types";
import { BrandLogo } from "./nodes/brandLogos";
import { PALETTE_TWO_COL_MIN } from "./panelSizing";
import {
  TRIGGER_TEMPLATES,
  AGENT_TEMPLATES,
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  TOOL402_TEMPLATES,
  ACTION_TEMPLATES,
  STATE_TEMPLATES,
  END_TEMPLATES,
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
    id: "x402",
    label: "x402",
    items: () => TOOL402_TEMPLATES,
    type: "tool402",
    dotColor: "magenta" as const,
    map: (it: (typeof TOOL402_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "tool402",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: `${it.provider} · ${it.price} / ${it.unit}`,
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
  actions: {
    type: "action",
    custom: true,
    name: "Custom Action",
    icon: "✦",
    sub: "your own action",
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
  width?: number;
}

export function PalettePanel({
  onDragNodeStart,
  width = 280,
}: PalettePanelProps) {
  const [tab, setTab] = useState<TabId>("triggers");
  const [q, setQ] = useState("");

  const tabDef = PALETTE_TABS.find((t) => t.id === tab)!;
  const items = tabDef.items() as unknown[];
  const mapped = (items as Parameters<typeof tabDef.map>[0][]).map(
    tabDef.map as (
      it: Parameters<typeof tabDef.map>[0],
    ) => Partial<WorkflowNode>,
  );
  const filtered = mapped.filter(
    (i) =>
      ((i.name ?? i.label ?? "") as string)
        .toLowerCase()
        .includes(q.toLowerCase()) ||
      (i.sub ?? "").toLowerCase().includes(q.toLowerCase()),
  );

  // Reflow the item list into two columns once the panel is dragged wide.
  const cols = width >= PALETTE_TWO_COL_MIN ? 2 : 1;

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
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            color: "var(--fg-dim)",
            marginBottom: 10,
          }}
        >
          library
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
        {/* Create row — always spans the full width, above the grid */}
        <div style={{ gridColumn: "1 / -1" }}>
          <CreateRow
            meta={CREATE_META[tab]}
            onDragStart={(e) => onDragNodeStart(e, CREATE_META[tab])}
            isX402={tab === "x402"}
          />
        </div>

        {filtered.map((it, i) => (
          <DraggableRow
            key={i}
            icon={(it.icon ?? "") as string}
            template={(it.template ?? "") as string}
            title={(it.name ?? it.label ?? "") as string}
            sub={(it.sub ?? "") as string}
            dotColor={tabDef.dotColor}
            onDragStart={(e) => onDragNodeStart(e, it)}
          />
        ))}

        {filtered.length === 0 && (
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
            no presets — drag the + above to build your own
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
  isX402,
}: {
  meta: Partial<WorkflowNode>;
  onDragStart: (e: React.DragEvent) => void;
  isX402: boolean;
}) {
  const accent = isX402 ? "#E879F9" : "var(--accent)";
  const bg = isX402 ? "rgba(232, 121, 249, 0.06)" : "var(--accent-soft)";
  const bgHover = isX402 ? "rgba(232, 121, 249, 0.12)" : "var(--accent-soft)";

  return (
    <div
      draggable
      onDragStart={onDragStart}
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
  sub,
  dotColor,
  onDragStart,
}: {
  icon: string;
  template?: string;
  title: string;
  sub: string;
  dotColor: "mute" | "accent" | "magenta" | "info";
  onDragStart: (e: React.DragEvent) => void;
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
          {title}
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
          {sub}
        </div>
      </div>
    </div>
  );
}
