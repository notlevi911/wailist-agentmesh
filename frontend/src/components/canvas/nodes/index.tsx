"use client";
import React from "react";
import { WorkflowNode, PortName } from "@/lib/types";
import { BrandLogo } from "./brandLogos";
import {
  NODE_TYPES,
  TRIGGER_TEMPLATES,
  AGENT_TEMPLATES,
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  TOOL402_TEMPLATES,
  ACTION_TEMPLATES,
  STATE_TEMPLATES,
  END_TEMPLATES,
} from "@/lib/data";
import { Pill } from "@/components/ui";

interface NodeProps {
  node: WorkflowNode;
  selected: boolean;
  deployed: boolean;
  onMouseDown: (e: React.MouseEvent) => void;
  onStartWire: (e: React.MouseEvent, port: PortName) => void;
  onPortHover: (port: PortName) => void;
  onPortLeave: () => void;
  attachedSummary?: { model: string | null; tools: number };
}

export function CanvasNode(props: NodeProps) {
  switch (props.node.type) {
    case "trigger":
      return <TriggerNode {...props} />;
    case "agent":
      return <AgentNode {...props} />;
    case "provider":
      return <ProviderNode {...props} />;
    case "tool":
      return <ToolNode {...props} />;
    case "tool402":
      return <Tool402Node {...props} />;
    case "action":
      return <ActionNode {...props} />;
    case "state":
      return <StateNode {...props} />;
    case "end":
      return <EndNode {...props} />;
    default:
      return null;
  }
}

// ── Shell ──────────────────────────────────────────────────────────────────
function NodeShell({
  node,
  selected,
  onMouseDown,
  W,
  H,
  accent,
  strong = false,
  dashed = false,
  children,
}: {
  node: WorkflowNode;
  selected: boolean;
  onMouseDown: (e: React.MouseEvent) => void;
  W: number;
  H: number;
  accent: string;
  strong?: boolean;
  dashed?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      data-node={node.id}
      onMouseDown={onMouseDown}
      style={{
        position: "absolute",
        top: node.y,
        left: node.x,
        width: W,
        minHeight: H,
        background: "var(--bg-elev-2)",
        border: dashed
          ? `1px dashed ${accent}`
          : `1px solid ${selected ? accent : "var(--border-strong)"}`,
        borderRadius: strong ? 10 : 8,
        boxShadow: selected
          ? `0 0 0 3px color-mix(in oklab, ${accent} 22%, transparent), 0 12px 30px rgba(0,0,0,0.5)`
          : "0 4px 14px rgba(0,0,0,0.35)",
        cursor: "grab",
        transition: "box-shadow .12s, border-color .12s",
        userSelect: "none",
      }}
    >
      {children}
    </div>
  );
}

function NodeHeader({
  icon,
  template,
  iconBg,
  iconColor,
  kicker,
  title,
  sub,
}: {
  icon: string;
  template?: string;
  iconBg: string;
  iconColor: string;
  kicker: string;
  title: string;
  sub?: string;
}) {
  return (
    <div
      style={{
        padding: "10px 12px",
        display: "flex",
        alignItems: "center",
        gap: 10,
      }}
    >
      <span
        style={{
          width: 24,
          height: 24,
          borderRadius: 5,
          background: iconBg,
          color: iconColor,
          border: "1px solid var(--border-strong)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 12,
          flexShrink: 0,
        }}
      >
        <BrandLogo template={template} fallback={icon} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 9.5,
            color: "var(--fg-dim)",
            textTransform: "uppercase",
            letterSpacing: "0.08em",
          }}
        >
          {kicker}
        </div>
        <div
          style={{
            fontSize: 12.5,
            fontWeight: 500,
            color: "var(--fg)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {title}
        </div>
        {sub && (
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 9.5,
              color: "var(--fg-dim)",
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
              marginTop: 1,
            }}
          >
            {sub}
          </div>
        )}
      </div>
    </div>
  );
}

// Port components
function SidePort({
  side,
  color,
  node,
  port,
  top,
  onHover,
  onLeave,
  onMouseDown,
}: {
  side: "left" | "right";
  color: string;
  node: WorkflowNode;
  port: PortName;
  top?: number;
  onHover: () => void;
  onLeave: () => void;
  onMouseDown?: (e: React.MouseEvent) => void;
}) {
  return (
    <div
      data-port={`${port}:${node.id}`}
      onMouseEnter={onHover}
      onMouseLeave={onLeave}
      onMouseDown={onMouseDown}
      style={{
        position: "absolute",
        top: top != null ? top : "50%",
        transform: top != null ? "translateY(-50%)" : "translateY(-50%)",
        [side]: -7,
        width: 14,
        height: 14,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        cursor: onMouseDown ? "crosshair" : "pointer",
        zIndex: 2,
      }}
    >
      <span
        style={{
          width: 10,
          height: 10,
          borderRadius: 999,
          background: "var(--bg)",
          border: `1.5px solid ${color}`,
        }}
      />
    </div>
  );
}

function TopPort({
  color,
  node,
  port,
  onHover,
  onLeave,
  onMouseDown,
}: {
  color: string;
  node: WorkflowNode;
  port: PortName;
  onHover: () => void;
  onLeave: () => void;
  onMouseDown?: (e: React.MouseEvent) => void;
}) {
  return (
    <div
      data-port={`${port}:${node.id}`}
      onMouseEnter={onHover}
      onMouseLeave={onLeave}
      onMouseDown={onMouseDown}
      style={{
        position: "absolute",
        top: -7,
        left: "50%",
        transform: "translateX(-50%)",
        width: 14,
        height: 14,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        cursor: onMouseDown ? "crosshair" : "pointer",
        zIndex: 2,
      }}
    >
      <span
        style={{
          width: 10,
          height: 10,
          borderRadius: 999,
          background: "var(--bg)",
          border: `1.5px solid ${color}`,
        }}
      />
    </div>
  );
}

function BottomPort({
  x,
  color,
  node,
  port,
  onHover,
  onLeave,
}: {
  x: string;
  color: string;
  node: WorkflowNode;
  port: PortName;
  onHover: () => void;
  onLeave: () => void;
}) {
  return (
    <div
      data-port={`${port}:${node.id}`}
      onMouseEnter={onHover}
      onMouseLeave={onLeave}
      style={{
        position: "absolute",
        bottom: -7,
        left: x,
        transform: "translateX(-50%)",
        width: 14,
        height: 14,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        cursor: "pointer",
        zIndex: 2,
      }}
    >
      <span
        style={{
          width: 10,
          height: 10,
          borderRadius: 999,
          background: "var(--bg)",
          border: `1.5px solid ${color}`,
        }}
      />
    </div>
  );
}

// ── Trigger ────────────────────────────────────────────────────────────────
function TriggerNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.trigger;
  const tpl = TRIGGER_TEMPLATES.find((x) => x.id === node.template);
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--fg-muted)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "▶"}
        template={node.template}
        iconBg="var(--bg-elev-3)"
        iconColor="var(--fg)"
        kicker="trigger"
        title={tpl?.name ?? node.label ?? node.name ?? "Trigger"}
        sub={node.sub ?? tpl?.desc}
      />
      <SidePort
        side="right"
        color="var(--fg)"
        node={node}
        port="out"
        onHover={() => onPortHover("out")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "out")}
      />
    </NodeShell>
  );
}

// ── Agent ──────────────────────────────────────────────────────────────────
function AgentNode({
  node,
  selected,
  deployed,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
  attachedSummary,
}: NodeProps) {
  const t = NODE_TYPES.agent;
  const tpl =
    AGENT_TEMPLATES.find((x) => x.id === node.template) ?? AGENT_TEMPLATES[0];

  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--accent)"
      strong
    >
      {/* Header */}
      <div
        style={{
          padding: "12px 14px 10px",
          display: "flex",
          alignItems: "center",
          gap: 10,
          borderBottom: "1px solid var(--border-soft)",
        }}
      >
        <span
          style={{
            width: 26,
            height: 26,
            borderRadius: 6,
            background: "var(--accent-soft)",
            color: "var(--accent)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 13,
            flexShrink: 0,
          }}
        >
          <BrandLogo
            template={node.template}
            fallback={node.icon ?? tpl.icon}
            size={16}
          />
        </span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 9.5,
              color: "var(--accent)",
              textTransform: "uppercase",
              letterSpacing: "0.08em",
            }}
          >
            AI Agent
          </div>
          <div
            style={{
              fontSize: 13,
              fontWeight: 500,
              color: "var(--fg)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {node.name}
          </div>
        </div>
        {deployed && (
          <Pill mono tone="ok" dot>
            live
          </Pill>
        )}
      </div>

      {/* Status row. Agents have no wallet of their own: paid tool calls are
          funded by the platform wallets and metered against the user's
          credits, so there is no per-agent address or balance to show. */}
      <div
        style={{
          padding: "8px 14px 10px",
          display: "flex",
          alignItems: "center",
          gap: 8,
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          minWidth: 0,
          overflow: "hidden",
        }}
      >
        <span style={{ color: "var(--fg-dim)", fontSize: 9.5 }}>
          {deployed ? "live · paid calls billed to credits" : "not deployed"}
        </span>
      </div>

      {/* Sub-port labels */}
      <div
        style={{
          position: "absolute",
          bottom: -22,
          left: 0,
          right: 0,
          display: "flex",
          pointerEvents: "none",
          padding: "0 8px",
        }}
      >
        <SubPortLabel
          label="Model"
          x={0.28}
          filled={!!attachedSummary?.model}
          hint={attachedSummary?.model ?? "none"}
        />
        <SubPortLabel
          label="Tools"
          x={0.72}
          filled={(attachedSummary?.tools ?? 0) > 0}
          hint={
            attachedSummary?.tools
              ? `${attachedSummary.tools} tool${attachedSummary.tools > 1 ? "s" : ""}`
              : "none"
          }
        />
      </div>

      <SidePort
        side="left"
        color="var(--accent)"
        node={node}
        port="in"
        onHover={() => onPortHover("in")}
        onLeave={onPortLeave}
        top={38}
      />
      <SidePort
        side="right"
        color="var(--accent)"
        node={node}
        port="out"
        onHover={() => onPortHover("out")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "out")}
        top={38}
      />
      <BottomPort
        x="28%"
        color="var(--accent)"
        node={node}
        port="model"
        onHover={() => onPortHover("model")}
        onLeave={onPortLeave}
      />
      <BottomPort
        x="72%"
        color="var(--accent)"
        node={node}
        port="tools"
        onHover={() => onPortHover("tools")}
        onLeave={onPortLeave}
      />
    </NodeShell>
  );
}

function SubPortLabel({
  label,
  x,
  filled,
  hint,
}: {
  label: string;
  x: number;
  filled: boolean;
  hint: string;
}) {
  return (
    <div
      style={{
        position: "absolute",
        left: `calc(${x * 100}% - 36px)`,
        top: 22,
        width: 72,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 1,
        fontFamily: "var(--font-mono)",
        fontSize: 9,
        textTransform: "uppercase",
        letterSpacing: "0.08em",
      }}
    >
      <span style={{ color: filled ? "var(--accent)" : "var(--fg-dim)" }}>
        {label}
      </span>
      <span
        style={{
          color: "var(--fg-dim)",
          fontSize: 8.5,
          textTransform: "none",
          letterSpacing: 0,
        }}
      >
        {hint}
      </span>
    </div>
  );
}

// ── Provider ───────────────────────────────────────────────────────────────
function ProviderNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.provider;
  const tpl = PROVIDER_TEMPLATES.find((x) => x.id === node.template);
  const hasKey = !!node.apiKey;
  // Always a fixed mask, never a slice of the raw value — the canvas node is
  // visible in screen shares/recordings, unlike the Inspector's password
  // input, so no characters of an unsaved key should ever render here.
  const maskedKey = hasKey ? "•".repeat(14) : null;
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--accent)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "+"}
        template={node.template}
        iconBg="var(--bg-elev-3)"
        iconColor="var(--accent)"
        kicker="ai provider"
        title={node.name ?? tpl?.name ?? "Provider"}
        sub={node.model ?? tpl?.model}
      />
      <div
        style={{
          padding: "4px 12px 10px",
          display: "flex",
          alignItems: "center",
          gap: 6,
          fontFamily: "var(--font-mono)",
          fontSize: 10,
        }}
      >
        <span style={{ color: "var(--fg-dim)" }}>key</span>
        {hasKey ? (
          <>
            <span
              style={{
                flex: 1,
                color: "var(--fg-muted)",
                letterSpacing: "0.04em",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {maskedKey}
            </span>
            <span style={{ color: "#4ade80", fontSize: 9, flexShrink: 0 }}>
              ✓
            </span>
          </>
        ) : (
          <span style={{ color: "var(--fg-dim)", fontStyle: "italic" }}>
            not set
          </span>
        )}
      </div>
      <TopPort
        color="var(--accent)"
        node={node}
        port="top"
        onHover={() => onPortHover("top")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "top")}
      />
    </NodeShell>
  );
}

// ── Tool (standard) ────────────────────────────────────────────────────────
function ToolNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.tool;
  const tpl = TOOL_TEMPLATES.find((x) => x.id === node.template);
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--fg-muted)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "⟶"}
        template={node.template}
        iconBg="var(--bg-elev-3)"
        iconColor="var(--fg)"
        kicker="tool · standard"
        title={node.name ?? tpl?.name ?? "Tool"}
        sub={node.sub ?? tpl?.desc}
      />
      <TopPort
        color="var(--fg)"
        node={node}
        port="top"
        onHover={() => onPortHover("top")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "top")}
      />
    </NodeShell>
  );
}

// ── Tool (x402 paywalled) ──────────────────────────────────────────────────
function Tool402Node({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.tool402;
  const tpl = TOOL402_TEMPLATES.find((x) => x.id === node.template);
  const magenta = "#E879F9";
  const name = node.name ?? tpl?.name ?? "x402 Tool";
  const provider = node.provider ?? tpl?.provider ?? "";
  const price = node.price ?? tpl?.price;
  const unit = node.unit ?? tpl?.unit ?? "call";
  const icon = node.icon ?? tpl?.icon ?? "✦";

  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent={magenta}
      dashed
    >
      <div
        style={{
          padding: "10px 12px 6px",
          display: "flex",
          alignItems: "center",
          gap: 10,
        }}
      >
        <span
          style={{
            width: 24,
            height: 24,
            borderRadius: 5,
            background: "rgba(232, 121, 249, 0.14)",
            color: magenta,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 13,
            flexShrink: 0,
            fontWeight: 600,
          }}
        >
          <BrandLogo template={node.template} fallback={icon} />
        </span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 9.5,
              color: magenta,
              textTransform: "uppercase",
              letterSpacing: "0.08em",
            }}
          >
            x402 · paid tool
          </div>
          <div
            style={{
              fontSize: 13,
              fontWeight: 500,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {name}
          </div>
        </div>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 9,
            padding: "2px 6px",
            borderRadius: 999,
            border: `1px solid ${magenta}`,
            color: magenta,
            textTransform: "uppercase",
            letterSpacing: "0.08em",
          }}
        >
          $
        </span>
      </div>
      <div
        style={{
          padding: "4px 12px 10px",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
        }}
      >
        <span
          style={{
            color: "var(--fg-dim)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
            maxWidth: 120,
          }}
        >
          {provider}
        </span>
        {price != null ? (
          <span style={{ color: magenta }}>
            {price}
            <span style={{ color: "var(--fg-dim)" }}>
              {" "}
              {node.asset ?? "USDC"} / {unit}
            </span>
          </span>
        ) : (
          <span style={{ color: "var(--fg-dim)" }}>price — set endpoint</span>
        )}
      </div>
      <TopPort
        color={magenta}
        node={node}
        port="top"
        onHover={() => onPortHover("top")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "top")}
      />
      <SidePort
        side="left"
        color={magenta}
        node={node}
        port="in"
        onHover={() => onPortHover("in")}
        onLeave={onPortLeave}
      />
      <SidePort
        side="right"
        color={magenta}
        node={node}
        port="out"
        onHover={() => onPortHover("out")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "out")}
      />
    </NodeShell>
  );
}

// ── Action ─────────────────────────────────────────────────────────────────
function ActionNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.action;
  const tpl = ACTION_TEMPLATES.find((x) => x.id === node.template);
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--fg-muted)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "✦"}
        template={node.template}
        iconBg="var(--bg-elev-3)"
        iconColor="var(--fg)"
        kicker="action"
        title={node.name ?? tpl?.name ?? "Action"}
        sub={node.sub ?? tpl?.desc}
      />
      <SidePort
        side="left"
        color="var(--fg)"
        node={node}
        port="in"
        onHover={() => onPortHover("in")}
        onLeave={onPortLeave}
      />
      <SidePort
        side="right"
        color="var(--fg)"
        node={node}
        port="out"
        onHover={() => onPortHover("out")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "out")}
      />
    </NodeShell>
  );
}

// ── State ──────────────────────────────────────────────────────────────────
// Blue rather than the accent violet of agent/provider or the magenta of
// x402: state is the one node that touches neither the model nor money, and
// keeping the money-coloured nodes visually exclusive is what lets you spot
// spend on a busy canvas at a glance.
function StateNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
  onStartWire,
}: NodeProps) {
  const t = NODE_TYPES.state;
  const tpl = STATE_TEMPLATES.find((x) => x.id === node.template);
  // The key is the useful thing at a glance -- "which value does this touch"
  // -- so it wins the subtitle over the template's generic description.
  const sub = node.stateKey
    ? `${node.stateOp ?? tpl?.id ?? "get"} · ${node.stateKey}`
    : (node.sub ?? tpl?.desc);
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--info)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "▤"}
        template={node.template}
        iconBg="var(--info-soft)"
        iconColor="var(--info)"
        kicker="state"
        title={node.name ?? tpl?.name ?? "State"}
        sub={sub}
      />
      <SidePort
        side="left"
        color="var(--fg)"
        node={node}
        port="in"
        onHover={() => onPortHover("in")}
        onLeave={onPortLeave}
      />
      <SidePort
        side="right"
        color="var(--fg)"
        node={node}
        port="out"
        onHover={() => onPortHover("out")}
        onLeave={onPortLeave}
        onMouseDown={(e) => onStartWire(e, "out")}
      />
    </NodeShell>
  );
}

// ── End ────────────────────────────────────────────────────────────────────
function EndNode({
  node,
  selected,
  onMouseDown,
  onPortHover,
  onPortLeave,
}: NodeProps) {
  const t = NODE_TYPES.end;
  const tpl = END_TEMPLATES.find((x) => x.id === node.template);
  return (
    <NodeShell
      node={node}
      selected={selected}
      onMouseDown={onMouseDown}
      W={t.w}
      H={t.h}
      accent="var(--fg-muted)"
    >
      <NodeHeader
        icon={node.icon ?? tpl?.icon ?? "■"}
        template={node.template}
        iconBg="var(--bg-elev-3)"
        iconColor="var(--fg)"
        kicker="end"
        title={tpl?.name ?? node.label ?? node.name ?? "End"}
        sub={node.label ?? tpl?.desc}
      />
      <SidePort
        side="left"
        color="var(--fg)"
        node={node}
        port="in"
        onHover={() => onPortHover("in")}
        onLeave={onPortLeave}
      />
    </NodeShell>
  );
}
