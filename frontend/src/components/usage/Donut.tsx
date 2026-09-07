"use client";

export interface DonutSegment {
  label: string;
  value: number;
  color: string;
}

// Minimal SVG donut. Segments render as dash-array arcs on stacked circles,
// rotated so 0 starts at 12 o'clock. Legend is rendered by the parent.
export function Donut({
  segments,
  size = 168,
  thickness = 22,
  centerLabel,
  centerSub,
}: {
  segments: DonutSegment[];
  size?: number;
  thickness?: number;
  centerLabel?: string;
  centerSub?: string;
}) {
  const total = segments.reduce((s, x) => s + x.value, 0) || 1;
  const r = (size - thickness) / 2;
  const c = 2 * Math.PI * r;
  const cx = size / 2;
  // Cumulative start fraction per segment, precomputed -- mutating a shared
  // accumulator from inside the map callback reassigns render-scope state.
  const starts: number[] = [];
  for (let a = 0, i = 0; i < segments.length; i++) {
    starts.push(a);
    a += segments[i].value / total;
  }

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      style={{ display: "block" }}
    >
      <g transform={`rotate(-90 ${cx} ${cx})`}>
        <circle
          cx={cx}
          cy={cx}
          r={r}
          fill="none"
          stroke="var(--border-soft)"
          strokeWidth={thickness}
        />
        {segments.map((s, i) => {
          const dash = (s.value / total) * c;
          return (
            <circle
              key={i}
              cx={cx}
              cy={cx}
              r={r}
              fill="none"
              stroke={s.color}
              strokeWidth={thickness}
              strokeDasharray={`${dash} ${c - dash}`}
              strokeDashoffset={-starts[i] * c}
            />
          );
        })}
      </g>
      {centerLabel && (
        <text
          x={cx}
          y={cx - 2}
          textAnchor="middle"
          fontFamily="var(--font-sans)"
          fontSize="20"
          fontWeight="500"
          fill="var(--fg)"
        >
          {centerLabel}
        </text>
      )}
      {centerSub && (
        <text
          x={cx}
          y={cx + 14}
          textAnchor="middle"
          fontFamily="var(--font-mono)"
          fontSize="9"
          fill="var(--fg-dim)"
          letterSpacing="0.06em"
        >
          {centerSub}
        </text>
      )}
    </svg>
  );
}
