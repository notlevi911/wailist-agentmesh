"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { WorkflowNode, CustomParam } from "@/lib/types";
import {
  base64DecodedBytes,
  formatFileSize,
  readFileAsBase64,
} from "@/lib/fileEncoding";
import {
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  TRIGGER_TEMPLATES,
  ACTION_TEMPLATES,
  STATE_TEMPLATES,
  END_TEMPLATES,
  AGENT_TEMPLATES,
  TENDRIL_TEMPLATES,
  GOOGLE_TEMPLATES,
  modelTier,
  TIER_FEES,
} from "@/lib/data";
import { IconClose, StatusDot } from "@/components/ui";
import { BrandLogo } from "./nodes/brandLogos";
import { can } from "@/lib/readonly";
import { iconBtn } from "@/components/ui/buttons";
import { useReadOnly } from "@/hooks/useReadOnly";
import {
  tools as toolsApi,
  workflows as workflowsApi,
  oauth2,
  OAuthCredentialSummary,
} from "@/lib/api";
import { ConnectorOAuthButton } from "./ConnectorOAuthButton";
import {
  tendril as tendrilApi,
  estimateLeaseHoursCostUSD,
  TendrilMachine,
} from "@/lib/tendril";

interface InspectorProps {
  selected: WorkflowNode | null;
  workflowId: string;
  onUpdate: (n: WorkflowNode) => void;
  onDelete: () => void;
  onClose: () => void;
  width?: number | string;
  /** Rendered inside a host that already draws the rail's left border and its
   *  own "INSPECT" caption (the right rail's tab pane). Drops this component's
   *  own edge chrome + caption so they aren't doubled. */
  embedded?: boolean;
}

export function Inspector({
  selected,
  workflowId,
  onUpdate,
  onDelete,
  onClose,
  width = 320,
  embedded = false,
}: InspectorProps) {
  // Above the early return: a hook after a conditional return is a hook
  // that does not always run.
  const readOnly = useReadOnly();

  if (!selected) return <EmptyInspector width={width} embedded={embedded} />;

  // A viewer gets a description of the node, not its editor -- see
  // ReadOnlyInspector below for why that is a separate component rather
  // than this one with every input disabled.
  if (!can("workflow.editGraph", readOnly)) {
    return (
      <ReadOnlyInspector
        selected={selected}
        onClose={onClose}
        width={width}
        embedded={embedded}
      />
    );
  }

  const meta = nodeMeta(selected);

  return (
    <div
      style={{
        width,
        flexShrink: 0,
        borderLeft: embedded ? undefined : "1px solid var(--border)",
        background: "var(--bg-elev-1)",
        overflow: "auto",
        height: "100%",
      }}
    >
      {/* Header */}
      <div
        style={{
          padding: "14px 16px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span
            style={{
              width: 24,
              height: 24,
              borderRadius: 6,
              background: meta.bg,
              color: meta.fg,
              border: "1px solid var(--border-strong)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              fontSize: 12,
            }}
          >
            <BrandLogo template={selected.template} fallback={meta.icon} />
          </span>
          <div>
            <div style={{ fontSize: 13, fontWeight: 500 }}>{meta.title}</div>
            <div
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--fg-dim)",
              }}
            >
              {selected.type} · {selected.id}
            </div>
          </div>
        </div>
        <button
          onClick={onClose}
          aria-label="Close inspector"
          title="Close"
          style={{
            width: 32,
            height: 32,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "transparent",
            border: "none",
            color: "var(--fg-muted)",
            cursor: "pointer",
            borderRadius: "var(--r-2)",
          }}
        >
          <IconClose size={12} />
        </button>
      </div>

      <div
        style={{
          padding: 16,
          display: "flex",
          flexDirection: "column",
          gap: 18,
        }}
      >
        {selected.type === "agent" && (
          <AgentInspector node={selected} onUpdate={onUpdate} />
        )}
        {selected.type === "provider" && (
          <ProviderInspector node={selected} onUpdate={onUpdate} />
        )}
        {selected.type === "tool" && (
          <ToolInspector
            node={selected}
            workflowId={workflowId}
            onUpdate={onUpdate}
          />
        )}
        {selected.type === "tool402" && (
          <Tool402Inspector node={selected} onUpdate={onUpdate} />
        )}
        {selected.type === "trigger" && (
          <TriggerInspector
            node={selected}
            onUpdate={onUpdate}
            workflowId={workflowId}
          />
        )}
        {selected.type === "action" && (
          <ActionInspector
            node={selected}
            workflowId={workflowId}
            onUpdate={onUpdate}
          />
        )}
        {selected.type === "state" && (
          <StateInspector
            node={selected}
            onUpdate={onUpdate}
            workflowId={workflowId}
          />
        )}
        {selected.type === "end" && (
          <EndInspector node={selected} onUpdate={onUpdate} />
        )}
        {selected.type === "tendril" && (
          <TendrilInspector node={selected} onUpdate={onUpdate} />
        )}
        {selected.type === "google" && (
          <GoogleInspector node={selected} onUpdate={onUpdate} />
        )}
      </div>

      <div style={{ padding: 16, borderTop: "1px solid var(--border)" }}>
        <button
          onClick={onDelete}
          style={{
            width: "100%",
            height: 36,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 8,
            background: "transparent",
            border: "1px solid var(--danger)",
            borderRadius: "var(--r-2)",
            color: "var(--danger)",
            cursor: "pointer",
            fontFamily: "var(--font-sans)",
            fontSize: 13,
            fontWeight: 500,
          }}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLElement).style.background =
              "rgba(255, 92, 92, 0.08)";
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLElement).style.background = "transparent";
          }}
        >
          <svg
            width="13"
            height="13"
            viewBox="0 0 14 14"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="M2.5 3.5h9M5.5 3.5V2.5h3v1M4 3.5l.5 8h5l.5-8"
              stroke="currentColor"
              strokeWidth="1.2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          Delete node
        </button>
      </div>
    </div>
  );
}

// ── Read-only inspector ───────────────────────────────────────────────────
// What a node's config looks like when the client cannot change it. Not the
// editor with its inputs disabled: a disabled field still renders as a field,
// which reads as "you may type here, later" rather than "this is what it is".
// Values are shown as text, so the panel describes the node instead.

// Secrets are acknowledged, never rendered. The backend already returns a
// "__enc__" sentinel rather than ciphertext, but a plaintext key left on an
// older row must not reach the DOM either.
const SECRET_PLACEHOLDER = "••••••••";

interface ReadOnlyRow {
  label: string;
  value: string;
  multiline?: boolean;
}

function readOnlyRows(n: WorkflowNode): ReadOnlyRow[] {
  const rows: ReadOnlyRow[] = [];
  const push = (label: string, value?: string | null, multiline?: boolean) => {
    const v = (value ?? "").trim();
    if (v) rows.push({ label, value: v, multiline });
  };

  push("Model", n.model);
  push("Key", n.apiKey ? SECRET_PLACEHOLDER : undefined);
  push("Key mode", n.keyMode);
  push("System prompt", n.systemPrompt, true);
  push("URL", n.url);
  push("Method", n.method);
  push("Endpoint", n.endpoint);
  push("Description", n.description, true);
  if (n.price) {
    push("Price", [n.price, n.unit, n.asset].filter(Boolean).join(" "));
  }
  push("Provider", n.provider);
  push("Source", n.source);
  push("To", n.emailTo);
  push("Subject", n.emailSubject);
  push("Action", n.tendrilAction);
  push("Hours", n.tendrilHours);
  push("Amount", n.tendrilAmount);

  for (const [k, v] of Object.entries(n.config ?? {})) push(k, v);
  // Keys only: that a credential is configured is part of understanding the
  // node; what it is is not.
  for (const k of Object.keys(n.secrets ?? {})) {
    rows.push({ label: k, value: SECRET_PLACEHOLDER });
  }
  return rows;
}

function ReadOnlyInspector({
  selected,
  onClose,
  width = 320,
  embedded = false,
}: {
  selected: WorkflowNode;
  onClose: () => void;
  width?: number | string;
  embedded?: boolean;
}) {
  const meta = nodeMeta(selected);
  const rows = readOnlyRows(selected);

  return (
    <div
      style={{
        width,
        flexShrink: 0,
        borderLeft: embedded ? undefined : "1px solid var(--border)",
        background: "var(--bg-elev-1)",
        overflow: "auto",
        height: "100%",
      }}
    >
      <div
        style={{
          padding: "14px 16px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            minWidth: 0,
          }}
        >
          <span
            style={{
              width: 24,
              height: 24,
              borderRadius: 6,
              background: meta.bg,
              color: meta.fg,
              border: "1px solid var(--border-strong)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              fontSize: 12,
              flexShrink: 0,
            }}
          >
            <BrandLogo template={selected.template} fallback={meta.icon} />
          </span>
          <div style={{ minWidth: 0 }}>
            <div
              style={{
                fontSize: 13,
                fontWeight: 500,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {meta.title}
            </div>
            <div
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--fg-dim)",
              }}
            >
              {selected.type} · {selected.id}
            </div>
          </div>
        </div>
        <button
          onClick={onClose}
          aria-label="Close inspector"
          title="Close"
          style={{
            width: 32,
            height: 32,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            flexShrink: 0,
            background: "transparent",
            border: "none",
            color: "var(--fg-dim)",
            cursor: "pointer",
            borderRadius: "var(--r-2)",
          }}
        >
          <IconClose size={13} />
        </button>
      </div>

      <div
        style={{
          padding: 16,
          display: "flex",
          flexDirection: "column",
          gap: 18,
        }}
      >
        {rows.length > 0 ? (
          <Section label="config">
            {rows.map((r) => (
              <div
                key={r.label}
                style={{ display: "flex", flexDirection: "column", gap: 4 }}
              >
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    textTransform: "uppercase",
                    letterSpacing: "0.06em",
                    color: "var(--fg-dim)",
                  }}
                >
                  {r.label}
                </div>
                <div
                  style={{
                    fontSize: 12.5,
                    lineHeight: 1.55,
                    color: "var(--fg)",
                    fontFamily: r.multiline
                      ? "var(--font-sans)"
                      : "var(--font-mono)",
                    whiteSpace: r.multiline ? "pre-wrap" : "normal",
                    wordBreak: "break-word",
                    // Prose wants a readable measure; a lone value does not.
                    maxWidth: r.multiline ? "60ch" : undefined,
                  }}
                >
                  {r.value}
                </div>
              </div>
            ))}
          </Section>
        ) : (
          <div
            style={{ fontSize: 12, lineHeight: 1.6, color: "var(--fg-dim)" }}
          >
            This node has no configuration of its own.
          </div>
        )}

        <div
          style={{
            fontSize: 11.5,
            lineHeight: 1.6,
            color: "var(--fg-dim)",
            borderTop: "1px solid var(--border-soft)",
            paddingTop: 14,
          }}
        >
          Editing happens in the AgentMesh desktop app.
        </div>
      </div>
    </div>
  );
}

function EmptyInspector({
  width = 320,
  embedded = false,
}: {
  width?: number | string;
  embedded?: boolean;
}) {
  return (
    <div
      style={{
        width,
        flexShrink: 0,
        borderLeft: embedded ? undefined : "1px solid var(--border)",
        background: "var(--bg-elev-1)",
        padding: 20,
        display: "flex",
        flexDirection: "column",
        // Without a definite height the inner flex:1 state collapses to
        // content height and jams to the top of a tall rail.
        height: "100%",
        flex: 1,
        minHeight: 0,
      }}
    >
      {!embedded && (
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            color: "var(--fg-dim)",
            marginBottom: 14,
          }}
        >
          inspector
        </div>
      )}
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--fg-dim)",
          textAlign: "center",
          padding: 24,
          fontSize: 12,
          lineHeight: 1.6,
        }}
      >
        <div
          style={{
            width: 40,
            height: 40,
            borderRadius: 999,
            border: "1px dashed var(--border-strong)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            marginBottom: 12,
          }}
        >
          ◇
        </div>
        select a node to inspect
        <br />
        its config + connections.
      </div>
    </div>
  );
}

function nodeMeta(n: WorkflowNode) {
  const tpls: Record<
    string,
    {
      list: { id: string; icon?: string; name?: string }[];
      bg: string;
      fg: string;
    }
  > = {
    trigger: {
      list: TRIGGER_TEMPLATES,
      bg: "var(--bg-elev-3)",
      fg: "var(--fg)",
    },
    agent: {
      list: AGENT_TEMPLATES,
      bg: "var(--accent-soft)",
      fg: "var(--accent)",
    },
    provider: {
      list: PROVIDER_TEMPLATES,
      bg: "var(--bg-elev-3)",
      fg: "var(--accent)",
    },
    tool: { list: TOOL_TEMPLATES, bg: "var(--bg-elev-3)", fg: "var(--fg)" },
    // No preset list -- every x402 node is custom (TOOL402_TEMPLATES removed,
    // see node-cleanup plan Part A1), so tpl below just never matches for it.
    tool402: {
      list: [],
      bg: "rgba(232, 121, 249, 0.14)",
      fg: "#E879F9",
    },
    action: { list: ACTION_TEMPLATES, bg: "var(--bg-elev-3)", fg: "var(--fg)" },
    state: {
      list: STATE_TEMPLATES,
      bg: "var(--info-soft)",
      fg: "var(--info)",
    },
    end: { list: END_TEMPLATES, bg: "var(--bg-elev-3)", fg: "var(--fg)" },
    tendril: {
      list: TENDRIL_TEMPLATES,
      bg: "rgba(232, 121, 249, 0.14)",
      fg: "#E879F9",
    },
  };
  const L = tpls[n.type] ?? tpls.action;
  const tpl = L.list.find((x) => x.id === n.template);
  return {
    icon: n.icon ?? tpl?.icon ?? "◇",
    title: n.name ?? n.label ?? tpl?.name ?? "Custom node",
    bg: L.bg,
    fg: L.fg,
  };
}

// ── Shared ─────────────────────────────────────────────────────────────────
function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
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
        {label}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        {children}
      </div>
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 5 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          fontSize: 11,
          color: "var(--fg-muted)",
        }}
      >
        <span>{label}</span>
        {hint && (
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--fg-dim)",
            }}
          >
            {hint}
          </span>
        )}
      </div>
      {children}
    </label>
  );
}

// ── Generic per-connector fields (Secrets/Config maps) ─────────────────────
function SecretField({
  node,
  onUpdate,
  secretKey,
  label,
  hint,
  placeholder,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
  secretKey: string;
  label: string;
  hint?: string;
  placeholder: string;
}) {
  const val = node.secrets?.[secretKey];
  const isSet = val === "__enc__";
  // "__clear__" is the backend's ClearSentinel (handlers/secrets.go) queued
  // locally by this field's own onChange below, between the user blanking a
  // previously-set field and the next save -- rendered the same as unset
  // (empty box, normal placeholder) rather than leaking the sentinel string
  // itself into the input.
  const isCleared = val === "__clear__";
  return (
    <Field label={label} hint={hint ?? "encrypted at rest"}>
      <input
        style={monoInputStyle}
        type="password"
        value={isSet || isCleared ? "" : (val ?? "")}
        placeholder={isSet ? "Key set, enter to replace" : placeholder}
        onChange={(e) => {
          const typed = e.target.value;
          // onChange only ever fires on a real user edit (an untouched
          // field never calls this, so it's never at risk of clearing a
          // secret the user didn't touch) -- so ending blank always means
          // the user just deleted whatever they'd typed, and should always
          // send the backend's "__clear__" sentinel, never "" ("no change,
          // keep existing"). This used to branch on isSet (only clear if
          // the field was already "__enc__"), which broke on a clear ->
          // retype -> clear-again cycle: after the first clear, val is no
          // longer "__enc__", so isSet goes false, and blanking a second
          // time fell through to "" -- silently keeping the old secret
          // while the UI showed the field as empty. Always-clear-on-blank
          // has no such gap, and is harmless for a field with nothing to
          // clear (ClearSentinel on an already-empty existingEnc is a
          // no-op in encryptField).
          const next = typed || "__clear__";
          onUpdate({
            ...node,
            secrets: { ...node.secrets, [secretKey]: next },
          });
        }}
      />
    </Field>
  );
}

function ConfigField({
  node,
  onUpdate,
  configKey,
  label,
  hint,
  placeholder,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
  configKey: string;
  label: string;
  hint?: string;
  placeholder?: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <input
        style={monoInputStyle}
        value={node.config?.[configKey] ?? ""}
        placeholder={placeholder}
        onChange={(e) =>
          onUpdate({
            ...node,
            config: { ...node.config, [configKey]: e.target.value },
          })
        }
      />
    </Field>
  );
}

const inputStyle: React.CSSProperties = {
  height: 36,
  padding: "0 10px",
  width: "100%",
  background: "var(--bg)",
  border: "1px solid var(--border)",
  borderRadius: "var(--r-2)",
  color: "var(--fg)",
  fontSize: 12,
  fontFamily: "var(--font-sans)",
  outline: "none",
};

const monoInputStyle: React.CSSProperties = {
  ...inputStyle,
  fontFamily: "var(--font-mono)",
  fontSize: 11,
};

// Add/remove key-value row editor for the HTTP tool node's custom headers,
// matching n8n's HTTP Request node ("Send Headers" -> "Using Fields Below"
// is its default/primary mode; raw JSON is only the fallback) rather than
// AgentMesh's original single JSON-textarea field. Still serializes to the
// same node.secrets.httpHeadersJSON JSON-object string the backend already
// reads (tool.go's callHTTP) -- purely a client-side editing upgrade.
function parseHttpHeaderRows(
  raw: string | undefined,
): { key: string; value: string }[] {
  // Once encrypted, the plaintext never comes back to the client -- same
  // sentinel semantics as SecretField above -- so editing starts a fresh
  // row set rather than attempting to decode "__enc__" as JSON.
  if (!raw || raw === "__enc__") return [];
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return Object.entries(parsed).map(([key, value]) => ({
        key,
        value: String(value),
      }));
    }
  } catch {
    // Not valid JSON -- fall through to an empty row set rather than
    // crashing the Inspector on unexpected stored content.
  }
  return [];
}

function HttpHeadersField({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const raw = node.secrets?.httpHeadersJSON;
  const isEncrypted = raw === "__enc__";

  // Rows live in local state, not derived fresh from node.secrets on every
  // render -- a freshly-added blank row has no key yet, so commit() (below)
  // never serializes it into httpHeadersJSON, and re-deriving from that
  // stripped-down JSON on the next render would make the blank row vanish
  // before the user can type into it. Only re-synced when a different node
  // is selected (node.id changes); the user's own edits flow through
  // setRows directly instead of round-tripping through node.secrets.
  const [rows, setRows] = useState(() => parseHttpHeaderRows(raw));
  const [syncedNodeID, setSyncedNodeID] = useState(node.id);
  if (syncedNodeID !== node.id) {
    setSyncedNodeID(node.id);
    setRows(parseHttpHeaderRows(raw));
  }

  const commit = (next: { key: string; value: string }[]) => {
    setRows(next);
    const obj: Record<string, string> = {};
    for (const r of next) {
      if (r.key.trim()) obj[r.key.trim()] = r.value;
    }
    // "" is encryptField's (backend/internal/api/handlers/secrets.go)
    // sentinel for "no change, keep whatever's already saved" -- so an
    // empty header set can never be sent as "", or removing every header
    // in the UI would silently leave the old encrypted set intact
    // server-side instead of actually clearing it. "__clear__" is the
    // distinct sentinel that means "yes, really clear this."
    const serialized =
      Object.keys(obj).length > 0 ? JSON.stringify(obj) : "__clear__";
    onUpdate({
      ...node,
      secrets: { ...node.secrets, httpHeadersJSON: serialized },
    });
  };

  // addRow/removeBlankRow touch only local `rows` state, deliberately NOT
  // calling commit -- a node with existing encrypted headers starts with
  // `rows = []` (parseHttpHeaderRows can't decrypt "__enc__"), so wiring
  // "+ Add header" through commit() serialized an empty object the instant
  // it was clicked, before the user typed anything, sending "__clear__" and
  // silently deleting the real saved headers. Only an actual key/value
  // keystroke (the two onChange handlers below, both already wired to
  // commit) should ever touch node.secrets -- clicking Add, or removing a
  // row that was never given any content, is purely local bookkeeping.
  const addRow = () => setRows([...rows, { key: "", value: "" }]);
  const removeRow = (i: number) => {
    const row = rows[i];
    const next = rows.filter((_, ri) => ri !== i);
    if (row.key.trim() === "" && row.value === "") {
      setRows(next);
    } else {
      commit(next);
    }
  };

  return (
    <Field label="Custom headers" hint="encrypted at rest">
      {isEncrypted && (
        <div style={{ fontSize: 11, color: "var(--fg-dim)", marginBottom: 6 }}>
          Headers set. Add a row below to replace them.
        </div>
      )}
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {rows.map((r, i) => (
          <div
            key={i}
            style={{ display: "flex", gap: 6, alignItems: "center" }}
          >
            <input
              style={monoInputStyle}
              value={r.key}
              placeholder="Header name"
              onChange={(e) => {
                const next = [...rows];
                next[i] = { ...next[i], key: e.target.value };
                commit(next);
              }}
            />
            <input
              style={monoInputStyle}
              value={r.value}
              placeholder="Value"
              onChange={(e) => {
                const next = [...rows];
                next[i] = { ...next[i], value: e.target.value };
                commit(next);
              }}
            />
            <button
              type="button"
              style={iconBtn}
              onClick={() => removeRow(i)}
              title="Remove header"
            >
              ×
            </button>
          </div>
        ))}
        <button
          type="button"
          style={{
            ...iconBtn,
            width: "auto",
            padding: "0 10px",
            alignSelf: "flex-start",
          }}
          onClick={addRow}
        >
          + Add header
        </button>
      </div>
    </Field>
  );
}

// ── Agent Inspector ────────────────────────────────────────────────────────
function AgentInspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  return (
    <>
      <Section label="Identity">
        <Field label="Name">
          <input
            style={inputStyle}
            value={node.name ?? ""}
            onChange={(e) => onUpdate({ ...node, name: e.target.value })}
          />
        </Field>
      </Section>

      <Section label="Funding">
        <div
          style={{
            padding: 12,
            background: "var(--bg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-2)",
            fontSize: 11,
            color: "var(--fg-muted)",
            lineHeight: 1.5,
          }}
        >
          Agents don&apos;t hold a wallet. Paid x402 tool calls are settled by
          the platform wallets and billed to your credit balance, so there is
          nothing to fund or top up per agent.
        </div>
      </Section>

      <Section label="System prompt">
        <textarea
          style={{
            ...inputStyle,
            height: "auto",
            padding: 10,
            resize: "vertical",
            lineHeight: 1.5,
          }}
          rows={5}
          value={node.systemPrompt ?? ""}
          onChange={(e) => onUpdate({ ...node, systemPrompt: e.target.value })}
        />
      </Section>

      <Section label="Limits">
        <div
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}
        >
          <Field label="Max spend / run">
            <input style={monoInputStyle} defaultValue="0.50 USDC" />
          </Field>
          <Field label="Timeout">
            <input style={monoInputStyle} defaultValue="30s" />
          </Field>
        </div>
      </Section>
    </>
  );
}

// ── Provider Inspector ─────────────────────────────────────────────────────
function ProviderInspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const tpl = PROVIDER_TEMPLATES.find((t) => t.id === node.template);
  return (
    <>
      <Section label="Model">
        <Field label="Provider">
          {node.custom ? (
            <input
              style={inputStyle}
              value={node.name ?? ""}
              placeholder="e.g. Together AI"
              onChange={(e) => onUpdate({ ...node, name: e.target.value })}
            />
          ) : (
            <input style={inputStyle} value={tpl?.name ?? ""} readOnly />
          )}
        </Field>
        <Field label="Model">
          {node.custom ? (
            <input
              style={monoInputStyle}
              value={node.model ?? ""}
              placeholder="e.g. llama-3.3-70b"
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            />
          ) : node.template === "gemini" ? (
            <select
              style={monoInputStyle}
              value={node.model ?? "gemini-2.5-flash"}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value="gemini-2.5-flash">gemini-2.5-flash</option>
              <option value="gemini-2.5-pro">gemini-2.5-pro</option>
            </select>
          ) : node.template === "openai" ? (
            <select
              style={monoInputStyle}
              value={node.model ?? "gpt-4.1"}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value="gpt-4.1">gpt-4.1</option>
              <option value="gpt-4o">gpt-4o</option>
              <option value="gpt-4o-mini">gpt-4o-mini</option>
              <option value="o3">o3</option>
              <option value="o4-mini">o4-mini</option>
            </select>
          ) : node.template === "anthropic" ? (
            <select
              style={monoInputStyle}
              value={node.model ?? "claude-sonnet-4-6"}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value="claude-sonnet-4-6">claude-sonnet-4-6</option>
              <option value="claude-opus-4-8">claude-opus-4-8</option>
              <option value="claude-haiku-4-5">claude-haiku-4-5</option>
            </select>
          ) : node.template === "groq" ? (
            <select
              style={monoInputStyle}
              value={node.model ?? "llama-3.3-70b-versatile"}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value="llama-3.3-70b-versatile">
                llama-3.3-70b-versatile
              </option>
              <option value="llama-3.1-8b-instant">llama-3.1-8b-instant</option>
            </select>
          ) : node.template === "mistral" ? (
            <select
              style={monoInputStyle}
              value={node.model ?? "mistral-large-latest"}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value="mistral-large-latest">mistral-large</option>
              <option value="mistral-medium-latest">mistral-medium</option>
              <option value="mistral-small-latest">mistral-small</option>
              <option value="codestral-latest">codestral</option>
            </select>
          ) : (
            <select
              style={monoInputStyle}
              value={node.model ?? tpl?.model ?? ""}
              onChange={(e) => onUpdate({ ...node, model: e.target.value })}
            >
              <option value={tpl?.model}>{tpl?.model}</option>
            </select>
          )}
        </Field>
      </Section>
      <Section label="Credentials">
        {!node.custom && (
          <Field label="Key source">
            <div style={{ display: "flex", gap: 8 }}>
              <button
                type="button"
                style={{
                  ...monoInputStyle,
                  cursor: "pointer",
                  fontWeight: node.keyMode !== "platform" ? 700 : 400,
                  opacity: node.keyMode !== "platform" ? 1 : 0.6,
                }}
                onClick={() => onUpdate({ ...node, keyMode: "byok" })}
              >
                Use my key
              </button>
              <button
                type="button"
                style={{
                  ...monoInputStyle,
                  cursor: "pointer",
                  fontWeight: node.keyMode === "platform" ? 700 : 400,
                  opacity: node.keyMode === "platform" ? 1 : 0.6,
                }}
                onClick={() => onUpdate({ ...node, keyMode: "platform" })}
              >
                Use platform key
              </button>
            </div>
          </Field>
        )}
        {node.keyMode === "platform" && !node.custom ? (
          <Field label="Billing" hint="charged per call, no key required">
            {(() => {
              const tier = modelTier(
                node.template ?? "",
                node.model ?? tpl?.model ?? "",
              );
              return (
                <input
                  style={monoInputStyle}
                  readOnly
                  value={`${tier} tier · $${TIER_FEES[tier].toFixed(2)}/call`}
                />
              );
            })()}
          </Field>
        ) : (
          <Field label="API Key" hint="encrypted at rest">
            <input
              style={monoInputStyle}
              type="password"
              value={node.apiKey === "__enc__" ? "" : (node.apiKey ?? "")}
              placeholder={
                node.apiKey === "__enc__"
                  ? "Key set, enter to replace"
                  : "AIza···"
              }
              onChange={(e) =>
                onUpdate({
                  ...node,
                  apiKey:
                    e.target.value ||
                    (node.apiKey === "__enc__" ? "__enc__" : ""),
                })
              }
            />
          </Field>
        )}
      </Section>
      <Section label="Parameters">
        <div
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}
        >
          <Field label="Temperature">
            <input style={monoInputStyle} defaultValue="0.4" />
          </Field>
          <Field label="Max tokens">
            <input style={monoInputStyle} defaultValue="2048" />
          </Field>
        </div>
      </Section>
    </>
  );
}

// ── Tool Inspector ─────────────────────────────────────────────────────────
// COMPUTE_TOOL_TEMPLATES take their settings entirely from
// CONNECTOR_CONFIG_FIELDS (via ConnectorConfigSection) — they read Config/
// Secrets keys, not node.url/node.method, so the generic Method/URL panel
// below is irrelevant for them and would just confuse the editor.
const COMPUTE_TOOL_TEMPLATES = new Set([
  "set",
  "json_extract",
  "crypto",
  "datetime",
  "xml",
  "template",
  "html_extract",
  "markdown",
  "quickchart",
]);

function ToolInspector({
  node,
  workflowId,
  onUpdate,
}: {
  node: WorkflowNode;
  workflowId: string;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const tpl = TOOL_TEMPLATES.find((t) => t.id === node.template);
  const isComputeTool = COMPUTE_TOOL_TEMPLATES.has(node.template ?? "");
  return (
    <>
      <Section label="Tool">
        {node.custom ? (
          <Field label="Name">
            <input
              style={inputStyle}
              value={node.name ?? ""}
              placeholder="My HTTP tool"
              onChange={(e) => onUpdate({ ...node, name: e.target.value })}
            />
          </Field>
        ) : (
          <>
            <Field label="Type">
              <input style={inputStyle} value={tpl?.name ?? ""} readOnly />
            </Field>
            <Field label="Description">
              <input style={inputStyle} value={tpl?.desc ?? ""} readOnly />
            </Field>
          </>
        )}
      </Section>
      {!isComputeTool && (
        <Section label="Config">
          <Field label="Method">
            <select
              style={monoInputStyle}
              value={node.method ?? "GET"}
              onChange={(e) => onUpdate({ ...node, method: e.target.value })}
            >
              <option>GET</option>
              <option>POST</option>
              <option>PUT</option>
              <option>PATCH</option>
              <option>DELETE</option>
            </select>
          </Field>
          <Field label="URL">
            <input
              style={monoInputStyle}
              value={node.url ?? ""}
              placeholder="https://api.example.com/v1/"
              onChange={(e) => onUpdate({ ...node, url: e.target.value })}
            />
          </Field>
        </Section>
      )}
      {node.template === "http" && (
        <Section label="Body (optional)">
          <Field
            label="Body template"
            hint="only sent on POST/PUT/PATCH/DELETE -- {{ result }} or {{ result.field }}"
          >
            <textarea
              style={{
                ...inputStyle,
                height: "auto",
                padding: 10,
                resize: "vertical",
                lineHeight: 1.5,
                fontFamily: "var(--font-mono)",
                fontSize: 11,
              }}
              rows={3}
              value={node.config?.httpBodyTemplate ?? ""}
              placeholder='Leave blank to send the raw upstream output, or write e.g. {"summary":"{{ result.extract }}"}'
              onChange={(e) =>
                onUpdate({
                  ...node,
                  config: { ...node.config, httpBodyTemplate: e.target.value },
                })
              }
            />
          </Field>
        </Section>
      )}
      {node.template === "http" && (
        // Unlike a real connector's Authentication section, these are
        // genuinely optional -- a plain public-API call needs none of
        // them -- so this deliberately skips ConnectorConfigSection's
        // connected/not-connected status pill, which assumes the secret
        // is required for the node to function at all. Also,
        // ConnectorConfigSection looks up CONNECTOR_CONFIG_FIELDS by
        // template id and there's deliberately no "http" entry there (a
        // plain URL call isn't a named connector), so it would silently
        // render nothing here -- these fields have to be inline.
        <Section label="Headers & auth (optional)">
          <HttpHeadersField node={node} onUpdate={onUpdate} />
          <SecretField
            node={node}
            onUpdate={onUpdate}
            secretKey="httpBasicUser"
            label="Basic auth username"
            hint="leave blank if not using basic auth"
            placeholder="username"
          />
          <SecretField
            node={node}
            onUpdate={onUpdate}
            secretKey="httpBasicPass"
            label="Basic auth password"
            placeholder="password"
          />
        </Section>
      )}
      {/* No-op for "http" (no CONNECTOR_CONFIG_FIELDS["http"] entry, see
          above) -- kept unconditional for any compute-tool template that
          does register a spec here. */}
      <ConnectorConfigSection
        node={node}
        workflowId={workflowId}
        onUpdate={onUpdate}
      />
    </>
  );
}

// Base64 inflates by 4/3, so the decoded size is what the user actually
// picked — showing the encoded length would overstate every file by a third.
// A tool402 node's JSON body references its own fields with {{kind:name}}.
// Kept in sync with nodes.bodyPlaceholder on the backend, which does the
// real substitution at call time — this copy only powers the editor's live
// feedback, so a bad body is caught while typing rather than by an endpoint
// that charges for the attempt.
const BODY_PLACEHOLDER = /\{\{(param|file|fileName|fileType):([^}]+)\}\}/g;

// referenceTokens lists what a field can be referenced as. A file offers its
// bytes, its name, and its type; a text field just its value.
function referenceTokens(p: CustomParam): string[] {
  if (!p.name) return [];
  return p.kind === "file"
    ? [`{{file:${p.name}}}`, `{{fileName:${p.name}}}`, `{{fileType:${p.name}}}`]
    : [`{{param:${p.name}}}`];
}

// validateBodyTemplate reports the first problem with a JSON body, or null.
// Two failure modes matter, and both are silent until money has moved: a
// reference to a field that does not exist, and a body that is not valid
// JSON once filled in.
function validateBodyTemplate(
  template: string,
  fields: CustomParam[],
  paramDefaults?: Record<string, string>,
): string | null {
  if (!template.trim()) return null;
  const known = new Set(fields.map((f) => f.name).filter(Boolean));
  const missing = new Set<string>();
  for (const m of template.matchAll(BODY_PLACEHOLDER)) {
    const name = m[2].trim();
    const isDiscoveredValue =
      m[1] === "param" && paramDefaults?.[name] !== undefined;
    if (!known.has(name) && !isDiscoveredValue) missing.add(m[0]);
  }
  if (missing.size > 0) {
    return `No field named ${[...missing].join(", ")} — add it below, or fix the name.`;
  }
  // Placeholders always sit inside string literals, so a stand-in value is
  // enough to check the surrounding document's shape.
  try {
    JSON.parse(template.replace(BODY_PLACEHOLDER, "x"));
  } catch (e) {
    return `Not valid JSON — ${e instanceof Error ? e.message : "check the syntax"}.`;
  }
  return null;
}

// bodySkeleton builds a starting body from whatever fields THIS node has,
// so the editor teaches the reference syntax without asserting anything
// about the endpoint. Nothing here knows a vendor's field names: an
// endpoint's real shape lives in its own docs (and almost none publish a
// machine-readable schema, so it cannot be generated), while the keys below
// are only a scaffold the caller edits.
function bodySkeleton(fields: CustomParam[]): string {
  const named = fields.filter((f) => f.name);
  if (named.length === 0) return "{\n  \n}";
  const lines = named.map(
    (f) =>
      `  "${f.name}": "${f.kind === "file" ? `{{file:${f.name}}}` : `{{param:${f.name}}}`}"`,
  );
  return `{\n${lines.join(",\n")}\n}`;
}


// ── Tool402 Inspector ──────────────────────────────────────────────────────
function Tool402Inspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const [draft, setDraft] = useState(node.endpoint ?? "");
  const [probing, setProbing] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [probeError, setProbeError] = useState<string | null>(null);
  const magenta = "#E879F9";

  const discover = async () => {
    if (!draft.trim()) return;
    setProbing(true);
    setProbeError(null);
    try {
      const quote = await toolsApi.x402quote(draft.trim());
      let host = draft;
      try {
        host = new URL(draft).host;
      } catch {
        /* use raw draft */
      }
      const params = quote.params ?? [];
      // Seed each discovered param's value: keep whatever the user already
      // typed for that name, otherwise take the backend's default (the
      // platform wallet address, for address-shaped params). Values for
      // params this endpoint no longer declares are dropped rather than
      // silently sent to a different endpoint after the URL is edited.
      const seeded: Record<string, string> = {};
      for (const p of params) {
        seeded[p.name] = node.paramDefaults?.[p.name] ?? p.default ?? "";
      }
      onUpdate({
        ...node,
        endpoint: draft.trim(),
        price: quote.price ?? "?",
        unit: quote.unit ?? "call",
        asset: quote.asset ?? "USDC",
        provider: host,
        priceLive: true,
        description: node.description || quote.description || "",
        // The target's own declared method wins over the dropdown's default:
        // calling a POST-only resource with GET fails before payment is even
        // considered.
        method: quote.method ?? node.method ?? "GET",
        discoveredParams: params,
        paramDefaults: seeded,
      });
    } catch (err: unknown) {
      setProbeError(err instanceof Error ? err.message : "probe failed");
      onUpdate({ ...node, endpoint: draft.trim(), priceLive: false });
    } finally {
      setProbing(false);
    }
  };

  const custom = node.customParams ?? [];
  const hasDiscovered = !!node.discoveredParams?.length;
  const hasFile = custom.some((p) => p.kind === "file" && p.value);
  const bodyMode = node.bodyMode === "json" ? "json" : "params";
  const bodyTemplate = node.bodyTemplate ?? "";
  const bodyRef = useRef<HTMLTextAreaElement | null>(null);
  const bodyError = validateBodyTemplate(
    bodyTemplate,
    custom,
    node.paramDefaults,
  );
  // How the configured values will actually reach the endpoint — worth
  // stating outright, since it changes with the mode, the method, and
  // whether a file is attached (a file forces multipart, a body forces POST).
  const paramTransport =
    bodyMode === "json"
      ? "as the JSON body below (POST)"
      : hasFile
        ? "as multipart/form-data (POST)"
        : node.method && node.method !== "GET"
          ? "in the JSON request body"
          : "as query params";

  // Inserts a reference at the cursor, so a file can be dropped into the
  // body without hand-typing a token whose spelling has to match exactly.
  const insertReference = (token: string) => {
    const el = bodyRef.current;
    const base = bodyTemplate || bodySkeleton(custom);
    if (!el) {
      onUpdate({ ...node, bodyTemplate: base + token });
      return;
    }
    const start = el.selectionStart ?? base.length;
    const end = el.selectionEnd ?? start;
    const next = base.slice(0, start) + token + base.slice(end);
    onUpdate({ ...node, bodyTemplate: next });
    requestAnimationFrame(() => {
      el.focus();
      const caret = start + token.length;
      el.setSelectionRange(caret, caret);
    });
  };

  const writeFields = (next: CustomParam[]) =>
    onUpdate({ ...node, customParams: next });
  const addField = () =>
    writeFields([...custom, { name: "", kind: "text", value: "" }]);
  const removeField = (i: number) =>
    writeFields(custom.filter((_, idx) => idx !== i));
  const patchField = (i: number, patch: Partial<CustomParam>) =>
    writeFields(custom.map((p, idx) => (idx === i ? { ...p, ...patch } : p)));

  const pickFile = async (i: number, file: File) => {
    setFieldError(null);
    try {
      // readFileAsBase64 owns the size check, the chunked encode, and the
      // limit-message wording, so the Inspector and the Prism console cannot
      // drift apart on any of the three.
      const encoded = await readFileAsBase64(file);
      patchField(i, {
        value: encoded.value,
        fileName: encoded.fileName,
        mimeType: encoded.mimeType,
      });
    } catch (e) {
      setFieldError(
        e instanceof Error ? e.message : `Could not read ${file.name}.`,
      );
    }
  };

  // Every x402 node is custom now (TOOL402_TEMPLATES removed -- see
  // node-cleanup plan Part A1), so this Inspector no longer has a
  // preset/non-custom branch to render; it always falls through to the
  // full editable form below.
  return (
    <>
      <Section label="Identity">
        <Field label="Name">
          <input
            style={inputStyle}
            value={node.name ?? ""}
            onChange={(e) => onUpdate({ ...node, name: e.target.value })}
          />
        </Field>
      </Section>
      <Section label="x402 endpoint">
        <Field label="Endpoint URL" hint="HTTP 402 compliant">
          <input
            style={monoInputStyle}
            placeholder="https://api.your-service.x402/v1/search"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") discover();
            }}
          />
        </Field>
        <Field
          label="Method"
          hint={
            node.method && node.method !== "GET"
              ? "body = the run's input message (e.g. from a chat trigger)"
              : undefined
          }
        >
          <select
            style={monoInputStyle}
            value={node.method ?? "GET"}
            onChange={(e) => onUpdate({ ...node, method: e.target.value })}
          >
            <option>GET</option>
            <option>POST</option>
          </select>
        </Field>
        <button
          onClick={discover}
          disabled={!draft.trim() || probing}
          style={{
            height: 32,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            gap: 6,
            width: "100%",
            border: `1px solid ${magenta}`,
            background: "transparent",
            color: probing ? "var(--fg-dim)" : magenta,
            borderRadius: "var(--r-2)",
            fontSize: 12,
            cursor: "pointer",
            fontFamily: "var(--font-sans)",
            fontWeight: 500,
          }}
        >
          {probing ? (
            <>● fetching price…</>
          ) : node.price ? (
            "Re-test & refresh price"
          ) : (
            "Test endpoint & fetch price"
          )}
        </button>
        {probeError && (
          <div
            style={{
              padding: "8px 10px",
              background: "rgba(248,113,113,0.08)",
              border: "1px solid rgba(248,113,113,0.3)",
              borderRadius: "var(--r-2)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "#F87171",
            }}
          >
            {probeError}
          </div>
        )}
        {node.price && !probing && (
          <div
            style={{
              padding: 14,
              background: "var(--bg)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-2)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
            }}
          >
            <div style={{ color: "var(--fg-muted)" }}>{node.provider}</div>
            <div
              style={{
                display: "flex",
                alignItems: "baseline",
                gap: 8,
                marginTop: 12,
              }}
            >
              <span style={{ color: magenta, fontSize: 22, fontWeight: 500 }}>
                {node.price}
              </span>
              <span style={{ color: "var(--fg-muted)" }}>
                {node.asset ?? "USDC"} / {node.unit}
              </span>
            </div>
            <div
              style={{
                marginTop: 8,
                color: node.priceLive ? "var(--accent)" : "var(--fg-dim)",
              }}
            >
              {node.priceLive
                ? "● live · fetched from backend"
                : "● cached · endpoint unreachable"}
            </div>
          </div>
        )}
      </Section>
      <Section label="Endpoint params">
        <div style={{ fontSize: 11, color: "var(--fg-dim)", marginBottom: 8 }}>
          {hasDiscovered
            ? "Declared by this endpoint itself. "
            : "This endpoint declares no inputs, so add whatever fields it needs. "}
          Sent {paramTransport}.
          {hasDiscovered && " An attached agent can override them per call."}
        </div>

        {/* Fields alone can only produce a flat request. An endpoint wanting a
            nested body — an array of file objects, say — needs the caller to
            write that shape, so the two ways of building a request are a
            deliberate, visible choice rather than something inferred. */}
        <div
          style={{
            display: "flex",
            gap: 2,
            padding: 2,
            marginBottom: 10,
            border: "1px solid var(--border)",
            borderRadius: "var(--r-2)",
            background: "var(--bg)",
          }}
        >
          {(
            [
              ["params", "Fields"],
              ["json", "JSON body"],
            ] as const
          ).map(([mode, label]) => {
            const active = bodyMode === mode;
            return (
              <button
                key={mode}
                onClick={() =>
                  onUpdate({
                    ...node,
                    bodyMode: mode,
                    // Seed the editor the first time, so the shape and the
                    // reference syntax are visible instead of a blank box.
                    bodyTemplate:
                      mode === "json" && !node.bodyTemplate
                        ? bodySkeleton(custom)
                        : node.bodyTemplate,
                  })
                }
                style={{
                  flex: 1,
                  height: 26,
                  border: "none",
                  borderRadius: "var(--r-1)",
                  cursor: "pointer",
                  fontFamily: "var(--font-mono)",
                  fontSize: 10.5,
                  letterSpacing: 0.3,
                  background: active ? "rgba(232,121,249,0.12)" : "transparent",
                  color: active ? magenta : "var(--fg-dim)",
                }}
              >
                {label}
              </button>
            );
          })}
        </div>

        {hasDiscovered && (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 10,
              marginBottom: 12,
            }}
          >
            {node.discoveredParams!.map((p) => {
              const value = node.paramDefaults?.[p.name] ?? "";
              const missing = p.required && !value.trim();
              return (
                <div key={p.name}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      marginBottom: 4,
                    }}
                  >
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        color: magenta,
                      }}
                    >
                      {p.name}
                    </span>
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 9,
                        color: "var(--fg-dim)",
                        background: "var(--bg-elev-2)",
                        padding: "1px 5px",
                        borderRadius: 3,
                      }}
                    >
                      {p.type}
                    </span>
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 9,
                        color: missing ? "#F87171" : "var(--fg-dim)",
                      }}
                    >
                      {p.required ? "required" : "optional"}
                    </span>
                  </div>
                  <input
                    style={{
                      ...monoInputStyle,
                      borderColor: missing ? "#F87171" : undefined,
                    }}
                    value={value}
                    placeholder={p.required ? "required" : "optional"}
                    onChange={(e) =>
                      onUpdate({
                        ...node,
                        paramDefaults: {
                          ...node.paramDefaults,
                          [p.name]: e.target.value,
                        },
                      })
                    }
                  />
                  {p.description && (
                    <div
                      style={{
                        fontSize: 10,
                        color: "var(--fg-muted)",
                        lineHeight: 1.4,
                        marginTop: 3,
                      }}
                    >
                      {p.description}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {custom.map((p, i) => (
            <div
              key={i}
              style={{
                display: "flex",
                flexDirection: "column",
                gap: 6,
                padding: 8,
                border: "1px solid var(--border)",
                borderRadius: "var(--r-2)",
                background: "var(--bg)",
              }}
            >
              <div style={{ display: "flex", gap: 6 }}>
                <input
                  style={{ ...monoInputStyle, flex: 1, minWidth: 0 }}
                  placeholder="field name"
                  value={p.name}
                  onChange={(e) => patchField(i, { name: e.target.value })}
                />
                <select
                  style={{ ...monoInputStyle, width: 74, flexShrink: 0 }}
                  value={p.kind}
                  onChange={(e) =>
                    patchField(i, {
                      kind: e.target.value as CustomParam["kind"],
                      value: "",
                      fileName: "",
                      mimeType: "",
                    })
                  }
                >
                  <option value="text">text</option>
                  <option value="file">file</option>
                </select>
                <button
                  onClick={() => removeField(i)}
                  title="remove field"
                  style={{
                    width: 30,
                    flexShrink: 0,
                    border: "1px solid var(--border)",
                    background: "transparent",
                    color: "var(--fg-dim)",
                    borderRadius: "var(--r-2)",
                    cursor: "pointer",
                    fontSize: 12,
                  }}
                >
                  ✕
                </button>
              </div>

              {p.kind === "file" ? (
                p.value ? (
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                    }}
                  >
                    <span style={{ color: magenta }}>
                      📎 {p.fileName || "file"}
                    </span>
                    <span style={{ color: "var(--fg-dim)" }}>
                      {formatFileSize(base64DecodedBytes(p.value ?? ""))}
                    </span>
                    <button
                      onClick={() =>
                        patchField(i, { value: "", fileName: "", mimeType: "" })
                      }
                      style={{
                        marginLeft: "auto",
                        border: "none",
                        background: "none",
                        color: "var(--fg-dim)",
                        cursor: "pointer",
                        fontSize: 11,
                      }}
                    >
                      ✕
                    </button>
                  </div>
                ) : (
                  <input
                    type="file"
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      if (f) pickFile(i, f);
                    }}
                    style={{ fontSize: 11, color: "var(--fg-muted)" }}
                  />
                )
              ) : (
                <input
                  style={monoInputStyle}
                  placeholder="value"
                  value={p.value ?? ""}
                  onChange={(e) => patchField(i, { value: e.target.value })}
                />
              )}

              {/* In JSON mode a field is not sent on its own — it is a value
                  the body can pull in. Clicking drops the exact token at the
                  cursor, since it has to match the field name character for
                  character to resolve. */}
              {bodyMode === "json" && referenceTokens(p).length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                  {referenceTokens(p).map((token) => (
                    <button
                      key={token}
                      onClick={() => insertReference(token)}
                      title="insert into the JSON body"
                      style={{
                        padding: "2px 6px",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--r-1)",
                        background: bodyTemplate.includes(token)
                          ? "rgba(232,121,249,0.10)"
                          : "transparent",
                        color: bodyTemplate.includes(token)
                          ? magenta
                          : "var(--fg-dim)",
                        fontFamily: "var(--font-mono)",
                        fontSize: 9.5,
                        cursor: "pointer",
                      }}
                    >
                      {token}
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>

        {fieldError && (
          <div
            style={{
              marginTop: 8,
              padding: "6px 8px",
              background: "rgba(248,113,113,0.08)",
              border: "1px solid rgba(248,113,113,0.3)",
              borderRadius: "var(--r-2)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "#F87171",
            }}
          >
            {fieldError}
          </div>
        )}

        <button
          onClick={addField}
          style={{
            marginTop: 8,
            height: 30,
            width: "100%",
            border: "1px dashed var(--border-strong)",
            background: "transparent",
            color: "var(--fg-muted)",
            borderRadius: "var(--r-2)",
            fontSize: 11,
            cursor: "pointer",
            fontFamily: "var(--font-sans)",
          }}
        >
          + add field
        </button>

        {bodyMode === "json" && (
          <div style={{ marginTop: 14 }}>
            <div
              style={{
                display: "flex",
                alignItems: "baseline",
                justifyContent: "space-between",
                marginBottom: 6,
              }}
            >
              <span
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  letterSpacing: 0.4,
                  color: "var(--fg-muted)",
                  textTransform: "uppercase",
                }}
              >
                Request body
              </span>
              <span style={{ fontSize: 10, color: "var(--fg-dim)" }}>
                paste the shape this endpoint documents
              </span>
            </div>
            <textarea
              ref={bodyRef}
              spellCheck={false}
              value={bodyTemplate}
              onChange={(e) =>
                onUpdate({ ...node, bodyTemplate: e.target.value })
              }
              placeholder={bodySkeleton(custom)}
              style={{
                ...monoInputStyle,
                height: 190,
                width: "100%",
                padding: 10,
                lineHeight: 1.55,
                resize: "vertical",
                whiteSpace: "pre",
                overflowWrap: "normal",
                overflowX: "auto",
                borderColor: bodyError ? "rgba(248,113,113,0.5)" : undefined,
              }}
            />
            {bodyError ? (
              <div
                style={{
                  marginTop: 6,
                  padding: "6px 8px",
                  background: "rgba(248,113,113,0.08)",
                  border: "1px solid rgba(248,113,113,0.3)",
                  borderRadius: "var(--r-2)",
                  fontFamily: "var(--font-mono)",
                  fontSize: 10,
                  color: "#F87171",
                }}
              >
                {bodyError}
              </div>
            ) : (
              <div
                style={{
                  marginTop: 6,
                  fontSize: 10,
                  lineHeight: 1.5,
                  color: "var(--fg-dim)",
                }}
              >
                {bodyTemplate.trim() ? (
                  <>
                    <span style={{ color: "var(--accent)" }}>✓ valid JSON</span>
                    {" — keys must match what the endpoint documents; field"}
                    {
                      " names are yours, they only appear inside {{…}}. A file's"
                    }
                    {" bytes are filled in at call time, never pasted here."}
                  </>
                ) : (
                  "Paste the body this endpoint documents, then click a field's chip above to reference it."
                )}
              </div>
            )}
          </div>
        )}
      </Section>
      <Section label="Tool description">
        <Field label="What this tool does" hint="shown to agent">
          <textarea
            style={{
              ...inputStyle,
              height: "auto",
              padding: 10,
              resize: "vertical",
              lineHeight: 1.5,
            }}
            rows={3}
            value={node.description ?? ""}
            placeholder="Describe what this x402 endpoint provides so the agent knows when to use it…"
            onChange={(e) => onUpdate({ ...node, description: e.target.value })}
          />
        </Field>
      </Section>
    </>
  );
}

// ── Trigger Inspector ──────────────────────────────────────────────────────
function TriggerInspector({
  node,
  onUpdate,
  workflowId,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
  workflowId: string;
}) {
  const tpl = TRIGGER_TEMPLATES.find((t) => t.id === node.template);
  return (
    <Section label="Trigger">
      {node.custom ? (
        <Field label="Label">
          <input
            style={inputStyle}
            value={node.label ?? ""}
            placeholder="When …"
            onChange={(e) => onUpdate({ ...node, label: e.target.value })}
          />
        </Field>
      ) : (
        <Field label="Type">
          <input style={inputStyle} value={tpl?.name ?? ""} readOnly />
        </Field>
      )}
      {node.template === "cron" && (
        <Field label="Cron">
          <input style={monoInputStyle} defaultValue="0 9 * * *" />
        </Field>
      )}
      {node.template === "webhook" && (
        <WebhookTriggerFields node={node} workflowId={workflowId} />
      )}
      {node.template === "chat" && (
        <Field label="Source">
          <input style={inputStyle} defaultValue="In-app chat widget" />
        </Field>
      )}
    </Section>
  );
}

// ── State ──────────────────────────────────────────────────────────────────
function StateInspector({
  node,
  onUpdate,
  workflowId,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
  workflowId: string;
}) {
  const op = node.stateOp ?? "get";
  const tpl = STATE_TEMPLATES.find((x) => x.id === op);

  return (
    <>
      <Section label="State">
        <Field label="Operation">
          <select
            style={inputStyle}
            value={op}
            onChange={(e) =>
              onUpdate({
                ...node,
                stateOp: e.target.value as NonNullable<WorkflowNode["stateOp"]>,
                // Keep the node's displayed identity in step with the
                // operation, so a node switched from Read to Write does not
                // keep announcing itself as "Read State" on the canvas.
                template: e.target.value,
                name: STATE_TEMPLATES.find((x) => x.id === e.target.value)
                  ?.name,
                icon: STATE_TEMPLATES.find((x) => x.id === e.target.value)
                  ?.icon,
              })
            }
          >
            {STATE_TEMPLATES.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Key" hint="persists across runs">
          <input
            style={monoInputStyle}
            value={node.stateKey ?? ""}
            placeholder="lastRowId"
            onChange={(e) => onUpdate({ ...node, stateKey: e.target.value })}
          />
        </Field>

        {op === "set" && (
          <Field label="Value" hint="blank = previous node's output">
            <input
              style={monoInputStyle}
              value={node.stateValue ?? ""}
              placeholder="leave blank to store the last output"
              onChange={(e) =>
                onUpdate({ ...node, stateValue: e.target.value })
              }
            />
          </Field>
        )}

        {op === "increment" && (
          <Field label="Amount" hint="defaults to 1">
            <input
              style={monoInputStyle}
              value={node.stateValue ?? ""}
              placeholder="1"
              onChange={(e) =>
                onUpdate({ ...node, stateValue: e.target.value })
              }
            />
          </Field>
        )}

        <div
          style={{
            fontSize: 11,
            lineHeight: 1.5,
            color: "var(--fg-dim)",
          }}
        >
          {op === "get" &&
            "Loads the saved value and passes it to the next node. Empty on the first run."}
          {op === "set" && "Saves a value that the next run can read back."}
          {op === "increment" &&
            "Adds to a running total. Safe when two runs overlap."}
          {op === "delete" && "Removes the saved value."}
          {tpl && " "}
        </div>
      </Section>

      <Section label="Use anywhere">
        <div
          style={{
            fontSize: 11,
            lineHeight: 1.6,
            color: "var(--fg-muted)",
          }}
        >
          Reference a saved value from any tool URL, prompt or email field:
          <div
            style={{
              marginTop: 6,
              padding: "6px 8px",
              borderRadius: "var(--r-2)",
              background: "var(--bg)",
              border: "1px solid var(--border)",
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--info)",
              userSelect: "all",
            }}
          >
            {`{{state.${node.stateKey || "key"}}}`}
          </div>
        </div>
      </Section>

      <SavedValues workflowId={workflowId} highlightKey={node.stateKey} />
    </>
  );
}

// The real public endpoint and its required auth secret -- both generated
// server-side (UpdateWorkflow's ensureWebhookSecrets) the first time this
// node is saved, never authored here. Only rendered once a secret exists
// (i.e. after at least one save), since before that the endpoint would
// reject every call anyway. Plain readonly inputs rather than a copy
// button: the security fix is the point here, a copy affordance is a nice-
// to-have this doesn't block on.
function WebhookTriggerFields({
  node,
  workflowId,
}: {
  node: WorkflowNode;
  workflowId: string;
}) {
  const apiOrigin = process.env.NEXT_PUBLIC_API_URL ?? "";
  const url = `${apiOrigin}/run/${workflowId}`;
  const secret = node.secrets?.webhookSecret;
  return (
    <>
      <Field label="Endpoint URL">
        <input style={monoInputStyle} value={url} readOnly />
      </Field>
      <Field label="Secret header">
        {secret ? (
          <input style={monoInputStyle} value={secret} readOnly />
        ) : (
          <div style={{ fontSize: 11.5, color: "var(--fg-muted)" }}>
            Save this workflow once to generate a secret.
          </div>
        )}
      </Field>
      <div style={{ fontSize: 11, color: "var(--fg-muted)", marginTop: 4 }}>
        POST to the endpoint above with header{" "}
        <code>X-Webhook-Secret: {secret ? "<secret>" : "…"}</code> -- calls
        without it are rejected.
      </div>
    </>
  );
}

// SavedValues shows what the workflow has actually stored right now. A state
// node is otherwise completely opaque in the editor -- you cannot tell
// whether a run ever wrote anything, or what a "{{state.x}}" reference will
// resolve to -- and that is exactly the question you have while wiring one up.
function SavedValues({
  workflowId,
  highlightKey,
}: {
  workflowId?: string;
  highlightKey?: string;
}) {
  const [vars, setVars] = useState<Record<string, unknown> | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Initial load. State is only ever set from the promise callbacks, never
  // synchronously in the effect body, and the cancelled flag drops a
  // response that lands after the inspector has moved to another node --
  // which happens routinely, since selecting a different node unmounts this.
  useEffect(() => {
    if (!workflowId || workflowId === "new") return;
    let cancelled = false;
    workflowsApi.variables
      .list(workflowId)
      .then((v) => {
        if (cancelled) return;
        setVars(v);
        setErr(null);
      })
      .catch((e: Error) => {
        if (!cancelled) setErr(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [workflowId]);

  const refresh = useCallback(() => {
    if (!workflowId || workflowId === "new") return;
    setBusy(true);
    workflowsApi.variables
      .list(workflowId)
      .then((v) => {
        setVars(v);
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message))
      .finally(() => setBusy(false));
  }, [workflowId]);

  if (!workflowId || workflowId === "new") return null;

  const entries = vars ? Object.entries(vars) : [];

  return (
    <Section label="Saved values">
      {err && <div style={{ fontSize: 11, color: "var(--danger)" }}>{err}</div>}
      {!err && vars && entries.length === 0 && (
        <div style={{ fontSize: 11, color: "var(--fg-dim)" }}>
          Nothing saved yet — a run has to write one first.
        </div>
      )}
      {entries.map(([k, v]) => {
        const isMatch = highlightKey === k;
        return (
          <div
            key={k}
            style={{
              display: "flex",
              alignItems: "baseline",
              justifyContent: "space-between",
              gap: 8,
              padding: "6px 8px",
              borderRadius: "var(--r-2)",
              background: isMatch ? "var(--info-soft)" : "var(--bg)",
              border: `1px solid ${isMatch ? "var(--info)" : "var(--border)"}`,
            }}
          >
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: isMatch ? "var(--info)" : "var(--fg-muted)",
                flexShrink: 0,
              }}
            >
              {k}
            </span>
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: "var(--fg)",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                textAlign: "right",
              }}
              title={JSON.stringify(v)}
            >
              {JSON.stringify(v)}
            </span>
          </div>
        );
      })}
      <button
        onClick={refresh}
        disabled={busy}
        style={{
          height: 30,
          background: "transparent",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-2)",
          color: "var(--fg-muted)",
          fontSize: 11,
          cursor: busy ? "default" : "pointer",
        }}
      >
        {busy ? "Refreshing…" : "Refresh"}
      </button>
    </Section>
  );
}

// ── Per-connector config field tables ───────────────────────────────────────
type ConnectorField =
  | {
      kind: "secret";
      key: string;
      label: string;
      hint?: string;
      placeholder: string;
      // The field's old Secrets key, for a connector whose Inspector field
      // moved to a new key -- e.g. Stripe's stripeAPIKey (was
      // stripeSecretKey). The backend already falls back to this key for a
      // node saved under the old name and runs correctly either way; this
      // is only so the "connected" status badge below doesn't call an
      // already-working node "Not connected" just because it checks the
      // new key alone.
      legacyKey?: string;
    }
  | {
      kind: "config";
      key: string;
      label: string;
      hint?: string;
      placeholder?: string;
    };

const CONNECTOR_CONFIG_FIELDS: Record<
  string,
  { label: string; oauthProvider?: string; fields: ConnectorField[] }
> = {
  slack: {
    label: "Slack config",
    oauthProvider: "slack",
    fields: [
      {
        kind: "secret",
        key: "slackWebhookURL",
        label: "Webhook URL",
        hint: "or connect above for bot-token mode",
        placeholder: "https://hooks.slack.com/services/…",
      },
      {
        kind: "config",
        key: "slackChannel",
        label: "Channel ID (bot-token mode)",
        placeholder: "C0123456789",
      },
    ],
  },
  discord: {
    label: "Discord config",
    fields: [
      {
        kind: "secret",
        key: "discordWebhookURL",
        label: "Webhook URL",
        placeholder: "https://discord.com/api/webhooks/…",
      },
    ],
  },
  teams: {
    label: "Teams config",
    fields: [
      {
        kind: "secret",
        key: "teamsWebhookURL",
        label: "Webhook URL",
        placeholder: "https://…webhook.office.com/webhookb2/…",
      },
    ],
  },
  google_chat: {
    label: "Google Chat config",
    fields: [
      {
        kind: "secret",
        key: "googleChatWebhookURL",
        label: "Webhook URL",
        placeholder: "https://chat.googleapis.com/v1/spaces/…",
      },
    ],
  },
  ntfy: {
    label: "Ntfy config",
    fields: [
      {
        kind: "config",
        key: "ntfyTopic",
        label: "Topic",
        placeholder: "agentmesh-alerts",
      },
      {
        kind: "config",
        key: "ntfyServerURL",
        label: "Server URL",
        placeholder: "https://ntfy.sh (default)",
      },
      {
        kind: "secret",
        key: "ntfyAuthToken",
        label: "Auth Token",
        hint: "optional, for private topics",
        placeholder: "tk_xxxxxxxxxxxx",
      },
    ],
  },
  telegram: {
    label: "Telegram config",
    fields: [
      {
        kind: "secret",
        key: "telegramBotToken",
        label: "Bot Token",
        placeholder: "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "telegramChatID",
        label: "Chat ID",
        placeholder: "-1001234567890",
      },
    ],
  },
  telegram_get_updates: {
    label: "Telegram config",
    fields: [
      {
        kind: "secret",
        key: "telegramBotToken",
        label: "Bot Token",
        placeholder: "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "telegramOffset",
        label: "Offset",
        hint: "optional -- only updates after this ID",
        placeholder: "e.g. 481231",
      },
      {
        kind: "config",
        key: "telegramLimit",
        label: "Limit",
        hint: "optional, default 100",
        placeholder: "e.g. 20",
      },
    ],
  },
  github: {
    label: "GitHub config",
    oauthProvider: "github",
    fields: [
      {
        kind: "secret",
        key: "githubToken",
        label: "Personal Access Token",
        placeholder: "ghp_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "githubRepo",
        label: "Repository",
        placeholder: "owner/repo",
      },
    ],
  },
  notion: {
    label: "Notion config",
    oauthProvider: "notion",
    fields: [
      {
        kind: "secret",
        key: "notionAPIKey",
        label: "Internal Integration Secret",
        placeholder: "secret_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "notionPageID",
        label: "Page ID",
        placeholder: "the target page's UUID",
      },
    ],
  },
  airtable: {
    label: "Airtable config",
    oauthProvider: "airtable",
    fields: [
      {
        kind: "secret",
        key: "airtableAPIKey",
        label: "Personal Access Token",
        placeholder: "pat_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "airtableBaseID",
        label: "Base ID",
        placeholder: "appXXXXXXXXXXXXXX",
      },
      {
        kind: "config",
        key: "airtableTable",
        label: "Table",
        placeholder: "Tasks",
      },
      {
        kind: "config",
        key: "airtableFieldName",
        label: "Field Name",
        placeholder: "Notes (default)",
      },
    ],
  },
  hubspot: {
    label: "HubSpot config",
    oauthProvider: "hubspot",
    fields: [
      {
        kind: "secret",
        key: "hubspotAPIKey",
        label: "Private App Token",
        placeholder: "pat-na1-xxxxxxxxxxxxxxxxxxxx",
      },
    ],
  },
  trello: {
    label: "Trello config",
    fields: [
      {
        kind: "secret",
        key: "trelloAPIKey",
        label: "API Key",
        placeholder: "your Trello API key",
      },
      {
        kind: "secret",
        key: "trelloToken",
        label: "Token",
        placeholder: "your Trello token",
      },
      {
        kind: "config",
        key: "trelloListID",
        label: "List ID",
        placeholder: "target list id",
      },
    ],
  },
  asana: {
    label: "Asana config",
    oauthProvider: "asana",
    fields: [
      {
        kind: "secret",
        key: "asanaAPIKey",
        label: "Personal Access Token",
        placeholder: "1/1234567890:xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "asanaProjectID",
        label: "Project ID",
        placeholder: "target project id",
      },
    ],
  },
  clickup: {
    label: "ClickUp config",
    oauthProvider: "clickup",
    fields: [
      {
        kind: "secret",
        key: "clickupAPIKey",
        label: "API Token",
        placeholder: "pk_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "clickupListID",
        label: "List ID",
        placeholder: "target list id",
      },
    ],
  },
  jira: {
    label: "Jira config",
    oauthProvider: "jira",
    fields: [
      {
        kind: "secret",
        key: "jiraAPIToken",
        label: "API Token",
        placeholder: "your Atlassian API token",
      },
      {
        kind: "config",
        key: "jiraEmail",
        label: "Account Email",
        placeholder: "bot@yourcompany.com",
      },
      {
        kind: "config",
        key: "jiraDomain",
        label: "Site Domain",
        placeholder: "yourcompany (as in yourcompany.atlassian.net)",
      },
      {
        kind: "config",
        key: "jiraProjectKey",
        label: "Project Key",
        placeholder: "ENG",
      },
      {
        kind: "config",
        key: "jiraIssueType",
        label: "Issue Type",
        placeholder: "Task (default)",
      },
    ],
  },
  mailchimp: {
    label: "Mailchimp config",
    oauthProvider: "mailchimp",
    fields: [
      {
        kind: "secret",
        key: "mailchimpAPIKey",
        label: "API Key",
        placeholder: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-us21",
      },
      {
        kind: "config",
        key: "mailchimpListID",
        label: "Audience (List) ID",
        placeholder: "target list id",
      },
      {
        kind: "config",
        key: "mailchimpEmail",
        label: "Email",
        hint: "optional, defaults to the run's output",
        placeholder: "leave blank to use the agent's message as the email",
      },
    ],
  },
  linear: {
    label: "Linear config",
    oauthProvider: "linear",
    fields: [
      {
        kind: "secret",
        key: "linearAPIKey",
        label: "Personal API Key",
        placeholder: "lin_api_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "linearTeamID",
        label: "Team ID",
        placeholder: "target team id",
      },
    ],
  },
  todoist: {
    label: "Todoist config",
    oauthProvider: "todoist",
    fields: [
      {
        kind: "secret",
        key: "todoistAPIKey",
        label: "API Token",
        placeholder: "your Todoist API token",
      },
      {
        kind: "config",
        key: "todoistProjectID",
        label: "Project ID",
        hint: "optional",
        placeholder: "leave blank for Inbox",
      },
    ],
  },
  gitlab: {
    label: "GitLab config",
    oauthProvider: "gitlab",
    fields: [
      {
        kind: "secret",
        key: "gitlabAPIToken",
        label: "Personal Access Token",
        placeholder: "glpat-xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "gitlabProjectID",
        label: "Project ID",
        placeholder: "numeric project id",
      },
      {
        kind: "config",
        key: "gitlabBaseURL",
        label: "Base URL",
        hint: "optional, for self-hosted",
        placeholder: "https://gitlab.com (default)",
      },
    ],
  },
  sentry: {
    label: "Sentry config",
    fields: [
      {
        kind: "secret",
        key: "sentryDSN",
        label: "DSN",
        placeholder: "https://xxxx@o000000.ingest.sentry.io/000000",
      },
    ],
  },
  supabase: {
    label: "Supabase config",
    fields: [
      {
        kind: "secret",
        key: "supabaseAPIKey",
        label: "Service Role Key",
        placeholder: "eyJhbGciOi…",
      },
      {
        kind: "config",
        key: "supabaseProjectURL",
        label: "Project URL",
        placeholder: "https://xxxxxxxx.supabase.co",
      },
      {
        kind: "config",
        key: "supabaseTable",
        label: "Table",
        placeholder: "logs",
      },
      {
        kind: "config",
        key: "supabaseColumn",
        label: "Column",
        placeholder: "content (default)",
      },
    ],
  },
  woocommerce: {
    label: "WooCommerce config",
    fields: [
      {
        kind: "secret",
        key: "woocommerceConsumerKey",
        label: "Consumer Key",
        placeholder: "ck_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "secret",
        key: "woocommerceConsumerSecret",
        label: "Consumer Secret",
        placeholder: "cs_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "woocommerceStoreURL",
        label: "Store URL",
        placeholder: "https://yourstore.com",
      },
      {
        kind: "config",
        key: "woocommerceOrderID",
        label: "Order ID",
        placeholder: "target order id",
      },
    ],
  },
  elevenlabs: {
    label: "ElevenLabs config",
    fields: [
      {
        kind: "secret",
        key: "elevenlabsAPIKey",
        label: "API Key",
        placeholder: "your ElevenLabs API key",
      },
      {
        kind: "config",
        key: "elevenlabsVoiceID",
        label: "Voice ID",
        placeholder: "21m00Tcm4TlvDq8ikWAM (Rachel, default)",
      },
    ],
  },
  set: {
    label: "Edit Fields config",
    fields: [
      {
        kind: "config",
        key: "setFields",
        label: "Fields (JSON)",
        placeholder: '{"city":"{{ node.n1.city }}","asked":"{{ input }}"}',
        hint: "String values may use {{ result }}, {{ input }}, {{ node.<id>.<field> }}",
      },
    ],
  },
  json_extract: {
    label: "JSON Extract config",
    fields: [
      {
        kind: "config",
        key: "jsonPath",
        label: "Path",
        placeholder: "data.items.0.name",
        hint: "Dot path; numeric segments index arrays",
      },
    ],
  },
  crypto: {
    label: "Crypto config",
    fields: [
      {
        kind: "config",
        key: "cryptoAction",
        label: "Action",
        placeholder: "sha256",
        hint: "sha256 · sha512 · sha1 · md5 · hmac-sha256 · base64 · base64decode",
      },
      {
        kind: "secret",
        key: "cryptoSecret",
        label: "HMAC secret",
        hint: "only for hmac-sha256",
        placeholder: "shared secret",
      },
    ],
  },
  datetime: {
    label: "Date & Time config",
    fields: [
      {
        kind: "config",
        key: "dtFormat",
        label: "Format",
        placeholder: "rfc3339",
        hint: "rfc3339 · unix · date · time · or a Go layout",
      },
      {
        kind: "config",
        key: "dtOffset",
        label: "Offset",
        hint: "optional",
        placeholder: "-24h",
      },
      {
        kind: "config",
        key: "dtZone",
        label: "Timezone",
        hint: "optional, IANA name",
        placeholder: "Asia/Kolkata",
      },
    ],
  },
  template: {
    label: "Text Template config",
    fields: [
      {
        kind: "config",
        key: "templateText",
        label: "Template",
        placeholder: "Result: {{ result }}",
        hint: "Supports {{ result }}, {{ input }}, {{ node.<id>.<field> }}",
      },
    ],
  },
  stripe: {
    label: "Stripe config",
    fields: [
      {
        kind: "secret",
        key: "stripeAPIKey",
        label: "Secret Key",
        placeholder: "sk_live_xxxxxxxxxxxx",
        legacyKey: "stripeSecretKey",
      },
      {
        kind: "config",
        key: "stripeEmail",
        label: "Customer email",
        placeholder: "buyer@example.com",
      },
      {
        kind: "config",
        key: "stripeName",
        label: "Customer name",
        hint: "optional",
        placeholder: "leave blank to omit",
      },
    ],
  },
  twilio: {
    label: "Twilio config",
    fields: [
      {
        kind: "secret",
        key: "twilioAuthToken",
        label: "Auth Token",
        placeholder: "your Twilio auth token",
      },
      {
        kind: "config",
        key: "twilioAccountSID",
        label: "Account SID",
        placeholder: "ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "twilioFrom",
        label: "From",
        placeholder: "+15551234567 (a number on your account)",
      },
      {
        kind: "config",
        key: "twilioTo",
        label: "To",
        placeholder: "+15559876543",
      },
    ],
  },
  mattermost: {
    label: "Mattermost config",
    fields: [
      {
        kind: "secret",
        key: "mattermostWebhookURL",
        label: "Incoming Webhook URL",
        placeholder: "https://mattermost.example.com/hooks/xxx",
      },
      {
        kind: "config",
        key: "mattermostChannel",
        label: "Channel",
        hint: "optional",
        placeholder: "town-square",
      },
      {
        kind: "config",
        key: "mattermostUsername",
        label: "Post as",
        hint: "optional",
        placeholder: "AgentMesh",
      },
    ],
  },
  pagerduty: {
    label: "PagerDuty config",
    fields: [
      {
        kind: "secret",
        key: "pagerdutyRoutingKey",
        label: "Events API v2 Integration Key",
        placeholder: "32-character routing key",
      },
      {
        kind: "config",
        key: "pagerdutySeverity",
        label: "Severity",
        placeholder: "info (default)",
        hint: "critical · error · warning · info",
      },
      {
        kind: "config",
        key: "pagerdutySource",
        label: "Source",
        hint: "optional",
        placeholder: "agentmesh",
      },
    ],
  },
  zendesk: {
    label: "Zendesk config",
    fields: [
      {
        kind: "secret",
        key: "zendeskAPIToken",
        label: "API Token",
        placeholder: "your Zendesk API token",
      },
      {
        kind: "config",
        key: "zendeskSubdomain",
        label: "Subdomain",
        hint: "the part before .zendesk.com",
        placeholder: "yourcompany",
      },
      {
        kind: "config",
        key: "zendeskEmail",
        label: "Agent Email",
        placeholder: "agent@yourcompany.com",
      },
    ],
  },
  monday: {
    label: "Monday.com config",
    fields: [
      {
        kind: "secret",
        key: "mondayAPIKey",
        label: "API Token",
        placeholder: "your Monday.com v2 token",
      },
      {
        kind: "config",
        key: "mondayBoardID",
        label: "Board ID",
        placeholder: "123456789",
      },
    ],
  },
  intercom: {
    label: "Intercom config",
    fields: [
      {
        kind: "secret",
        key: "intercomAccessToken",
        label: "Access Token",
        placeholder: "your Intercom access token",
      },
      {
        kind: "config",
        key: "intercomEmail",
        label: "Lead Email",
        hint: "optional, defaults to the upstream message",
      },
    ],
  },
  openweathermap: {
    label: "OpenWeatherMap config",
    fields: [
      {
        kind: "secret",
        key: "openWeatherAPIKey",
        label: "API Key",
        placeholder: "your OpenWeatherMap API key",
      },
      {
        kind: "config",
        key: "weatherCity",
        label: "City",
        hint: "optional, defaults to the upstream message",
        placeholder: "London",
      },
      {
        kind: "config",
        key: "weatherUnits",
        label: "Units",
        placeholder: "metric (default) · imperial · standard",
      },
    ],
  },
  calendly: {
    label: "Calendly config",
    fields: [
      {
        kind: "secret",
        key: "calendlyAccessToken",
        label: "Personal Access Token",
        placeholder: "your Calendly PAT",
      },
      {
        kind: "config",
        key: "calendlyUserURI",
        label: "User URI",
        placeholder: "https://api.calendly.com/users/…",
      },
      {
        kind: "config",
        key: "calendlyCount",
        label: "Count",
        placeholder: "10 (default)",
      },
    ],
  },
  shopify_customer: {
    label: "Shopify config",
    fields: [
      {
        kind: "secret",
        key: "shopifyAccessToken",
        label: "Admin API Access Token",
        placeholder: "shpat_xxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "shopifyStore",
        label: "Store handle",
        placeholder: "acme-store (from acme-store.myshopify.com)",
      },
      {
        kind: "config",
        key: "shopifyEmail",
        label: "Customer email",
        placeholder: "buyer@example.com",
      },
    ],
  },
  pipedrive: {
    label: "Pipedrive config",
    fields: [
      {
        kind: "secret",
        key: "pipedriveAPIToken",
        label: "API Token",
        placeholder: "your Pipedrive API token",
      },
      {
        kind: "config",
        key: "pipedriveCompanyDomain",
        label: "Company domain",
        placeholder: "acme (from acme.pipedrive.com)",
      },
      {
        kind: "config",
        key: "pipedriveDealID",
        label: "Deal ID",
        hint: "optional",
        placeholder: "attach the note to a deal",
      },
      {
        kind: "config",
        key: "pipedrivePersonID",
        label: "Person ID",
        hint: "optional",
        placeholder: "attach the note to a person",
      },
    ],
  },
  db: {
    label: "Postgres config",
    fields: [
      {
        kind: "secret",
        key: "pgConnString",
        label: "Connection string",
        placeholder: "postgres://user:pass@host:5432/dbname",
      },
      {
        kind: "config",
        key: "pgTable",
        label: "Table",
        placeholder: "events",
      },
      {
        kind: "config",
        key: "pgColumn",
        label: "Output column",
        placeholder: "payload",
        hint: "receives the run output",
      },
      {
        kind: "config",
        key: "pgExtraColumns",
        label: "Extra columns (JSON)",
        hint: "optional",
        placeholder: '{"source":"agentmesh","city":"{{ node.n1.city }}"}',
      },
    ],
  },
  html_extract: {
    label: "HTML Extract config",
    fields: [
      {
        kind: "config",
        key: "htmlSelector",
        label: "CSS selector",
        placeholder: "h1.title",
      },
      {
        kind: "config",
        key: "htmlAttr",
        label: "Attribute",
        hint: "optional, blank = text",
        placeholder: "href",
      },
      {
        kind: "config",
        key: "htmlMode",
        label: "Mode",
        placeholder: "first",
        hint: "first · all",
      },
    ],
  },
  markdown: {
    label: "Markdown config",
    fields: [
      {
        kind: "config",
        key: "mdGFM",
        label: "GitHub Flavored",
        placeholder: "true",
        hint: "true · false — tables, strikethrough, autolinks",
      },
    ],
  },
  rss: {
    label: "RSS config",
    fields: [
      {
        kind: "config",
        key: "rssURL",
        label: "Feed URL",
        placeholder: "https://example.com/feed.xml",
      },
      {
        kind: "config",
        key: "rssLimit",
        label: "Max items",
        hint: "optional, default 10",
        placeholder: "10",
      },
    ],
  },
  graphql: {
    label: "GraphQL config",
    fields: [
      {
        kind: "config",
        key: "graphqlEndpoint",
        label: "Endpoint",
        placeholder: "https://api.github.com/graphql",
      },
      {
        kind: "config",
        key: "graphqlQuery",
        label: "Query",
        placeholder: "query { viewer { login } }",
      },
      {
        kind: "config",
        key: "graphqlVariables",
        label: "Variables (JSON)",
        hint: "optional",
        placeholder: '{"first":10,"search":"{{ result }}"}',
      },
      {
        kind: "secret",
        key: "graphqlAuthHeader",
        label: "Authorization header",
        hint: "sent verbatim — include Bearer if the API wants it",
        placeholder: "Bearer ghp_xxxxxxxx",
      },
    ],
  },
  hackernews: {
    label: "Hacker News config",
    fields: [
      {
        kind: "config",
        key: "hnQuery",
        label: "Search query",
        placeholder: "{{ result }}",
      },
      {
        kind: "config",
        key: "hnTags",
        label: "Tags",
        hint: "optional",
        placeholder: "story · comment · show_hn · ask_hn",
      },
      {
        kind: "config",
        key: "hnLimit",
        label: "Max items",
        hint: "optional, default 10",
        placeholder: "10",
      },
    ],
  },
  coingecko: {
    label: "CoinGecko config",
    fields: [
      {
        kind: "config",
        key: "cgIDs",
        label: "Coin IDs",
        placeholder: "bitcoin,ethereum",
      },
      {
        kind: "config",
        key: "cgCurrencies",
        label: "Currencies",
        hint: "optional, default usd",
        placeholder: "usd,eur",
      },
    ],
  },
  quickchart: {
    label: "QuickChart config",
    fields: [
      {
        kind: "config",
        key: "qcConfig",
        label: "Chart.js config (JSON)",
        placeholder: '{"type":"bar","data":{"labels":["a","b"],"datasets":[{"data":[1,2]}]}}',
      },
      {
        kind: "config",
        key: "qcWidth",
        label: "Width",
        hint: "optional",
        placeholder: "600",
      },
      {
        kind: "config",
        key: "qcHeight",
        label: "Height",
        hint: "optional",
        placeholder: "400",
      },
    ],
  },
  // Distinct from "shopify_customer" above (which creates a customer): this
  // adds a note to an existing order, and keeps template id "shopify" --
  // master's original id and behavior for this operation -- rather than
  // "shopify_customer"'s newer id, so an already-saved order-note node
  // keeps hitting the same backend dispatch with no config change on the
  // user's side. See connectors_business.go's sendShopifyOrderNote doc
  // comment.
  shopify: {
    label: "Shopify: Add Order Note config",
    fields: [
      {
        kind: "secret",
        key: "shopifyAccessToken",
        label: "Admin API Access Token",
        placeholder: "shpat_…",
      },
      {
        kind: "config",
        key: "shopifyShopDomain",
        label: "Shop Domain",
        placeholder: "mystore.myshopify.com",
      },
      {
        kind: "config",
        key: "shopifyOrderID",
        label: "Order ID",
        placeholder: "target order id",
      },
    ],
  },
  baserow: {
    label: "Baserow config",
    fields: [
      {
        kind: "secret",
        key: "baserowAPIToken",
        label: "API Token",
        placeholder: "your Baserow database token",
      },
      {
        kind: "config",
        key: "baserowTableID",
        label: "Table ID",
        placeholder: "the numeric table id",
      },
      {
        kind: "config",
        key: "baserowFieldName",
        label: "Field Name",
        placeholder: "Notes (default)",
      },
    ],
  },
};

// ── Per-connector auth metadata ─────────────────────────────────────────────
// Where each connector's credential is obtained. Every live connector requires
// an account login to get its credential EXCEPT ntfy (token is optional), which
// is why it alone carries needsLogin: false.
const CONNECTOR_AUTH: Record<
  string,
  { needsLogin: boolean; docUrl: string; linkLabel: string }
> = {
  slack: {
    needsLogin: true,
    docUrl: "https://api.slack.com/apps",
    linkLabel: "Create webhook",
  },
  discord: {
    needsLogin: true,
    docUrl:
      "https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks",
    linkLabel: "Create webhook",
  },
  teams: {
    needsLogin: true,
    docUrl:
      "https://learn.microsoft.com/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook",
    linkLabel: "Create webhook",
  },
  google_chat: {
    needsLogin: true,
    docUrl: "https://developers.google.com/workspace/chat/quickstart/webhooks",
    linkLabel: "Create webhook",
  },
  ntfy: {
    needsLogin: false,
    docUrl: "https://docs.ntfy.sh/publish/",
    linkLabel: "ntfy docs",
  },
  telegram: {
    needsLogin: true,
    docUrl: "https://t.me/BotFather",
    linkLabel: "Open BotFather",
  },
  telegram_get_updates: {
    needsLogin: true,
    docUrl: "https://t.me/BotFather",
    linkLabel: "Open BotFather",
  },
  github: {
    needsLogin: true,
    docUrl: "https://github.com/settings/tokens",
    linkLabel: "Get token",
  },
  notion: {
    needsLogin: true,
    docUrl: "https://www.notion.so/my-integrations",
    linkLabel: "Get secret",
  },
  airtable: {
    needsLogin: true,
    docUrl: "https://airtable.com/create/tokens",
    linkLabel: "Get token",
  },
  hubspot: {
    needsLogin: true,
    docUrl: "https://app.hubspot.com/private-apps",
    linkLabel: "Get token",
  },
  trello: {
    needsLogin: true,
    docUrl: "https://trello.com/power-ups/admin",
    linkLabel: "Get key & token",
  },
  asana: {
    needsLogin: true,
    docUrl: "https://app.asana.com/0/my-apps",
    linkLabel: "Get token",
  },
  clickup: {
    needsLogin: true,
    docUrl: "https://app.clickup.com/settings/apps",
    linkLabel: "Get token",
  },
  jira: {
    needsLogin: true,
    docUrl: "https://id.atlassian.com/manage-profile/security/api-tokens",
    linkLabel: "Get token",
  },
  mailchimp: {
    needsLogin: true,
    docUrl: "https://admin.mailchimp.com/account/api/",
    linkLabel: "Get key",
  },
  linear: {
    needsLogin: true,
    docUrl: "https://linear.app/settings/api",
    linkLabel: "Get key",
  },
  todoist: {
    needsLogin: true,
    docUrl: "https://todoist.com/app/settings/integrations/developer",
    linkLabel: "Get token",
  },
  gitlab: {
    needsLogin: true,
    docUrl: "https://gitlab.com/-/user_settings/personal_access_tokens",
    linkLabel: "Get token",
  },
  sentry: {
    needsLogin: true,
    docUrl:
      "https://docs.sentry.io/product/sentry-basics/concepts/dsn-explainer/",
    linkLabel: "Find your DSN",
  },
  supabase: {
    needsLogin: true,
    docUrl: "https://supabase.com/dashboard/project/_/settings/api",
    linkLabel: "Get service key",
  },
  woocommerce: {
    needsLogin: true,
    docUrl: "https://woocommerce.com/document/woocommerce-rest-api/",
    linkLabel: "Get API keys",
  },
  elevenlabs: {
    needsLogin: true,
    docUrl: "https://elevenlabs.io/app/settings/api-keys",
    linkLabel: "Get key",
  },
  twilio: {
    needsLogin: true,
    docUrl: "https://console.twilio.com",
    linkLabel: "Get credentials",
  },
  stripe: {
    needsLogin: true,
    docUrl: "https://dashboard.stripe.com/apikeys",
    linkLabel: "Get key",
  },
  pagerduty: {
    needsLogin: true,
    docUrl: "https://support.pagerduty.com/docs/services-and-integrations",
    linkLabel: "Get integration key",
  },
  zendesk: {
    needsLogin: true,
    docUrl: "https://support.zendesk.com/hc/en-us/articles/4408889192858",
    linkLabel: "Get API token",
  },
  intercom: {
    needsLogin: true,
    docUrl: "https://app.intercom.com/a/apps/_/settings/api-keys",
    linkLabel: "Get token",
  },
  openweathermap: {
    needsLogin: true,
    docUrl: "https://home.openweathermap.org/api_keys",
    linkLabel: "Get key",
  },
  calendly: {
    needsLogin: true,
    docUrl: "https://calendly.com/integrations/api_webhooks",
    linkLabel: "Get token",
  },
  shopify: {
    needsLogin: true,
    docUrl:
      "https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens",
    linkLabel: "Get access token",
  },
  shopify_customer: {
    needsLogin: true,
    docUrl: "https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens",
    linkLabel: "Get access token",
  },
  baserow: {
    needsLogin: true,
    docUrl: "https://baserow.io/user/settings/tokens",
    linkLabel: "Get token",
  },
};

// Small "where to get the credential" deep-link. Underline-free per the design
// system -- links read via --accent color, not decoration.
function AuthDocLink({ href, label }: { href: string; label: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 5,
        alignSelf: "flex-start",
        padding: "4px 0",
        fontFamily: "var(--font-sans)",
        fontSize: 11,
        fontWeight: 600,
        color: "var(--accent)",
        textDecoration: "none",
        transition: "color 0.12s var(--ease)",
      }}
      onMouseEnter={(e) => {
        (e.currentTarget as HTMLElement).style.color = "var(--accent-strong)";
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.color = "var(--accent)";
      }}
    >
      {label}
      <svg
        width="11"
        height="11"
        viewBox="0 0 12 12"
        fill="none"
        aria-hidden="true"
      >
        <path
          d="M3.5 8.5l5-5M4.5 3.5h4v4"
          stroke="currentColor"
          strokeWidth="1.3"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </a>
  );
}

// Lets a connector send just part of the upstream output instead of always
// the whole thing -- {{ result }} is the raw output (today's default
// behavior, unchanged if this is left blank), {{ result.field }} picks one
// field out of it (e.g. {{ result.extract }} against a JSON API response).
// Backend: resolveMessage/expandTemplate in connector_helpers.go.
function MessageTemplateField({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  return (
    <Field
      label="Message template"
      hint="optional -- {{ result }} or {{ result.field }}"
    >
      <textarea
        style={{
          ...inputStyle,
          height: "auto",
          padding: 10,
          resize: "vertical",
          lineHeight: 1.5,
        }}
        rows={3}
        value={node.config?.messageTemplate ?? ""}
        placeholder="Leave blank to send the raw output, or write e.g. {{ result.extract }}"
        onChange={(e) =>
          onUpdate({
            ...node,
            config: { ...node.config, messageTemplate: e.target.value },
          })
        }
      />
    </Field>
  );
}

function ConnectorConfigSection({
  node,
  workflowId,
  onUpdate,
}: {
  node: WorkflowNode;
  workflowId: string;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const spec = CONNECTOR_CONFIG_FIELDS[node.template ?? ""];
  if (!spec) return null;
  const auth = CONNECTOR_AUTH[node.template ?? ""];
  const secretFields = spec.fields.filter((f) => f.kind === "secret");
  const configFields = spec.fields.filter((f) => f.kind === "config");

  // A connector counts as "connected" only when every secret it needs is set.
  const secretSet = (key: string) => {
    const v = node.secrets?.[key];
    return v !== undefined && v !== "";
  };
  const connected =
    secretFields.length > 0 &&
    secretFields.every(
      (f) => secretSet(f.key) || (f.legacyKey !== undefined && secretSet(f.legacyKey)),
    );
  const needsLogin = auth?.needsLogin ?? true;

  const statusTone: "ok" | "warn" | "default" = connected
    ? "ok"
    : needsLogin
      ? "warn"
      : "default";
  const statusText = connected
    ? needsLogin
      ? "Connected"
      : "Token set"
    : needsLogin
      ? "Not connected"
      : "No login required";

  return (
    <>
      {spec.oauthProvider && (
        <ConnectorOAuthButton
          provider={spec.oauthProvider}
          workflowId={workflowId}
          node={node}
        />
      )}
      {secretFields.length > 0 && (
        <Section label="Authentication">
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 7,
              fontSize: 11,
              color: "var(--fg-muted)",
            }}
          >
            <StatusDot tone={statusTone} />
            {statusText}
          </div>
          {secretFields.map((f) => (
            <SecretField
              key={f.key}
              node={node}
              onUpdate={onUpdate}
              secretKey={f.key}
              label={f.label}
              hint={f.hint}
              placeholder={f.placeholder}
            />
          ))}
          {auth && <AuthDocLink href={auth.docUrl} label={auth.linkLabel} />}
        </Section>
      )}
      {configFields.length > 0 && (
        <Section label={secretFields.length > 0 ? "Setup" : spec.label}>
          {configFields.map((f) => (
            <ConfigField
              key={f.key}
              node={node}
              onUpdate={onUpdate}
              configKey={f.key}
              label={f.label}
              hint={f.hint}
              placeholder={f.placeholder}
            />
          ))}
        </Section>
      )}
      {/* email is excluded: it already has its own dedicated Body field
          (in the email-specific block above, in ActionInspector) wired to
          the same expandTemplate engine server-side -- a second generic
          field here would be redundant. */}
      {node.template !== "email" && (
        <Section label="Message">
          <MessageTemplateField node={node} onUpdate={onUpdate} />
        </Section>
      )}
    </>
  );
}

// ── Action Inspector ───────────────────────────────────────────────────────
function ActionInspector({
  node,
  workflowId,
  onUpdate,
}: {
  node: WorkflowNode;
  workflowId: string;
  onUpdate: (n: WorkflowNode) => void;
}) {
  return (
    <>
      <Section label="Action">
        <Field label="Name">
          <input
            style={inputStyle}
            value={node.name ?? ""}
            onChange={(e) => onUpdate({ ...node, name: e.target.value })}
          />
        </Field>
      </Section>

      {node.template === "email" && (
        <Section label="Email config">
          <Field label="Provider">
            <select
              style={inputStyle}
              value={node.emailProvider ?? "resend"}
              onChange={(e) =>
                onUpdate({ ...node, emailProvider: e.target.value })
              }
            >
              <option value="resend">Resend</option>
              <option value="postmark">Postmark</option>
              <option value="sendgrid">SendGrid</option>
              <option value="brevo">Brevo</option>
            </select>
          </Field>
          <Field label="API Key" hint="encrypted at rest">
            <input
              style={monoInputStyle}
              type="password"
              value={
                node.emailApiKey === "__enc__" ? "" : (node.emailApiKey ?? "")
              }
              placeholder={
                node.emailApiKey === "__enc__"
                  ? "Key set, enter to replace"
                  : node.emailProvider === "postmark"
                    ? "your-postmark-server-token"
                    : "re_xxxxxxxxxxxx"
              }
              onChange={(e) =>
                onUpdate({
                  ...node,
                  emailApiKey:
                    e.target.value ||
                    (node.emailApiKey === "__enc__" ? "__enc__" : ""),
                })
              }
            />
          </Field>
          <Field label="From" hint="must be verified in your provider">
            <input
              style={monoInputStyle}
              value={node.emailFrom ?? ""}
              placeholder="AgentMesh <you@yourdomain.com>"
              onChange={(e) => onUpdate({ ...node, emailFrom: e.target.value })}
            />
          </Field>
          <Field label="To" hint="{{ variables }} supported">
            <input
              style={monoInputStyle}
              value={node.emailTo ?? ""}
              placeholder="recipient@example.com"
              onChange={(e) => onUpdate({ ...node, emailTo: e.target.value })}
            />
          </Field>
          <Field label="Subject">
            <input
              style={inputStyle}
              value={node.emailSubject ?? ""}
              placeholder="Your AgentMesh result"
              onChange={(e) =>
                onUpdate({ ...node, emailSubject: e.target.value })
              }
            />
          </Field>
          <Field label="Body" hint="{{ result }} = agent output">
            <textarea
              style={{
                ...inputStyle,
                height: "auto",
                padding: 10,
                resize: "vertical",
                lineHeight: 1.6,
              }}
              rows={5}
              value={node.emailBody ?? ""}
              placeholder={
                "Hi,\n\nHere is your result:\n\n{{ result }}\n\nAgentMesh"
              }
              onChange={(e) => onUpdate({ ...node, emailBody: e.target.value })}
            />
          </Field>
        </Section>
      )}

      <ConnectorConfigSection
        node={node}
        workflowId={workflowId}
        onUpdate={onUpdate}
      />
    </>
  );
}

// ── Google Inspector ───────────────────────────────────────────────────────
// One connection (Config.oauthCredentialID) covers all four products --
// see backend/internal/api/handlers/oauth2creds.go's googleConnectorScopes,
// requested together in a single consent screen -- so every Google template
// shares the same "Connected account" section below, and only the
// operation-specific fields change per product.
function GoogleInspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const tpl = GOOGLE_TEMPLATES.find((t) => t.id === node.template);
  const [credentials, setCredentials] = useState<OAuthCredentialSummary[]>([]);
  const [loadingCreds, setLoadingCreds] = useState(true);

  useEffect(() => {
    let cancelled = false;
    oauth2
      .listCredentials("google")
      .then((creds) => {
        if (!cancelled) {
          setCredentials(creds);
          setLoadingCreds(false);
        }
      })
      .catch(() => {
        // Same guard as tendrilApi.credit()/machines() below -- without
        // this, a rejected fetch (network blip, backend briefly down)
        // left loadingCreds stuck true forever, showing "Loading…"
        // permanently instead of falling back to "no accounts connected".
        if (!cancelled) {
          setCredentials([]);
          setLoadingCreds(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const selectedCredID = node.config?.oauthCredentialID ?? "";
  const template = node.template ?? "";
  // usesMessage lives on the GOOGLE_TEMPLATES row itself (data.ts) so this
  // can't drift out of sync with the write-op cases in google.go the way a
  // separately maintained id list could.
  const usesMessageTemplate = tpl?.usesMessage ?? false;

  return (
    <>
      <Section label="Google">
        <Field label="Name">
          <input
            style={inputStyle}
            value={node.name ?? ""}
            placeholder={tpl?.name ?? "Google"}
            onChange={(e) => onUpdate({ ...node, name: e.target.value })}
          />
        </Field>
        <Field label="Operation">
          <input style={inputStyle} value={tpl?.name ?? template} readOnly />
        </Field>
      </Section>

      <Section label="Connected account">
        {loadingCreds ? (
          <div style={{ fontSize: 11, color: "var(--fg-dim)" }}>Loading…</div>
        ) : (
          <>
            {credentials.length === 0 ? (
              <div
                style={{
                  fontSize: 11,
                  color: "var(--fg-muted)",
                  marginBottom: 8,
                  lineHeight: 1.5,
                }}
              >
                No Google account connected yet.
              </div>
            ) : (
              <Field label="Account">
                <select
                  style={monoInputStyle}
                  value={selectedCredID}
                  onChange={(e) =>
                    onUpdate({
                      ...node,
                      config: {
                        ...node.config,
                        oauthCredentialID: e.target.value,
                      },
                    })
                  }
                >
                  <option value="">Select an account…</option>
                  {credentials.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.accountLabel || c.id}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            <button
              type="button"
              onClick={() => {
                window.location.href = oauth2.connectURL("google");
              }}
              style={{
                marginTop: 8,
                height: 32,
                width: "100%",
                border: "1px dashed var(--accent)",
                background: "var(--accent-soft)",
                color: "var(--accent)",
                borderRadius: "var(--r-2)",
                fontSize: 12,
                fontWeight: 500,
                cursor: "pointer",
              }}
            >
              + Connect Google Account
            </button>
            <div
              style={{
                fontSize: 10,
                color: "var(--fg-dim)",
                marginTop: 6,
                lineHeight: 1.5,
              }}
            >
              One connection covers Gmail, Sheets, Calendar, and Drive together.
            </div>
          </>
        )}
      </Section>

      {template.startsWith("gmail_") && (
        <GoogleGmailFields node={node} onUpdate={onUpdate} />
      )}
      {template.startsWith("sheets_") && (
        <GoogleSheetsFields node={node} onUpdate={onUpdate} />
      )}
      {template.startsWith("calendar_") && (
        <GoogleCalendarFields node={node} onUpdate={onUpdate} />
      )}
      {template.startsWith("drive_") && (
        <GoogleDriveFields node={node} onUpdate={onUpdate} />
      )}

      {usesMessageTemplate && (
        <Section label="Message">
          <MessageTemplateField node={node} onUpdate={onUpdate} />
        </Section>
      )}
    </>
  );
}

function GoogleGmailFields({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  switch (node.template) {
    case "gmail_list":
      return (
        <Section label="Search">
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="gmailQuery"
            label="Query"
            hint="Gmail search syntax, e.g. is:unread from:someone@x.com"
            placeholder="is:unread"
          />
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="gmailMaxResults"
            label="Max results"
            placeholder="10"
          />
        </Section>
      );
    case "gmail_get":
      return (
        <Section label="Message">
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="gmailMessageID"
            label="Message ID"
            hint="e.g. {{ result.id }} from an upstream Gmail: List step"
          />
        </Section>
      );
    case "gmail_send":
    case "gmail_reply":
      return (
        <Section label="Send">
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="gmailTo"
            label="To"
            placeholder="recipient@example.com"
          />
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="gmailSubject"
            label="Subject"
            placeholder="AgentMesh workflow result"
          />
          {node.template === "gmail_reply" && (
            <ConfigField
              node={node}
              onUpdate={onUpdate}
              configKey="gmailThreadID"
              label="Thread ID"
              hint="keeps the reply in the original thread, e.g. {{ result.threadId }}"
            />
          )}
        </Section>
      );
    default:
      return null;
  }
}

function GoogleSheetsFields({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  return (
    <Section label="Spreadsheet">
      <ConfigField
        node={node}
        onUpdate={onUpdate}
        configKey="sheetsSpreadsheetID"
        label="Spreadsheet ID"
        hint="the long id in the sheet's URL"
      />
      <ConfigField
        node={node}
        onUpdate={onUpdate}
        configKey="sheetsRange"
        label="Range"
        placeholder="Sheet1!A1:Z1000"
      />
    </Section>
  );
}

function GoogleCalendarFields({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  return (
    <Section label="Calendar">
      <ConfigField
        node={node}
        onUpdate={onUpdate}
        configKey="calendarID"
        label="Calendar ID"
        hint="leave blank for your primary calendar"
        placeholder="primary"
      />
      {node.template === "calendar_create" && (
        <>
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="calendarSummary"
            label="Title"
            hint="leave blank to use the Message field below"
          />
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="calendarStart"
            label="Start"
            placeholder="2026-08-10T10:00:00Z"
          />
          <ConfigField
            node={node}
            onUpdate={onUpdate}
            configKey="calendarEnd"
            label="End"
            placeholder="2026-08-10T11:00:00Z"
          />
        </>
      )}
    </Section>
  );
}

function GoogleDriveFields({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  if (node.template === "drive_list") {
    return (
      <Section label="Search">
        <ConfigField
          node={node}
          onUpdate={onUpdate}
          configKey="driveQuery"
          label="Query"
          hint="Drive search syntax, e.g. name contains 'report'"
        />
      </Section>
    );
  }
  return (
    <Section label="File">
      <ConfigField
        node={node}
        onUpdate={onUpdate}
        configKey="driveFileID"
        label="File ID"
        hint="e.g. {{ result.id }} from an upstream Drive: List step"
      />
    </Section>
  );
}

// ── Tendril Inspector ──────────────────────────────────────────────────────
function TendrilInspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const [credit, setCredit] = useState<number | null>(null);
  const [machines, setMachines] = useState<TendrilMachine[]>([]);
  const action = node.tendrilAction ?? "rent";

  useEffect(() => {
    tendrilApi
      .credit()
      .then(setCredit)
      .catch(() => setCredit(null));
  }, []);

  useEffect(() => {
    if (action !== "rent") return;
    tendrilApi
      .machines()
      .then(setMachines)
      .catch(() => setMachines([]));
  }, [action]);

  const selectedMachine =
    machines.find((m) => m.id === node.tendrilNodeId) ?? machines[0];
  const hours = parseFloat(node.tendrilHours || "1") || 1;
  // Tendril-credit-only (hours, no gate fee) -- matches the "of Tendril
  // credit" label below exactly. The gate fee is a separate real charge
  // billed in AgentMesh credit, not drawn from this balance.
  const cost = selectedMachine
    ? estimateLeaseHoursCostUSD(selectedMachine.pricePerHourUsd, hours)
    : null;
  const creditVal = credit ?? 0;
  const topupAmount = parseFloat(node.tendrilAmount || "0") || 0;

  const custom = node.customParams ?? [];
  const payloadValue = custom.find((p) => p.name === "payload")?.value ?? "";
  const setPayload = (value: string) => {
    const next = custom.some((p) => p.name === "payload")
      ? custom.map((p) => (p.name === "payload" ? { ...p, value } : p))
      : [...custom, { name: "payload", kind: "text" as const, value }];
    onUpdate({ ...node, customParams: next });
  };

  return (
    <>
      <div style={{ fontSize: 12, opacity: 0.85 }}>
        Tendril credit: <strong>${creditVal.toFixed(2)}</strong>
        {selectedMachine && (
          <>
            {" "}
            — about {(creditVal / selectedMachine.pricePerHourUsd).toFixed(1)} h
            on {selectedMachine.label || selectedMachine.id}
          </>
        )}
        <div style={{ opacity: 0.6, marginTop: 2 }}>
          Separate from your AgentMesh credits. Buy more with a Topup node.
        </div>
      </div>

      <Section label="Action">
        <Field label="Action">
          <select
            style={monoInputStyle}
            value={action}
            onChange={(e) =>
              onUpdate({
                ...node,
                tendrilAction: e.target.value as WorkflowNode["tendrilAction"],
              })
            }
          >
            <option value="topup">Buy Tendril Credit</option>
            <option value="rent">Rent a Machine</option>
            <option value="run">Run a Job</option>
            <option value="release">Release</option>
          </select>
        </Field>
      </Section>

      {action === "topup" && (
        <Section label="Topup">
          <Field label="Amount (USD)">
            <input
              style={monoInputStyle}
              type="number"
              min="0.1"
              step="0.5"
              value={node.tendrilAmount ?? "10"}
              onChange={(e) =>
                onUpdate({ ...node, tendrilAmount: e.target.value })
              }
            />
          </Field>
          <div style={{ fontSize: 11, color: "var(--fg-dim)" }}>
            Converts ${topupAmount.toFixed(2)} of your AgentMesh credits into
            Tendril credit.
          </div>
        </Section>
      )}

      {action === "rent" && (
        <Section label="Rent">
          <Field label="Machine">
            <select
              style={monoInputStyle}
              value={node.tendrilNodeId ?? ""}
              onChange={(e) =>
                onUpdate({ ...node, tendrilNodeId: e.target.value })
              }
            >
              <option value="">Cheapest online</option>
              {machines.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.label || m.id} — {m.cpuCores} vCPU,{" "}
                  {Math.round(m.ramMb / 1024)} GB — ${m.pricePerHourUsd}/hr
                </option>
              ))}
            </select>
          </Field>
          <Field label="Hours">
            <input
              style={monoInputStyle}
              type="number"
              min="0.5"
              step="0.5"
              max="24"
              value={node.tendrilHours ?? "1"}
              onChange={(e) =>
                onUpdate({ ...node, tendrilHours: e.target.value })
              }
            />
          </Field>
          {cost != null && (
            <div
              style={{
                fontSize: 11,
                color: cost > creditVal ? "var(--danger)" : "var(--fg-dim)",
              }}
            >
              Costs ${cost.toFixed(2)} of Tendril credit
              {cost > creditVal &&
                " — not enough Tendril credit, add a Topup node"}
            </div>
          )}
        </Section>
      )}

      {action === "run" && (
        <Section label="Run">
          <Field label="Payload (Python)">
            <textarea
              style={{ ...monoInputStyle, height: 120, resize: "vertical" }}
              value={payloadValue}
              onChange={(e) => setPayload(e.target.value)}
            />
          </Field>
        </Section>
      )}
    </>
  );
}

// ── End Inspector ──────────────────────────────────────────────────────────
function EndInspector({
  node,
  onUpdate,
}: {
  node: WorkflowNode;
  onUpdate: (n: WorkflowNode) => void;
}) {
  const tpl = END_TEMPLATES.find((t) => t.id === node.template);
  return (
    <Section label="End">
      {node.custom ? (
        <Field label="Label">
          <input
            style={inputStyle}
            value={node.label ?? ""}
            placeholder="Mark complete"
            onChange={(e) => onUpdate({ ...node, label: e.target.value })}
          />
        </Field>
      ) : (
        <Field label="Type">
          <input style={inputStyle} value={tpl?.name ?? ""} readOnly />
        </Field>
      )}
      <Field label="Status code">
        <input style={monoInputStyle} defaultValue="200" />
      </Field>
    </Section>
  );
}
