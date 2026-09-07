import type { CSSProperties } from "react";

// Every shared button style in the app, in one place, with the rules that keep
// a label inside its own box baked in.
//
// These were previously nine separate declarations across seven files. All nine
// pinned a fixed `height`, only one set `whiteSpace: nowrap`, only one set
// `flexShrink: 0`, and none set both -- so the same failure kept reappearing:
// a toolbar runs out of room, the button shrinks (flex-shrink defaults to 1),
// the label wraps onto a second line, the height stays pinned, and the text
// renders straight through the border. `ghostBtnSm` was also declared twice,
// identically, so the name meant two things depending on where you imported it.
//
// ── The contract ─────────────────────────────────────────────────────────
// 1. `whiteSpace: "nowrap"` -- a label never wraps inside its own box.
// 2. `flexShrink: 0`        -- the button keeps its natural width. When a row
//                              runs out of space the ROW wraps; the button does
//                              not get squeezed. (Rows need `flexWrap: "wrap"`.)
// 3. `minHeight`, not `height` -- the safety net. If content ever does exceed
//                              the box despite 1 and 2, the box grows instead of
//                              spilling. This is what turns the bug from
//                              "unlikely" into "impossible".
//
// buttons.test.ts enforces all three on every export below, so a new button
// cannot reintroduce the problem.

// Shared by every button that carries a text label.
const labelBase: CSSProperties = {
  whiteSpace: "nowrap",
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  cursor: "pointer",
  fontFamily: "var(--font-sans)",
  borderRadius: "var(--r-2)",
};

// Small ghost button: topbars and row actions. A style const (not a component)
// so callers can spread-extend it: { ...ghostBtnSm, width: 28 }.
export const ghostBtnSm: CSSProperties = {
  ...labelBase,
  minHeight: 28,
  padding: "0 10px",
  fontSize: 12,
  fontWeight: 500,
  background: "transparent",
  border: "1px solid var(--border-strong)",
  color: "var(--fg-muted)",
  gap: 4,
};

// Page-level secondary action (36px), as used by the workflows header.
export const ghostBtn: CSSProperties = {
  ...labelBase,
  minHeight: 36,
  padding: "0 14px",
  fontSize: 13,
  fontWeight: 500,
  background: "var(--bg-elev-2)",
  border: "1px solid var(--border-strong)",
  color: "var(--fg)",
};

// The one primary action on a page.
export const primaryBtn: CSSProperties = {
  ...labelBase,
  minHeight: 36,
  padding: "0 14px",
  fontSize: 13,
  fontWeight: 600,
  background: "var(--accent)",
  border: "1px solid var(--accent)",
  color: "var(--accent-fg)",
};

// Primary action in a dense toolbar -- the studio topbar's Run button.
export const primaryBtnSm: CSSProperties = {
  ...labelBase,
  minHeight: 28,
  padding: "0 12px",
  fontSize: 12,
  fontWeight: 600,
  background: "var(--accent)",
  border: "1px solid var(--accent)",
  color: "var(--accent-fg)",
  gap: 4,
};

// Full-width button on the auth screens: taller, and centred because it spans
// its column rather than sitting in a row of siblings.
export const authBtn: CSSProperties = {
  ...labelBase,
  minHeight: 40,
  display: "flex",
  justifyContent: "center",
  gap: 8,
  background: "var(--bg-elev-1)",
  border: "1px solid var(--border)",
  color: "var(--fg)",
  fontSize: 13,
  fontWeight: 500,
};

// Action at the end of a table row (purchase history).
export const rowBtn: CSSProperties = {
  ...ghostBtnSm,
  padding: "0 12px",
};

// ── Icon-only buttons ────────────────────────────────────────────────────
// A fixed square IS the design here, so `height` stays: there is no label to
// wrap, and the box must stay square to sit in a stack of identical controls.
// `flexShrink: 0` still applies -- a squeezed icon button is just as wrong.
const iconBase: CSSProperties = {
  width: 28,
  height: 28,
  flexShrink: 0,
  alignItems: "center",
  justifyContent: "center",
  background: "transparent",
  cursor: "pointer",
};

// Canvas zoom / fit controls.
export const ctrlBtn: CSSProperties = {
  ...iconBase,
  display: "flex",
  border: "none",
  color: "var(--fg-muted)",
  fontFamily: "var(--font-mono)",
  fontSize: 16,
  borderRadius: "var(--r-1)",
};

// Bordered icon button in the inspector.
export const iconBtn: CSSProperties = {
  ...iconBase,
  display: "inline-flex",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-2)",
  color: "var(--fg-muted)",
  fontSize: 12,
  fontFamily: "var(--font-mono)",
};

// The two families, for the test to iterate. Keeping them separate is what
// lets the test hold label buttons to `minHeight` while allowing icon buttons
// their deliberate fixed square.
export const LABEL_BUTTONS = {
  ghostBtnSm,
  ghostBtn,
  primaryBtn,
  primaryBtnSm,
  authBtn,
  rowBtn,
};

export const ICON_BUTTONS = { ctrlBtn, iconBtn };
