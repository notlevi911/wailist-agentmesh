"use client";
import type { OrderTotals } from "./types";
import { bonusUSD, creditsForTopup, gstBreakdown } from "@/lib/credits/fx";

// Money summary for the credits checkout -- a totals block. Coupons are
// intentionally omitted (not offered yet) and there is no shipping, since
// credits are digital: you pay the subtotal. The credits you receive are
// highlighted as the reward of the purchase.
export function OrderSummary({ totals }: { totals: OrderTotals }) {
  const credits = creditsForTopup(totals.total);
  const bonus = bonusUSD(totals.total);
  const { base, gst } = gstBreakdown(totals.total);
  return (
    <div
      style={{
        paddingTop: 20,
        borderTop: "1px solid var(--border)",
        display: "flex",
        flexDirection: "column",
        gap: 12,
      }}
    >
      <SummaryRow label="Subtotal (excl. GST)" value={base} />
      <SummaryRow label="GST (18%)" value={gst} />

      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          paddingTop: 12,
          borderTop: "1px solid var(--border)",
        }}
      >
        <span style={{ fontSize: 14, fontWeight: 600, color: "var(--fg)" }}>
          Total payable
        </span>
        <span
          style={{
            fontSize: 20,
            fontWeight: 700,
            color: "var(--fg)",
            fontFamily: "var(--font-mono)",
            fontVariantNumeric: "tabular-nums",
            letterSpacing: "-0.01em",
          }}
        >
          ₹{totals.total.toFixed(2)}
        </span>
      </div>

      {/* Reward -- the credits this purchase grants. */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          padding: "10px 12px",
          borderRadius: "var(--r-2)",
          background: "var(--accent-soft)",
          border: "1px solid var(--accent-line)",
        }}
      >
        <span style={{ fontSize: 12, fontWeight: 500, color: "var(--accent)" }}>
          You receive
        </span>
        <span
          style={{
            fontSize: 15,
            fontWeight: 700,
            fontFamily: "var(--font-mono)",
            fontVariantNumeric: "tabular-nums",
            color: "var(--accent)",
          }}
        >
          ≈ ${credits.toFixed(2)} credits
        </span>
      </div>
      {bonus > 0 && (
        <div
          style={{
            fontSize: 11,
            color: "var(--fg-dim)",
            textAlign: "right",
            marginTop: -4,
          }}
        >
          includes ${bonus.toFixed(2)} bonus
        </div>
      )}
    </div>
  );
}

function SummaryRow({ label, value }: { label: string; value: number }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between" }}>
      <span style={{ fontSize: 13, color: "var(--fg-muted)" }}>{label}</span>
      <span
        style={{
          fontSize: 13,
          color: "var(--fg)",
          fontFamily: "var(--font-mono)",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        ₹{value.toFixed(2)}
      </span>
    </div>
  );
}
