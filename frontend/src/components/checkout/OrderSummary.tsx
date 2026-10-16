"use client";
import type { OrderTotals } from "./types";
import { creditsForTopup, gstBreakdown } from "@/lib/credits/fx";

// Money summary for the credits checkout -- a totals block. Coupons are
// intentionally omitted (not offered yet) and there is no shipping, since
// credits are digital: you pay the subtotal. The credits you receive are
// highlighted as the reward of the purchase.
export function OrderSummary({
  totals,
  usdPerINR,
}: {
  totals: OrderTotals;
  /** The server's live rate. 0 until it arrives; see the comment below. */
  usdPerINR: number;
}) {
  // Only quote once the server has told us the rate it will charge at.
  // This used to convert at a hardcoded 1/83 and add a 5% bonus the
  // backend never granted, so it promised roughly 21% more credit than
  // the ledger recorded -- on the screen where the payer decides.
  const credits =
    usdPerINR > 0 ? creditsForTopup(totals.total, usdPerINR) : null;
  const { base, gst } = gstBreakdown(totals.total);
  return (
    <div
      style={{
        paddingTop: 20,
        borderTop: "1px solid var(--border)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--s-4)",
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
        <span style={{ fontSize: "var(--t-4)", fontWeight: 600, color: "var(--fg)" }}>
          Total payable
        </span>
        <span
          style={{
            fontSize: "var(--t-5)",
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
        <span style={{ fontSize: "var(--t-2)", fontWeight: 500, color: "var(--accent)" }}>
          You receive
        </span>
        <span
          style={{
            fontSize: "var(--t-4)",
            fontWeight: 700,
            fontFamily: "var(--font-mono)",
            fontVariantNumeric: "tabular-nums",
            color: "var(--accent)",
          }}
        >
          {credits === null ? "≈ -" : `≈ $${credits.toFixed(2)} credits`}
        </span>
      </div>
    </div>
  );
}

function SummaryRow({ label, value }: { label: string; value: number }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between" }}>
      <span style={{ fontSize: "var(--t-3)", color: "var(--fg-muted)" }}>{label}</span>
      <span
        style={{
          fontSize: "var(--t-3)",
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
