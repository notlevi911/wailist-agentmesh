"use client";
import { useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { IconClose } from "@/components/ui";
import { useModalDismissal } from "@/hooks/useModalDismissal";
import { useCredits } from "@/lib/credits/store";
import { creditsForTopup } from "@/lib/credits/fx";
import type { PaymentMethod } from "./types";
import { DEFAULT_PROVIDER } from "./paymentProviders";
import { buildCreditCart, computeTotals } from "./mockData";
import { CartItemRow } from "./CartItemRow";
import { OrderSummary } from "./OrderSummary";
import { PaymentInfoPanel } from "./PaymentInfoPanel";

// We intentionally avoid native <dialog showModal()> here because showModal()
// places the element in the browser top layer, which sits above every z-index
// value including Cashfree's payment overlay. A CSS-based modal lets the
// Cashfree SDK render its QR / hosted checkout above our modal without any
// stacking conflict.
const MODAL_CSS = `
.checkout-split { display: grid; grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr); gap: 20px; }
.checkout-check { animation: checkout-pulse 2.4s var(--ease) infinite; }
.checkout-backdrop {
  position: fixed; inset: 0; z-index: 1000;
  background: rgba(8,7,12,0.72); backdrop-filter: blur(4px);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
  animation: checkout-backdrop-in 0.18s var(--ease);
}
.checkout-panel {
  position: relative;
  display: flex; flex-direction: column;
  max-height: 90vh; max-width: min(980px, calc(100vw - 48px));
  width: 100%;
  border: 1px solid var(--border-strong);
  border-radius: var(--r-4);
  background: var(--bg-elev-1);
  color: var(--fg);
  box-shadow: 0 24px 64px rgba(0,0,0,0.55);
  animation: checkout-in 0.24s var(--ease);
}
@keyframes checkout-backdrop-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
@keyframes checkout-in {
  from { opacity: 0; transform: translateY(8px) scale(0.985); }
  to { opacity: 1; transform: none; }
}
@keyframes checkout-pulse {
  0%, 100% { box-shadow: 0 0 26px 2px var(--accent-glow); }
  50% { box-shadow: 0 0 10px 0 var(--accent-glow); }
}
@media (max-width: 860px) {
  .checkout-split { grid-template-columns: minmax(0, 1fr); }
}
@media (prefers-reduced-motion: reduce) {
  .checkout-backdrop, .checkout-panel, .checkout-check { animation: none; }
}
`;

export function CheckoutModal({
  open,
  amountINR,
  onClose,
}: {
  open: boolean;
  amountINR: number;
  onClose: () => void;
}) {
  const panelRef = useRef<HTMLDivElement>(null);
  const items = useMemo(() => buildCreditCart(amountINR), [amountINR]);
  const [method, setMethod] = useState<PaymentMethod>(DEFAULT_PROVIDER);
  const { recordPurchase, balanceUSD } = useCredits();
  const router = useRouter();
  // Just the credited amount for the success screen, not a purchase record:
  // credit_ledger is where the purchase lives, written by the backend when the
  // payment was created and settled by the gateway's webhook. Keeping a local
  // record here is what used to make history per-browser.
  const [creditedUSD, setCreditedUSD] = useState<number | null>(null);

  const totals = useMemo(() => computeTotals(items), [items]);

  const handlePaid = (creditsUSDOverride?: number) => {
    // creditsUSDOverride is the backend-verified credited amount. A provider
    // that cannot report one yet (the NOWPayments stub) falls back to the FX
    // estimate for this screen only -- the balance shown underneath comes from
    // the server either way, so an estimate here can never become a number the
    // user is billed against.
    setCreditedUSD(creditsUSDOverride ?? creditsForTopup(totals.total));
    void recordPurchase();
  };

  useModalDismissal(onClose, open);

  if (!open) return null;

  return (
    <>
      <style>{MODAL_CSS}</style>
      {/* Backdrop -- click outside the panel to close */}
      <div
        className="checkout-backdrop"
        role="presentation"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <div
          ref={panelRef}
          role="dialog"
          aria-modal="true"
          aria-label="Checkout"
          className="checkout-panel"
        >
          <div
            style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: 24 }}
          >
            {creditedUSD !== null ? (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "center",
                  textAlign: "center",
                  gap: 12,
                  padding: "40px 16px",
                }}
              >
                <div
                  className="checkout-check"
                  style={{
                    width: 52,
                    height: 52,
                    borderRadius: 999,
                    background: "var(--accent-soft)",
                    border: "1px solid var(--accent-line)",
                    color: "var(--accent)",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                    fontSize: 24,
                  }}
                >
                  ✓
                </div>
                <h2
                  style={{
                    fontSize: 18,
                    fontWeight: 700,
                    color: "var(--fg)",
                    margin: 0,
                  }}
                >
                  Payment successful
                </h2>
                <p
                  style={{ fontSize: 13, color: "var(--fg-muted)", margin: 0 }}
                >
                  ${creditedUSD.toFixed(2)} credits added to your
                  wallet.
                </p>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 13,
                    color: "var(--fg)",
                    background: "var(--bg-elev-2)",
                    border: "1px solid var(--border)",
                    borderRadius: "var(--r-2)",
                    padding: "8px 14px",
                  }}
                >
                  New balance: ${balanceUSD.toFixed(2)}
                </div>
                <div style={{ display: "flex", gap: 10, marginTop: 8 }}>
                  <button
                    type="button"
                    onClick={() => router.push("/usage")}
                    style={{
                      height: 38,
                      padding: "0 18px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--accent-line)",
                      background: "var(--accent)",
                      color: "var(--accent-fg)",
                      fontSize: 13,
                      fontWeight: 600,
                      cursor: "pointer",
                    }}
                  >
                    Go to Usage
                  </button>
                  <button
                    type="button"
                    onClick={onClose}
                    style={{
                      height: 38,
                      padding: "0 18px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--border-strong)",
                      background: "transparent",
                      color: "var(--fg-muted)",
                      fontSize: 13,
                      fontWeight: 500,
                      cursor: "pointer",
                    }}
                  >
                    Close
                  </button>
                </div>
              </div>
            ) : (
              <>
                {/* Header */}
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    marginBottom: 20,
                  }}
                >
                  <div>
                    <h2
                      style={{
                        fontSize: 19,
                        fontWeight: 700,
                        color: "var(--fg)",
                        margin: 0,
                        letterSpacing: "-0.01em",
                      }}
                    >
                      Checkout
                    </h2>
                    <p
                      style={{
                        margin: "3px 0 0",
                        fontSize: 12.5,
                        color: "var(--fg-muted)",
                      }}
                    >
                      Complete your credit top-up
                    </p>
                  </div>
                  <button
                    type="button"
                    aria-label="Close checkout"
                    onClick={onClose}
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      width: 32,
                      height: 32,
                      background: "transparent",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-2)",
                      color: "var(--fg-muted)",
                      cursor: "pointer",
                    }}
                  >
                    <IconClose size={14} />
                  </button>
                </div>

                <div className="checkout-split">
                  {/* Cart card */}
                  <div
                    style={{
                      background: "var(--bg-elev-2)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-3)",
                      padding: 20,
                    }}
                  >
                    <div
                      style={{
                        paddingBottom: 4,
                        fontSize: 12,
                        fontWeight: 600,
                        color: "var(--fg-muted)",
                      }}
                    >
                      Order summary
                    </div>
                    {items.map((item) => (
                      <CartItemRow key={item.id} item={item} />
                    ))}
                    <div style={{ marginTop: 8 }}>
                      <OrderSummary totals={totals} />
                    </div>
                  </div>

                  {/* Payment card */}
                  <div
                    style={{
                      background: "var(--bg-elev-2)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-3)",
                      padding: 20,
                      display: "flex",
                    }}
                  >
                    <div
                      style={{
                        width: "100%",
                        display: "flex",
                        flexDirection: "column",
                      }}
                    >
                      <PaymentInfoPanel
                        method={method}
                        onMethodChange={setMethod}
                        amountINR={totals.total}
                        payable={totals.total > 0}
                        onPaid={handlePaid}
                      />
                    </div>
                  </div>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
