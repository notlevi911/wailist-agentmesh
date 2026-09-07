"use client";
import type { CartItem } from "./types";
import { IconWallet } from "@/components/ui";

// A single, read-only credit line. A top-up is one indivisible amount -- there is
// no quantity to change or item to remove -- so this just presents the bundle and
// its price. Styling comes entirely from the app's dark design tokens.
export function CartItemRow({ item }: { item: CartItem }) {
  const lineTotal = item.unitPrice * item.quantity;

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "44px 1fr auto",
        alignItems: "center",
        gap: 12,
        padding: "16px 0",
        borderTop: "1px solid var(--border-soft)",
      }}
    >
      <div
        aria-hidden
        style={{
          width: 44,
          height: 44,
          borderRadius: "var(--r-2)",
          background:
            "linear-gradient(135deg, var(--bg-elev-3), var(--bg-elev-2))",
          border: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--accent)",
        }}
      >
        <IconWallet size={20} />
      </div>

      <div style={{ minWidth: 0 }}>
        <div
          style={{
            fontSize: 13,
            fontWeight: 500,
            color: "var(--fg)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {item.title}
        </div>
        <div style={{ fontSize: 12, color: "var(--fg-dim)", marginTop: 2 }}>
          {item.detail}
        </div>
      </div>

      <div
        style={{
          fontSize: 14,
          fontWeight: 600,
          color: "var(--fg)",
          fontFamily: "var(--font-mono)",
          minWidth: 72,
          textAlign: "right",
        }}
      >
        ₹{lineTotal.toFixed(2)}
      </div>
    </div>
  );
}
