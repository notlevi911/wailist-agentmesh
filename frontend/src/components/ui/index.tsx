"use client";
import React from "react";

// ── Logo ──────────────────────────────────────────────────────────────────
// /logo-mark.png is a 64x64 downscale of /logo.png (the actual brand mark --
// also what layout.tsx points the favicon/apple-touch icon at,
// metadata.icons) -- so the wordmark badge matches the tab icon instead of
// the hand-drawn mesh glyph this used to render on its own. The full-size
// /logo.png is ~220KB; every call site here renders at 14-20px, so this
// component uses the small pre-shrunk copy (~7KB) rather than shipping a
// favicon-resolution PNG to display an icon this size -- regenerate it with
// `magick public/logo.png -resize 64x64 -strip public/logo-mark.png` if
// /logo.png itself ever changes.
export function Logo({ size = 18 }: { size?: number }) {
  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src="/logo-mark.png"
        alt=""
        width={size}
        height={size}
        style={{ borderRadius: size * 0.22, flexShrink: 0 }}
      />
      <span
        style={{
          fontFamily: "var(--font-sans)",
          fontWeight: 600,
          fontSize: size * 0.85,
          letterSpacing: "-0.02em",
          color: "var(--fg)",
        }}
      >
        AgentMesh
      </span>
    </div>
  );
}

// ── Pill ─────────────────────────────────────────────────────────────────
type PillTone = "default" | "accent" | "warm" | "danger" | "ok";
export function Pill({
  children,
  tone = "default",
  mono = false,
  dot = false,
}: {
  children: React.ReactNode;
  tone?: PillTone;
  mono?: boolean;
  dot?: boolean;
}) {
  const tones: Record<PillTone, { bg: string; fg: string; border: string }> = {
    default: {
      bg: "var(--bg-elev-2)",
      fg: "var(--fg-muted)",
      border: "var(--border)",
    },
    accent: {
      bg: "var(--accent-soft)",
      fg: "var(--accent)",
      border: "var(--accent-line)",
    },
    warm: {
      bg: "var(--warm-soft)",
      fg: "var(--warm)",
      border: "rgba(255,181,71,0.35)",
    },
    danger: {
      bg: "rgba(255,92,92,0.10)",
      fg: "var(--danger)",
      border: "rgba(255,92,92,0.35)",
    },
    ok: {
      bg: "rgba(167,140,250,0.10)",
      fg: "var(--accent)",
      border: "var(--accent-line)",
    },
  };
  const s = tones[tone];
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        height: 22,
        padding: "0 8px",
        whiteSpace: "nowrap",
        flexShrink: 0,
        borderRadius: 999,
        border: `1px solid ${s.border}`,
        background: s.bg,
        color: s.fg,
        fontSize: 11,
        fontWeight: 500,
        fontFamily: mono ? "var(--font-mono)" : "var(--font-sans)",
        letterSpacing: mono ? "0.02em" : "-0.01em",
      }}
    >
      {dot && (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: 999,
            background: s.fg,
            display: "inline-block",
          }}
        />
      )}
      {children}
    </span>
  );
}

// ── Card ─────────────────────────────────────────────────────────────────
// The standard elevated panel (bg-elev-1 / border / r-3 / 16px padding) used
// across the workflows and usage pages. Override via style (e.g. padding: 0)
// and attach handlers as needed -- extra props spread onto the div.
export function Card({ style, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      {...rest}
      style={{
        background: "var(--bg-elev-1)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-3)",
        padding: 16,
        ...style,
      }}
    />
  );
}
// Shared button styles live in ./buttons, which holds every one of them
// together with the contract that keeps a label inside its box. Re-exported
// here because this barrel is what most of the app imports from.
export { ghostBtnSm } from "./buttons";

// ── Tag ──────────────────────────────────────────────────────────────────
export function Tag({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "baseline",
        gap: 6,
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        color: "var(--fg-muted)",
        letterSpacing: "0.04em",
        textTransform: "uppercase",
      }}
    >
      <span
        style={{
          width: 4,
          height: 4,
          background: "var(--accent)",
          borderRadius: 999,
          display: "inline-block",
          alignSelf: "center",
        }}
      />
      {children}
    </span>
  );
}

// ── Hairline ─────────────────────────────────────────────────────────────
export function Hairline({
  vertical = false,
  length = "100%",
  className,
}: {
  vertical?: boolean;
  length?: string | number;
  // Lets callers attach a responsive utility (e.g. `hide-md`) to a separator
  // whose neighbours drop out at a breakpoint.
  className?: string;
}) {
  return (
    <div
      className={className}
      style={{
        background: "var(--border)",
        width: vertical ? 1 : length,
        height: vertical ? length : 1,
        flexShrink: 0,
      }}
    />
  );
}

// ── StatusDot ────────────────────────────────────────────────────────────
export function StatusDot({
  tone = "ok",
  size = 8,
}: {
  tone?: "ok" | "warn" | "err" | "default";
  size?: number;
}) {
  const c =
    tone === "ok"
      ? "var(--accent)"
      : tone === "warn"
        ? "var(--warm)"
        : tone === "err"
          ? "var(--danger)"
          : "var(--fg-dim)";
  return (
    <span
      style={{
        display: "inline-block",
        width: size,
        height: size,
        borderRadius: 999,
        background: c,
        boxShadow: tone === "ok" ? `0 0 8px ${c}` : "none",
      }}
    />
  );
}

// ── Icons ─────────────────────────────────────────────────────────────────
export const IconArrow = ({ size = 14 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none">
    <path
      d="M3 8 L13 8 M9 4 L13 8 L9 12"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export const IconPlay = ({ size = 12 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 12 12" fill="currentColor">
    <path d="M3 2 L10 6 L3 10 Z" />
  </svg>
);

export const IconStop = ({ size = 10 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 10 10" fill="currentColor">
    <rect x="2" y="2" width="6" height="6" rx="1" />
  </svg>
);

export const IconSearch = ({ size = 14 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none">
    <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
    <path
      d="M11 11 L14 14"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
    />
  </svg>
);

export const IconClose = ({ size = 14 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none">
    <path
      d="M3 3 L13 13 M13 3 L3 13"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
    />
  </svg>
);

export const IconBackspace = ({ size = 12 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
    style={{ display: "block" }}
  >
    <path
      d="M6 3.5h6.5a1.5 1.5 0 0 1 1.5 1.5v6a1.5 1.5 0 0 1-1.5 1.5H6L1.5 8z"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
    <path
      d="M7.5 6.5l3 3M10.5 6.5l-3 3"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
    />
  </svg>
);

export const IconChat = ({ size = 11 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
    style={{ display: "block" }}
  >
    <path
      d="M4.5 3h7a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H7.5L4.5 13.5V11a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export const IconInspect = ({ size = 11 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
    style={{ display: "block" }}
  >
    <path
      d="M2.5 5h2.5M8 5h5.5M2.5 11h5.5M11 11h2.5"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
    />
    <circle cx="6.5" cy="5" r="1.5" stroke="currentColor" strokeWidth="1.4" />
    <circle cx="9.5" cy="11" r="1.5" stroke="currentColor" strokeWidth="1.4" />
  </svg>
);

export const IconGrid = ({ size = 14 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.3"
  >
    <rect x="2" y="2" width="5" height="5" />
    <rect x="9" y="2" width="5" height="5" />
    <rect x="2" y="9" width="5" height="5" />
    <rect x="9" y="9" width="5" height="5" />
  </svg>
);

export const IconSpeaker = ({ size = 12 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
    style={{ display: "block" }}
  >
    <path d="M2 6h2.5L8.5 3v10L4.5 10H2z" fill="currentColor" />
    <path
      d="M10.5 5.3a4 4 0 0 1 0 5.4M12.3 3.7a6.5 6.5 0 0 1 0 8.6"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
    />
  </svg>
);

export const IconMic = ({ size = 12 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
    style={{ display: "block" }}
  >
    <rect x="5.5" y="1.5" width="5" height="8" rx="2.5" stroke="currentColor" strokeWidth="1.3" />
    <path
      d="M3.5 7.5a4.5 4.5 0 0 0 9 0M8 12v2.5M5.5 14.5h5"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
    />
  </svg>
);

export const IconWallet = ({ size = 14 }: { size?: number }) => (
  <svg width={size} height={size} viewBox="0 0 16 16" fill="none">
    <rect
      x="2"
      y="4"
      width="12"
      height="9"
      rx="1.5"
      stroke="currentColor"
      strokeWidth="1.3"
    />
    <path d="M2 7 H14" stroke="currentColor" strokeWidth="1.3" />
    <circle cx="11" cy="10" r="1" fill="currentColor" />
  </svg>
);

// ── Toast ─────────────────────────────────────────────────────────────────
export function Toast({ message }: { message: string }) {
  return (
    <div
      style={{
        position: "fixed",
        bottom: 24,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 9999,
        background: "var(--bg-elev-3)",
        border: "1px solid var(--accent-line)",
        color: "var(--fg)",
        padding: "10px 16px",
        borderRadius: "var(--r-2)",
        fontFamily: "var(--font-mono)",
        fontSize: 12,
        boxShadow: "0 10px 32px rgba(0,0,0,0.5)",
        display: "flex",
        alignItems: "center",
        gap: 10,
        animation: "fade-up 0.25s var(--ease)",
      }}
    >
      <StatusDot tone="ok" /> {message}
    </div>
  );
}
