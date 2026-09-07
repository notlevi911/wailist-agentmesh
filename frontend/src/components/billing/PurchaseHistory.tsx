"use client";
import { useEffect, useState } from "react";
import { Card, Pill } from "@/components/ui";
import { rowBtn } from "@/components/ui/buttons";
import { useCredits } from "@/lib/credits/store";
import { Receipt } from "./Receipt";
import type { Purchase, PurchaseStatus } from "@/lib/credits/types";
import type { PaymentMethod } from "@/components/checkout/types";

const METHOD_LABELS: Record<PaymentMethod, string> = {
  cashfree: "Cashfree",
  nowpayments: "NOWPayments",
  paypal: "PayPal",
  stripe: "Stripe",
};

const dateFmt = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
});

// STATUS_PILLS maps credit_ledger.status to what the row shows. Only
// 'completed' actually granted credits; the others are surfaced rather than
// filtered out so a user whose payment did not land can see which stage it
// stopped at instead of an empty page.
const STATUS_PILLS: Record<
  PurchaseStatus,
  { label: string; tone: "ok" | "warm" | "danger" }
> = {
  completed: { label: "Paid", tone: "ok" },
  pending: { label: "Pending", tone: "warm" },
  partial: { label: "Partial", tone: "warm" },
  refunded: { label: "Refunded", tone: "warm" },
  failed: { label: "Failed", tone: "danger" },
  expired: { label: "Expired", tone: "danger" },
};

// Billing history from credit_ledger via GET /credits/purchases. Newest first.
export function PurchaseHistory({
  onBuyAgain,
}: {
  onBuyAgain: (amountINR: number) => void;
}) {
  const { purchases, purchasesKnown, purchasesFailed, refreshPurchases } =
    useCredits();
  // A repeat failure leaves purchasesFailed already true, so the store's state
  // does not change and the UI would be pixel-identical to before the click.
  // Tracking the attempt locally is what makes the retry observable.
  const [retrying, setRetrying] = useState(false);
  const [receipt, setReceipt] = useState<Purchase | null>(null);

  useEffect(() => {
    void refreshPurchases();
  }, [refreshPurchases]);

  // Nothing while the first fetch is still in flight: an empty list before the
  // server answers is "not asked yet", and rendering "No purchases yet" for it
  // tells a paying user their receipts are gone. A FAILED fetch is different --
  // it falls through so the section can say so and offer a retry, rather than
  // disappearing and looking like an account that never paid.
  if (!purchasesKnown && !purchasesFailed) return null;

  return (
    <>
      <div style={{ marginTop: 32 }}>
        <h2
          style={{
            fontSize: 15,
            fontWeight: 600,
            color: "var(--fg)",
            marginBottom: 12,
          }}
        >
          Billing history
        </h2>

        {purchasesFailed && purchases.length === 0 ? (
          <p style={{ fontSize: 13, color: "var(--fg-dim)", margin: 0 }}>
            Could not load your billing history.{" "}
            <button
              type="button"
              disabled={retrying}
              onClick={() => {
                setRetrying(true);
                void refreshPurchases().finally(() => setRetrying(false));
              }}
              style={{
                ...rowBtn,
                // minHeight, not height: rowBtn inherits minHeight from
                // ghostBtnSm, so overriding `height` does nothing and the
                // link renders as a 28px box stretching this line.
                minHeight: 0,
                padding: 0,
                border: "none",
                background: "none",
                color: "var(--accent)",
                textDecoration: "underline",
                cursor: retrying ? "default" : "pointer",
                opacity: retrying ? 0.6 : 1,
              }}
            >
              {retrying ? "Retrying…" : "Retry"}
            </button>
          </p>
        ) : purchases.length === 0 ? (
          <p style={{ fontSize: 13, color: "var(--fg-dim)", margin: 0 }}>
            No purchases yet.
          </p>
        ) : (
          <Card style={{ padding: 0, overflow: "hidden" }}>
            {purchases.map((p, i) => (
              <div
                key={p.id}
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 8,
                  padding: "12px 16px",
                  borderTop: i === 0 ? "none" : "1px solid var(--border-soft)",
                }}
              >
                {/* Amount + credits granted + status */}
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 8,
                  }}
                >
                  <span
                    style={{
                      fontSize: 13,
                      fontWeight: 600,
                      color: "var(--fg)",
                      fontFamily: "var(--font-mono)",
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {p.amountINR !== undefined
                      ? `\u20B9${p.amountINR.toFixed(2)}`
                      : `$${(p.amountUSD ?? 0).toFixed(2)}`}
                  </span>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      flexShrink: 0,
                    }}
                  >
                    {/* credit_usd_micros is written when the row is CREATED,
                        not when it settles, so it is what the top-up would
                        have granted -- not what landed. Showing "+$6.00" in
                        accent green beside a red "Failed" pill reads as money
                        received; for a refund the balance was actively
                        reversed. Only a completed row asserts a grant. */}
                    {p.status === "completed" ? (
                      <span
                        style={{
                          fontSize: 13,
                          fontFamily: "var(--font-mono)",
                          color: "var(--accent)",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        +${p.creditsUSD.toFixed(2)}
                      </span>
                    ) : (
                      <span
                        style={{
                          fontSize: 13,
                          fontFamily: "var(--font-mono)",
                          color: "var(--fg-dim)",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        ${p.creditsUSD.toFixed(2)}
                      </span>
                    )}
                    <Pill tone={STATUS_PILLS[p.status].tone}>
                      {STATUS_PILLS[p.status].label}
                    </Pill>
                  </div>
                </div>

                {/* Method · date */}
                <div
                  style={{
                    fontSize: 11,
                    color: "var(--fg-dim)",
                    fontFamily: "var(--font-mono)",
                  }}
                >
                  {METHOD_LABELS[p.method]} ·{" "}
                  {dateFmt.format(new Date(p.createdAt))}
                </div>

                {/* Actions */}
                <div style={{ display: "flex", gap: 8 }}>
                  {/* A receipt is a GST-shaped payment document. Offering one
                      for a failed, expired or pending row would let a user
                      print an invoice for money that never moved. */}
                  {p.status === "completed" && (
                    <button
                      type="button"
                      onClick={() => setReceipt(p)}
                      style={rowBtn}
                    >
                      Receipt
                    </button>
                  )}
                  {/* Only an INR row can be repeated -- the checkout is
                      driven by an INR amount, which a crypto top-up has
                      none of. */}
                  {p.amountINR !== undefined && (
                    <button
                      type="button"
                      onClick={() => onBuyAgain(p.amountINR!)}
                      style={rowBtn}
                    >
                      Buy again
                    </button>
                  )}
                </div>
              </div>
            ))}
          </Card>
        )}
      </div>
      {receipt && (
        <Receipt purchase={receipt} onClose={() => setReceipt(null)} />
      )}
    </>
  );
}
