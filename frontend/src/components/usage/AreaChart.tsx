"use client";
import { useRef, useState } from "react";
import { UsagePoint } from "@/lib/types";

// Hand-rolled dual-line chart (no chart lib -- matches the codebase's SVG-by-hand
// convention). Two series on two independent scales so both read clearly:
// Spend = accent purple (shown as USD in the tooltip), Usage = warm amber
// (x402 calls). Responsive via viewBox + preserveAspectRatio="none"; strokes stay
// crisp with vectorEffect="non-scaling-stroke".

const W = 1000;
const padL = 10,
  padR = 10,
  padT = 14,
  padB = 4;

export function AreaChart({
  data,
  height = 210,
  algoUsd = 1,
}: {
  data: UsagePoint[];
  height?: number;
  algoUsd?: number;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const n = data.length;

  // New data invalidates the stored index: without this, switching the range
  // leaves a valid-but-stale hover active, and the guideline/tooltip sit at a
  // position that no longer matches the cursor until the next mousemove.
  // Reset during render (React's "adjust state when props change" pattern)
  // rather than in an effect, so the stale frame never paints.
  const [prevData, setPrevData] = useState(data);
  if (prevData !== data) {
    setPrevData(data);
    setHover(null);
  }
  const H = height;
  const innerW = W - padL - padR;
  const innerH = H - padT - padB;

  if (n === 0) return null;

  // Hover is a stored index; when the range switches to a shorter series the old
  // index can point past the new array. Clamp to a valid slot (else no hover) so
  // data[active] never reads undefined and crashes the chart.
  const active = hover != null && hover < n ? hover : null;

  // Spend (ALGO) and usage (calls) differ by orders of magnitude, so each series
  // maps to its own vertical scale -- both lines fill the height and stay legible.
  const maxSpend = Math.max(1e-9, ...data.map((d) => d.x402Algo + d.llmAlgo));
  const maxCalls = Math.max(1e-9, ...data.map((d) => d.calls));
  const x = (i: number) =>
    padL + (n <= 1 ? innerW / 2 : (i / (n - 1)) * innerW);
  const yS = (v: number) => padT + (1 - v / maxSpend) * innerH; // spend → purple
  const yU = (v: number) => padT + (1 - v / maxCalls) * innerH; // usage → amber
  const base = padT + innerH;

  const spendTop = data.map((d, i) => `${x(i)},${yS(d.x402Algo + d.llmAlgo)}`);
  const usageTop = data.map((d, i) => `${x(i)},${yU(d.calls)}`);
  const areaSpend = `M ${padL},${base} L ${spendTop.join(" L ")} L ${x(n - 1)},${base} Z`;
  const lineSpend = `M ${spendTop.join(" L ")}`;
  const lineUsage = `M ${usageTop.join(" L ")}`;

  const gridY = [0, 0.25, 0.5, 0.75, 1].map((f) => padT + f * innerH);
  const labelEvery = Math.max(1, Math.ceil(n / 6));

  const onMove = (e: React.MouseEvent) => {
    const el = wrapRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const frac = (e.clientX - rect.left) / rect.width;
    setHover(Math.max(0, Math.min(n - 1, Math.round(frac * (n - 1)))));
  };

  return (
    <div
      ref={wrapRef}
      style={{ position: "relative" }}
      onMouseMove={onMove}
      onMouseLeave={() => setHover(null)}
    >
      <svg
        width="100%"
        height={H}
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        style={{ display: "block" }}
      >
        <defs>
          <linearGradient id="am-gx402" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.42" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0.03" />
          </linearGradient>
        </defs>
        {gridY.map((gy, i) => (
          <line
            key={i}
            x1={padL}
            y1={gy}
            x2={W - padR}
            y2={gy}
            stroke="var(--border-soft)"
            strokeWidth="1"
            vectorEffect="non-scaling-stroke"
          />
        ))}
        {/* spend (purple, left scale) with soft fill; usage (amber, right scale) as a line */}
        <path d={areaSpend} fill="url(#am-gx402)" />
        <path
          d={lineSpend}
          fill="none"
          stroke="var(--accent)"
          strokeWidth="1.8"
          vectorEffect="non-scaling-stroke"
        />
        <path
          d={lineUsage}
          fill="none"
          stroke="var(--warm)"
          strokeWidth="1.8"
          vectorEffect="non-scaling-stroke"
        />
        {active != null && (
          <>
            <line
              x1={x(active)}
              y1={padT}
              x2={x(active)}
              y2={base}
              stroke="var(--border-strong)"
              strokeWidth="1"
              vectorEffect="non-scaling-stroke"
            />
            <circle
              cx={x(active)}
              cy={yS(data[active].x402Algo + data[active].llmAlgo)}
              r="3.5"
              fill="var(--accent)"
              vectorEffect="non-scaling-stroke"
            />
            <circle
              cx={x(active)}
              cy={yU(data[active].calls)}
              r="3.5"
              fill="var(--warm)"
              vectorEffect="non-scaling-stroke"
            />
          </>
        )}
      </svg>

      {/* x-axis labels, positioned at their data points */}
      <div style={{ position: "relative", height: 14, marginTop: 4 }}>
        {data.map((d, i) =>
          i % labelEvery === 0 || i === n - 1 ? (
            <span
              key={i}
              style={{
                position: "absolute",
                left: `${(x(i) / W) * 100}%`,
                transform: "translateX(-50%)",
                fontFamily: "var(--font-mono)",
                fontSize: 9,
                color: "var(--fg-dim)",
                whiteSpace: "nowrap",
              }}
            >
              {d.ts}
            </span>
          ) : null,
        )}
      </div>

      {active != null && (
        <ChartTip
          data={data[active]}
          leftPct={(x(active) / W) * 100}
          algoUsd={algoUsd}
        />
      )}
    </div>
  );
}

function ChartTip({
  data,
  leftPct,
  algoUsd,
}: {
  data: UsagePoint;
  leftPct: number;
  algoUsd: number;
}) {
  const flip = leftPct > 62;
  const spendUsd = (data.x402Algo + data.llmAlgo) * algoUsd;
  return (
    <div
      style={{
        position: "absolute",
        top: 6,
        left: `${leftPct}%`,
        transform: flip
          ? "translateX(-100%) translateX(-10px)"
          : "translateX(10px)",
        pointerEvents: "none",
        zIndex: 3,
        background: "var(--bg-elev-3)",
        border: "1px solid var(--border-strong)",
        borderRadius: "var(--r-2)",
        padding: "8px 10px",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        color: "var(--fg)",
        boxShadow: "0 8px 24px rgba(0,0,0,0.5)",
        whiteSpace: "nowrap",
      }}
    >
      <div style={{ color: "var(--fg-dim)", marginBottom: 5 }}>{data.ts}</div>
      <TipRow
        c="var(--accent)"
        label="Spend"
        val={`$${spendUsd.toFixed(2)} USD`}
      />
      <TipRow
        c="var(--warm)"
        label="Usage"
        val={`${data.calls.toLocaleString()} calls`}
      />
    </div>
  );
}

function TipRow({ c, label, val }: { c: string; label: string; val: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 7,
        marginTop: 2,
        color: "var(--fg-muted)",
      }}
    >
      <span
        style={{
          width: 7,
          height: 7,
          borderRadius: 999,
          background: c,
          display: "inline-block",
        }}
      />
      {label}
      <span style={{ marginLeft: 16, color: "var(--fg)" }}>{val}</span>
    </div>
  );
}
