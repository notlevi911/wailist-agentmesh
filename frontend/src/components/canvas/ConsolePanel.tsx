"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { Pill } from "@/components/ui";
import type { DeadLetterRun } from "@/lib/api";
import {
  isX402Payment,
  type LogEvent,
  type X402Payment,
} from "./useRunTranscript";
import { latestPerNode } from "./chat/resolveReply";

interface ConsolePanelProps {
  open: boolean;
  onToggle: () => void;
  runId: string | null;
  running: boolean;
  logs: LogEvent[];
  elapsed: number | null;
  done: boolean;
  deadLetters: DeadLetterRun[];
  // Takes the dead-letter's own runId rather than relying solely on the
  // caller's live session state: a dead-letter row restored from
  // useRunTranscript's cache (see CachedRun.deadLetters) can render with no
  // live runId in this session at all -- see CanvasPage's handleResume.
  onResume: (runId: string) => void;
}

const HEIGHT_KEY = "agentmesh_console_height";
const DEFAULT_HEIGHT = 240;
const MIN_HEIGHT = 120;
// Width at or under which a log row stacks instead of using fixed columns.
const COMPACT_ROW_MAX_WIDTH = 520;
// Leave room for the topbar and some canvas: a console dragged to fill the
// window would hide the graph it is reporting on.
const maxHeight = () =>
  typeof window === "undefined"
    ? 640
    : Math.max(MIN_HEIGHT, window.innerHeight - 220);

export function ConsolePanel({
  open,
  onToggle,
  runId,
  running,
  logs,
  elapsed,
  done,
  deadLetters,
  onResume,
}: ConsolePanelProps) {
  const [height, setHeight] = useState(DEFAULT_HEIGHT);
  const [resizing, setResizing] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  // Below this the four fixed columns (52 + 34 + 110 + gaps) leave the output
  // cell so little room that JSON wraps a few characters per line -- a single
  // step measured 431px tall in a 293px-wide console. Rows switch to a stacked
  // layout instead of squeezing.
  const [compactRows, setCompactRows] = useState(false);
  const logListRef = useRef<HTMLDivElement | null>(null);
  // Observed on the list, not the window: the console's width is whatever is
  // left after the palette and the right rail, so a viewport-width breakpoint
  // would fire at the wrong times.
  const attachLogList = useCallback((el: HTMLDivElement | null) => {
    logListRef.current = el;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      setCompactRows(el.getBoundingClientRect().width < COMPACT_ROW_MAX_WIDTH);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Restore the last chosen console height. Read in an effect rather than in
  // useState's initializer so the server render and first client render agree.
  // Applied on the next frame rather than synchronously: the server renders
  // the default height, so setting the stored one during the effect body
  // would both trip the cascading-render rule and mismatch hydration.
  useEffect(() => {
    const id = requestAnimationFrame(() => {
      try {
        const saved = Number(window.localStorage.getItem(HEIGHT_KEY));
        if (saved >= MIN_HEIGHT) setHeight(Math.min(saved, maxHeight()));
      } catch {
        /* unavailable storage: keep the default */
      }
    });
    return () => cancelAnimationFrame(id);
  }, []);

  // Drag the top edge to resize. Listeners live on the window, not the handle,
  // so a fast drag that outruns the pointer doesn't drop the gesture.
  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    setResizing(true);
    const startY = e.clientY;
    const startHeight = height;
    const onMove = (ev: MouseEvent) => {
      // Dragging up grows the console, hence the inverted delta.
      const next = Math.min(
        maxHeight(),
        Math.max(MIN_HEIGHT, startHeight + (startY - ev.clientY)),
      );
      setHeight(next);
    };
    const onUp = () => {
      setResizing(false);
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      setHeight((h) => {
        try {
          window.localStorage.setItem(HEIGHT_KEY, String(h));
        } catch {
          /* best-effort */
        }
        return h;
      });
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  };

  // Auto-scroll to bottom on new logs
  useEffect(() => {
    if (open) bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs, deadLetters, open]);

  const statusColor = (s: LogEvent["status"]) => {
    if (s === "success") return "var(--accent)";
    if (s === "failed") return "#F87171";
    if (s === "stopped") return "#FB923C";
    return "var(--fg-dim)";
  };

  const statusLabel = (s: LogEvent["status"]) => {
    if (s === "success") return "OK";
    if (s === "failed") return "ERR";
    if (s === "stopped") return "STP";
    return "RUN";
  };

  const elapsedStr = (elapsed ?? 0).toFixed(1);
  const headerPill = runId
    ? done
      ? `run · ${runId.slice(0, 8)} · ${elapsedStr}s`
      : running
        ? `running · ${elapsedStr}s`
        : `run · ${runId.slice(0, 8)}`
    : null;

  const pillTone = done ? "ok" : running ? "warm" : "default";

  return (
    <div
      style={{
        position: "absolute",
        left: 0,
        right: 0,
        bottom: 0,
        background: "var(--bg-elev-1)",
        borderTop: "1px solid var(--border)",
        height: open ? height : 32,
        // No animation mid-drag: the tween lags the pointer and feels broken.
        transition: resizing ? "none" : "height .2s var(--ease)",
        display: "flex",
        flexDirection: "column",
        zIndex: 5,
      }}
    >
      {/* Resize grip. Sits above the header and slightly outside the drawer
          so it stays grabbable without stealing clicks from the header's
          expand/collapse toggle. */}
      {open && (
        <div
          onMouseDown={startResize}
          title="Drag to resize console"
          style={{
            position: "absolute",
            top: -3,
            left: 0,
            right: 0,
            height: 7,
            cursor: "ns-resize",
            zIndex: 6,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <div
            style={{
              width: 40,
              height: 3,
              borderRadius: 2,
              background: resizing ? "var(--accent)" : "var(--border-strong)",
            }}
          />
        </div>
      )}

      {/* Header bar */}
      <div
        onClick={onToggle}
        style={{
          height: 32,
          padding: "0 14px",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          cursor: "pointer",
          borderBottom: open ? "1px solid var(--border)" : "none",
          flexShrink: 0,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              color: "var(--fg-muted)",
              textTransform: "uppercase",
              letterSpacing: "0.08em",
            }}
          >
            logs
          </span>
          {headerPill && (
            <Pill mono tone={pillTone} dot={running}>
              {headerPill}
            </Pill>
          )}
          {logs.length > 0 && (
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--fg-dim)",
              }}
            >
              {logs.length} step{logs.length !== 1 ? "s" : ""}
            </span>
          )}
        </div>
        <span
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 5,
            fontFamily: "var(--font-mono)",
            fontSize: 13,
            color: "var(--fg-muted)",
          }}
        >
          <span style={{ fontSize: 15, lineHeight: 1 }}>
            {open ? "▾" : "▴"}
          </span>
          {open ? "collapse" : "expand"}
        </span>
      </div>

      {/* Body: the run's log lines, and nothing else. Node config and the
          Tendril terminal live in the right rail (see CanvasPage) so opening
          either no longer pushes the transcript off screen. */}
      {open && (
        <div
          ref={attachLogList}
          style={{
            flex: 1,
            overflow: "auto",
            padding: "6px 14px 10px",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            lineHeight: 1.7,
          }}
        >
          {logs.length === 0 && !running && !runId && (
            <div style={{ color: "var(--fg-dim)", paddingTop: 8 }}>
              run a workflow to see execution logs here.
            </div>
          )}
          {logs.length === 0 && running && (
            <div style={{ color: "var(--fg-dim)", paddingTop: 8 }}>
              waiting for first node…
            </div>
          )}
          {logs.map((l, i) => (
            <div
              key={i}
              style={{
                display: "grid",
                gridTemplateColumns: compactRows
                  ? "1fr"
                  : "52px 34px 110px 1fr",
                gap: compactRows ? 2 : 10,
                alignItems: compactRows ? "stretch" : "baseline",
                borderBottom: "1px solid var(--border-soft)",
                padding: "3px 0",
              }}
            >
              {/* display:contents keeps these three as real grid cells in the
                    wide layout; in compact mode they collapse onto one meta
                    line above the output instead of each taking a row. */}
              <div
                style={
                  compactRows
                    ? {
                        display: "flex",
                        gap: 8,
                        alignItems: "baseline",
                        minWidth: 0,
                      }
                    : { display: "contents" }
                }
              >
                <span style={{ color: "var(--fg-dim)", fontSize: 9.5 }}>
                  {new Date(l.ts).toLocaleTimeString("en", {
                    hour12: false,
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                  })}
                </span>
                <span
                  style={{
                    color: statusColor(l.status),
                    fontWeight: 600,
                    fontSize: 9.5,
                  }}
                >
                  {statusLabel(l.status)}
                </span>
                <span
                  style={{
                    color: "var(--fg-muted)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  <span style={{ color: nodeTypeColor(l.nodeType) }}>
                    {l.nodeType}
                  </span>
                  {l.durationMs > 0 && (
                    <span style={{ color: "var(--fg-dim)" }}>
                      {" "}
                      · {l.durationMs}ms
                    </span>
                  )}
                </span>
              </div>
              <OutputCell output={l.output} />
            </div>
          ))}
          {deadLetters.map((dl) => (
            <DeadLetterRow key={dl.id} dl={dl} onResume={onResume} />
          ))}
          {done &&
            (() => {
              // Each node's FINAL attempt decides this, not a raw scan over
              // `logs`: a resumed node that failed then succeeded still has
              // its stale "failed" row sitting in `logs` (see
              // latestPerNode's doc comment), and a raw scan would still
              // report the run as failed for that node even though it went
              // on to succeed.
              const final = latestPerNode(logs);
              const succeeded = final.filter(
                (l) => l.status === "success",
              ).length;
              const hasFailure = final.some((l) => l.status === "failed");
              return (
                <div
                  style={{
                    color: hasFailure ? "#F87171" : "var(--accent)",
                    paddingTop: 6,
                    fontSize: 10,
                  }}
                >
                  {hasFailure ? "✕ run failed" : "✓ run complete"} ·{" "}
                  {(elapsed ?? 0).toFixed(1)}s · {succeeded}/{final.length} nodes
                  succeeded
                </div>
              );
            })()}
          <div ref={bottomRef} />
        </div>
      )}
    </div>
  );
}

// DeadLetterRow renders one node that exhausted retry or hit a
// non-retryable error. Resume is a two-click confirm in place, matching
// RowMenu's delete pattern -- this codebase deliberately avoids
// window.confirm() (see RowMenu.tsx's comment on why).
function DeadLetterRow({
  dl,
  onResume,
}: {
  dl: DeadLetterRun;
  onResume: (runId: string) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  return (
    <div
      style={{
        padding: "6px 0",
        borderBottom: "1px solid var(--border-soft)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
        }}
      >
        <Pill mono tone="danger" dot>
          dead-lettered
        </Pill>
        <span style={{ color: "var(--fg-muted)", fontSize: 10.5 }}>
          {dl.nodeId} · attempt {dl.attemptCount}
        </span>
        <button
          onClick={() => {
            if (!confirming) {
              setConfirming(true);
              return;
            }
            setConfirming(false);
            onResume(dl.runId);
          }}
          onBlur={() => setConfirming(false)}
          style={{
            marginLeft: "auto",
            fontFamily: "var(--font-mono)",
            fontSize: 10.5,
            padding: "3px 8px",
            borderRadius: 4,
            border: "1px solid var(--border-strong)",
            background: confirming ? "var(--accent-soft)" : "var(--bg-elev-2)",
            color: confirming ? "var(--accent)" : "var(--fg)",
            cursor: "pointer",
          }}
        >
          {confirming ? "Confirm — completed steps won't re-run" : "Resume"}
        </button>
      </div>
      <div style={{ color: "var(--fg-dim)", fontSize: 10, paddingTop: 2 }}>
        {dl.error}
      </div>
    </div>
  );
}

function formatSize(chars: number): string {
  return chars < 1024 ? `${chars} B` : `${(chars / 1024).toFixed(1)} KB`;
}

// OutputCell renders a step's result: the settlement receipt (amount + both
// on-chain tx ids) when the step paid for something, AND the payload the
// endpoint actually returned. Previously these were either/or — a paid
// tool402 step matched the receipt branch and its response body was never
// shown at all, so the whole point of the call (the data) was invisible.
// Large payloads collapse behind a toggle rather than flooding the console.
function OutputCell({ output }: { output: unknown }) {
  const [expanded, setExpanded] = useState(false);

  const payment = isX402Payment(output) ? output : null;
  // A paid step's own response lives under `response`; an unpaid step's
  // output IS the payload.
  const payload = payment
    ? (output as { response?: unknown }).response
    : output;

  let text = "";
  if (payload !== null && payload !== undefined) {
    text =
      typeof payload === "string" ? payload : JSON.stringify(payload, null, 2);
  }
  const collapsible = text.length > 200;

  return (
    <div style={{ minWidth: 0 }}>
      {payment && (
        <span
          style={{
            display: "flex",
            alignItems: "center",
            gap: 5,
            flexWrap: "wrap",
          }}
        >
          {/* Which endpoint this payment was for. A run-funded agent emits
              one row for the run's up-front funding settlement plus one per
              tool call, all against the same tx id — without a label they
              read as duplicate charges, and an agent with several attached
              tool402 nodes gave no way to tell its payment rows apart. */}
          {payment.nodeName && (
            <span style={{ color: "var(--fg-dim)" }}>{payment.nodeName} ·</span>
          )}
          <span style={{ color: "var(--fg)" }}>
            {payment.amount
              ? `${parseFloat(payment.amount).toFixed(6)} paid`
              : "paid"}
          </span>
          {payment.txId && payment.explorerURL && (
            <>
              <span style={{ color: "var(--fg-dim)" }}>· in</span>
              <a
                href={payment.explorerURL}
                target="_blank"
                rel="noopener noreferrer"
                style={txLinkStyle}
                onClick={(e) => e.stopPropagation()}
              >
                {payment.txId.slice(0, 8)}…
              </a>
            </>
          )}
          {payment.outboundTxId && payment.outboundExplorerURL && (
            <>
              <span style={{ color: "var(--fg-dim)" }}>· out</span>
              <a
                href={payment.outboundExplorerURL}
                target="_blank"
                rel="noopener noreferrer"
                style={txLinkStyle}
                onClick={(e) => e.stopPropagation()}
              >
                {payment.outboundTxId.slice(0, 8)}…
              </a>
            </>
          )}
        </span>
      )}
      {text === "" ? (
        !payment && <span style={{ color: "var(--fg-dim)" }}>—</span>
      ) : collapsible ? (
        <>
          <button
            onClick={() => setExpanded((v) => !v)}
            style={{
              background: "none",
              border: "none",
              padding: 0,
              cursor: "pointer",
              color: "var(--accent)",
              fontFamily: "var(--font-mono)",
              fontSize: 10,
            }}
          >
            {expanded ? "▾" : "▸"} response · {formatSize(text.length)}
          </button>
          {expanded && (
            <pre
              style={{
                margin: "4px 0 0",
                padding: 8,
                maxHeight: 220,
                overflow: "auto",
                background: "var(--bg)",
                border: "1px solid var(--border)",
                borderRadius: 4,
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                lineHeight: 1.5,
                color: "var(--fg)",
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
              }}
            >
              {text}
            </pre>
          )}
        </>
      ) : (
        <span
          style={{
            color: "var(--fg)",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {text}
        </span>
      )}
    </div>
  );
}

const txLinkStyle: React.CSSProperties = {
  color: "#E879F9",
  textDecoration: "underline",
  fontFamily: "var(--font-mono)",
  fontSize: 9.5,
  whiteSpace: "nowrap",
};

function nodeTypeColor(t: string): string {
  if (t === "agent") return "var(--accent)";
  if (t === "tool402") return "#E879F9";
  if (t === "action") return "#FB923C";
  return "var(--fg-dim)";
}

export type { LogEvent, X402Payment };
