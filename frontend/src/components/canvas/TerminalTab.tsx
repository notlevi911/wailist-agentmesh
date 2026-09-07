"use client";
import { useEffect, useRef } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

// The SSE stream already bypasses Next's /api rewrite because that proxy does
// not hold long-lived connections open (see useRunTranscript's SSE_BASE
// comment). A
// WebSocket has exactly the same problem, so it dials the backend directly for
// exactly the same reason.
const WS_BASE = process.env.NEXT_PUBLIC_API_URL ?? "";

// xterm's theme takes literal colors, not CSS custom properties, so this has
// to mirror --bg-elev-1 in globals.css by hand -- keep the two in step.
const BG_ELEV_1 = "#0f0e18";

export function TerminalTab({
  leaseId,
  onClose,
}: {
  leaseId: string;
  onClose: () => void;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const term = new Terminal({
      convertEol: true,
      fontSize: 12,
      fontFamily:
        "var(--font-geist-mono), ui-monospace, SFMono-Regular, Menlo, monospace",
      // Matches the rail it is embedded in; a hardcoded near-black left a
      // visible seam against --bg-elev-1.
      theme: { background: BG_ELEV_1 },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    const base = WS_BASE.replace(/^http/, "ws");
    const ws = new WebSocket(`${base}/leases/${leaseId}/terminal`);
    ws.binaryType = "arraybuffer";

    const sendResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }),
        );
      }
    };

    // Dragging the rail's resize handle delivers a ResizeObserver callback per
    // frame; refitting and sending a PTY resize frame on each one costs ~60 of
    // both for a one-second drag. Coalesce to the trailing edge instead.
    let resizeTimer: ReturnType<typeof setTimeout> | null = null;
    const queueResize = () => {
      if (resizeTimer !== null) clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        resizeTimer = null;
        sendResize();
      }, 80);
    };

    ws.onopen = () => {
      term.writeln(
        "\x1b[2m connected — this is a real machine you are paying for \x1b[0m",
      );
      sendResize();
    };
    ws.onmessage = (ev) => {
      term.write(
        typeof ev.data === "string"
          ? ev.data
          : new Uint8Array(ev.data as ArrayBuffer),
      );
    };
    // A close here is the SSH connection to the machine dropping (or never
    // opening — LeaseTerminal accepts the WebSocket first, then dials SSH,
    // so "connected" can print before a dial/auth failure closes it right
    // after). It never touches the lease itself: releasing is only ever the
    // explicit Release button or the lease's own funded-window reaper,
    // neither of which this handler calls.
    ws.onclose = (ev) => {
      const reason = ev.reason ? ` — ${ev.reason}` : "";
      term.writeln(
        `\r\n\x1b[2m disconnected${reason} (the lease itself is unaffected — reopen the terminal or check "Online machines" if the box dropped) \x1b[0m`,
      );
    };

    const keys = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data);
    });

    const observer = new ResizeObserver(queueResize);
    observer.observe(host);

    return () => {
      if (resizeTimer !== null) clearTimeout(resizeTimer);
      observer.disconnect();
      keys.dispose();
      ws.close();
      term.dispose();
    };
  }, [leaseId]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          display: "flex",
          justifyContent: "flex-end",
          padding: "4px 8px",
        }}
      >
        <button
          onClick={onClose}
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 9.5,
            textTransform: "uppercase",
            letterSpacing: "0.06em",
            padding: "2px 8px",
            borderRadius: 999,
            border: "1px solid var(--border)",
            color: "var(--fg-dim)",
            background: "transparent",
            cursor: "pointer",
          }}
        >
          close terminal
        </button>
      </div>
      <div ref={hostRef} style={{ flex: 1, minHeight: 0 }} />
    </div>
  );
}
