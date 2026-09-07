"use client";
import { useEffect, useRef, useState } from "react";

// A thin vertical drag bar that sits between the canvas and a side panel.
// Uses Pointer Events with pointer capture so the drag keeps tracking even when
// the cursor leaves the window and releases cleanly on pointerup/cancel -- no
// window-level listeners to leak. The parent owns the width state and does the
// clamping (it knows the container width); this component only reports the
// requested value.
export function ResizeHandle({
  value,
  min,
  max,
  side,
  ariaLabel,
  onChange,
  onCommit,
  onReset,
}: {
  value: number;
  min: number;
  max: number;
  // Which panel this handle resizes, relative to the canvas in the middle.
  // "left" = palette (drag right → wider); "right" = inspector (drag left → wider).
  side: "left" | "right";
  ariaLabel: string;
  onChange: (requested: number) => void;
  onCommit?: () => void;
  onReset?: () => void;
}) {
  const drag = useRef<{ startX: number; startVal: number } | null>(null);
  const [active, setActive] = useState(false);
  const [hover, setHover] = useState(false);

  // If this handle unmounts mid-drag (e.g. a route change fires while the
  // pointer is still down), pointerup/pointercancel never reach `end()`, so
  // without this the body is left stuck with cursor: col-resize and no text
  // selection for the rest of the session.
  useEffect(
    () => () => {
      if (!drag.current) return;
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    },
    [],
  );

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    // Stop the event reaching CanvasGraph's background pan handler.
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { startX: e.clientX, startVal: value };
    setActive(true);
    document.body.style.userSelect = "none";
    document.body.style.cursor = "col-resize";
  };

  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    const dx = e.clientX - drag.current.startX;
    const delta = side === "left" ? dx : -dx;
    onChange(drag.current.startVal + delta);
  };

  const end = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    drag.current = null;
    setActive(false);
    document.body.style.userSelect = "";
    document.body.style.cursor = "";
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      /* pointer already released */
    }
    onCommit?.();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? 48 : 16;
    if (e.key === "ArrowLeft") {
      onChange(value + (side === "left" ? -step : step));
    } else if (e.key === "ArrowRight") {
      onChange(value + (side === "left" ? step : -step));
    } else {
      return;
    }
    e.preventDefault();
    onCommit?.();
  };

  const lit = active || hover;

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={ariaLabel}
      aria-valuenow={Math.round(value)}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={end}
      onPointerCancel={end}
      onDoubleClick={onReset}
      onKeyDown={onKeyDown}
      onPointerEnter={() => setHover(true)}
      onPointerLeave={() => setHover(false)}
      style={{
        flexShrink: 0,
        width: 6,
        alignSelf: "stretch",
        cursor: "col-resize",
        background: "transparent",
        position: "relative",
        zIndex: 5,
        touchAction: "none",
      }}
    >
      {/* 1px hairline centred in the 6px hit area; brightens on hover/drag */}
      <span
        aria-hidden="true"
        style={{
          position: "absolute",
          top: 0,
          bottom: 0,
          left: 2,
          width: lit ? 2 : 1,
          background: lit ? "var(--accent-line)" : "var(--border)",
          transition: "background 120ms var(--ease), width 120ms var(--ease)",
        }}
      />
    </div>
  );
}
