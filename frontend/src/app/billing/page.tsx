"use client";
import { useEffect, useState } from "react";
import { IconArrow, IconWallet } from "@/components/ui";
import { Topbar } from "@/components/Topbar";
import { PurchaseHistory } from "@/components/billing/PurchaseHistory";
import { CheckoutModal } from "@/components/checkout/CheckoutModal";
import { useCredits } from "@/lib/credits/store";
import {
  bonusRate,
  creditsForTopup,
  maxTopupINR,
  MAX_TOPUP_USD,
} from "@/lib/credits/fx";
import { credits as creditsApi } from "@/lib/api";

const PRESETS_INR = [1000, 5000, 10000, 20000];
const MAX_INR = maxTopupINR();
const LOW_BALANCE_USD = 5;

const HOW_IT_WORKS = [
  "Credits are spent as your agents call paid tools, x402 endpoints, and LLM providers.",
  "Testnet usage is always free. You only pay for mainnet calls.",
  "Top-ups of ₹1000 or more earn 5% bonus credits.",
  "Every purchase generates a printable receipt for your records.",
];

const BILLING_CSS = `
.bill-reveal { animation: fade-up 0.45s var(--ease) both; }
.bill-preset { transition: transform 0.15s var(--ease), border-color 0.15s var(--ease), background 0.15s var(--ease); }
.bill-preset:hover { transform: translateY(-2px); border-color: var(--border-strong); }
.bill-cta { transition: transform 0.12s var(--ease), box-shadow 0.2s var(--ease); }
.bill-cta:not(:disabled):hover { box-shadow: 0 12px 34px var(--accent-glow); }
.bill-cta:not(:disabled):active { transform: scale(0.99); }
.bill-grid { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: 20px; align-items: start; }
@media (max-width: 900px) { .bill-grid { grid-template-columns: minmax(0, 1fr); } }
@media (prefers-reduced-motion: reduce) {
  .bill-reveal, .bill-preset, .bill-cta { animation: none; transition: none; }
}
`;

const panelStyle: React.CSSProperties = {
  background: "var(--bg-elev-1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--r-3)",
  padding: 20,
};

const fmtUSD = (n: number) => `$${n.toFixed(2)}`;

export default function BillingPage() {
  const { balanceUSD, balanceKnown, lastPurchase, refreshBalance } =
    useCredits();
  const [amountINR, setAmountINR] = useState<number>(PRESETS_INR[1]);
  const [customINR, setCustomINR] = useState("");
  const [checkoutOpen, setCheckoutOpen] = useState(false);

  // Read the authoritative balance (users.credit_balance_usd_micros) every time
  // this page is opened. The store keeps a cross-route copy in memory, but it
  // goes stale the moment a run spends credits in another tab — and this is the
  // page where the number has to be right.
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  // A crypto top-up sends the browser to NOWPayments and back. This closes out
  // that round trip. Nothing is credited here -- the IPN webhook is the only
  // path that grants credit -- so the message says the balance will follow
  // rather than claiming success.
  //
  // Written as one asynchronous routine so the effect never sets state during
  // its own render pass.
  const [returnState, setReturnState] = useState<{
    tone: "pending" | "error";
    message: string;
  } | null>(null);

  useEffect(() => {
    void (async () => {
      const params = new URLSearchParams(window.location.search);
      const outcome = params.get("crypto");
      if (!outcome) return;

      // Strip the param so a refresh doesn't re-run this.
      const url = new URL(window.location.href);
      url.searchParams.delete("crypto");
      window.history.replaceState({}, "", url.toString());

      if (outcome === "cancelled" || outcome === "canceled") {
        setReturnState({ tone: "error", message: "Checkout was cancelled." });
        return;
      }
      if (outcome !== "success") return;

      setReturnState({
        tone: "pending",
        message:
          "Payment submitted. Crypto settles on-chain, so your balance will update once it confirms.",
      });
      // The credit may already have landed while the payer was redirecting
      // back, so it is worth one look rather than making them reload.
      await refreshBalance();
    })();
  }, [refreshBalance]);

  const [couponCode, setCouponCode] = useState("");
  const [couponState, setCouponState] = useState<
    "idle" | "loading" | "success" | "error"
  >("idle");
  const [couponMessage, setCouponMessage] = useState("");

  const openCheckoutFor = (inr: number) => {
    setCustomINR(String(inr));
    setCheckoutOpen(true);
  };

  const applyCoupon = async () => {
    const code = couponCode.trim();
    if (!code || couponState === "loading") return;
    setCouponState("loading");
    try {
      // The credited amount is whatever this code is configured for on the
      // backend, so report what the server actually granted.
      const { creditedUSD } = await creditsApi.redeemCoupon(code);
      await refreshBalance();
      setCouponState("success");
      setCouponMessage(
        `Coupon applied — ${fmtUSD(creditedUSD)} added to your balance.`,
      );
      setCouponCode("");
    } catch (e) {
      setCouponState("error");
      setCouponMessage(
        e instanceof Error ? e.message : "coupon redemption failed",
      );
    }
  };

  const parsedCustom = customINR ? parseFloat(customINR) : NaN;
  const effectiveINR = customINR
    ? Number.isFinite(parsedCustom)
      ? parsedCustom
      : 0
    : amountINR;
  const overMax = effectiveINR > MAX_INR;
  const checkoutAmountINR =
    effectiveINR >= 1 && !overMax ? effectiveINR : 0;
  const canCheckout = checkoutAmountINR > 0;
  const credits = creditsForTopup(checkoutAmountINR);
  // Only call a balance "low" once we've actually read it — before the first
  // fetch lands, balanceUSD is 0 because nothing is known, not because the
  // account is empty.
  const isLow = balanceKnown && balanceUSD < LOW_BALANCE_USD;

  return (
    <div
      className="am-viewport"
      style={{
        height: "100dvh",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background: "var(--bg)",
      }}
    >
      <style>{BILLING_CSS}</style>

      <Topbar />

      {/* Main scroll area */}
      <div style={{ flex: 1, overflow: "auto", background: "var(--bg)" }}>
        <div
          style={{
            maxWidth: 1040,
            margin: "0 auto",
            padding: "40px 24px 96px",
          }}
        >
          {/* Header */}
          <div className="bill-reveal" style={{ marginBottom: 24 }}>
            <h1
              style={{
                fontSize: 26,
                fontWeight: 700,
                letterSpacing: "-0.02em",
                margin: 0,
                color: "var(--fg)",
              }}
            >
              Add credits
            </h1>
            <p
              style={{
                margin: "6px 0 0",
                fontSize: 14,
                color: "var(--fg-muted)",
                lineHeight: 1.5,
              }}
            >
              Credits are spent as your agents call paid tools and models. Top
              up anytime; testnet usage stays free.
            </p>
          </div>

          {/* Outcome of a redirect checkout, above the fold: the payer has just
              come back from another site and the first thing they need is
              whether it worked. */}
          {returnState && (
            <div
              role="status"
              style={{
                marginTop: 18,
                padding: "12px 14px",
                borderRadius: "var(--r-2)",
                fontSize: 13,
                lineHeight: 1.5,
                border: `1px solid ${
                  returnState.tone === "error"
                    ? "var(--danger)"
                    : "var(--border)"
                }`,
                background: "var(--bg-elev-1)",
                color:
                  returnState.tone === "error"
                    ? "var(--danger)"
                    : "var(--fg-muted)",
              }}
            >
              {returnState.message}
            </div>
          )}

          <div className="bill-grid">
            {/* MAIN column */}
            <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
              {/* Balance hero */}
              <div
                className="bill-reveal"
                style={{
                  animationDelay: "0.05s",
                  position: "relative",
                  overflow: "hidden",
                  background:
                    "linear-gradient(135deg, var(--bg-elev-2), var(--bg-elev-1))",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-3)",
                  padding: 20,
                }}
              >
                <div
                  aria-hidden
                  style={{
                    position: "absolute",
                    top: -60,
                    right: -40,
                    width: 200,
                    height: 200,
                    borderRadius: 999,
                    background: "var(--accent-glow)",
                    filter: "blur(60px)",
                    opacity: 0.5,
                    pointerEvents: "none",
                  }}
                />
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    position: "relative",
                  }}
                >
                  <div>
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 7,
                        color: "var(--fg-muted)",
                        fontSize: 12,
                        fontWeight: 500,
                      }}
                    >
                      <IconWallet size={14} /> Wallet balance
                    </div>
                    <div
                      style={{
                        marginTop: 8,
                        fontFamily: "var(--font-mono)",
                        fontSize: 34,
                        fontWeight: 600,
                        letterSpacing: "-0.01em",
                        color: "var(--fg)",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {balanceKnown ? fmtUSD(balanceUSD) : "—"}
                    </div>
                  </div>
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      gap: 6,
                      height: 24,
                      padding: "0 10px",
                      borderRadius: 999,
                      fontSize: 11,
                      fontWeight: 500,
                      border: `1px solid ${isLow ? "rgba(255,181,71,0.35)" : "var(--accent-line)"}`,
                      background: isLow
                        ? "var(--warm-soft)"
                        : "var(--accent-soft)",
                      color: isLow ? "var(--warm)" : "var(--accent)",
                    }}
                  >
                    <span
                      style={{
                        width: 6,
                        height: 6,
                        borderRadius: 999,
                        background: isLow ? "var(--warm)" : "var(--accent)",
                      }}
                    />
                    {!balanceKnown
                      ? "Checking…"
                      : isLow
                        ? "Low balance"
                        : "Active"}
                  </span>
                </div>
              </div>

              {/* Top-up panel */}
              <div
                className="bill-reveal"
                style={{ ...panelStyle, animationDelay: "0.1s" }}
              >
                <div
                  style={{
                    fontSize: 12,
                    fontWeight: 600,
                    color: "var(--fg-muted)",
                    marginBottom: 12,
                  }}
                >
                  Choose an amount
                </div>

                {/* Preset cards */}
                <div
                  className="am-grid-4"
                  style={{
                    display: "grid",
                    gridTemplateColumns: "var(--wf-kpi-cols)",
                    gap: 8,
                  }}
                >
                  {PRESETS_INR.map((inr) => {
                    const selected = !customINR && amountINR === inr;
                    const hasBonus = bonusRate(inr) > 0;
                    return (
                      <button
                        key={inr}
                        type="button"
                        className="bill-preset"
                        onClick={() => {
                          setAmountINR(inr);
                          setCustomINR("");
                        }}
                        style={{
                          position: "relative",
                          display: "flex",
                          flexDirection: "column",
                          alignItems: "flex-start",
                          gap: 3,
                          padding: "12px 12px 11px",
                          borderRadius: "var(--r-2)",
                          border: `1px solid ${selected ? "var(--accent)" : "var(--border)"}`,
                          background: selected
                            ? "var(--accent-soft)"
                            : "var(--bg)",
                          cursor: "pointer",
                          boxShadow: selected
                            ? "0 0 0 3px var(--accent-soft)"
                            : "none",
                          fontFamily: "var(--font-sans)",
                        }}
                      >
                        <span
                          style={{
                            fontSize: 16,
                            fontWeight: 700,
                            color: "var(--fg)",
                            letterSpacing: "-0.01em",
                          }}
                        >
                          ₹{inr}
                        </span>
                        <span
                          style={{
                            fontSize: 11,
                            color: "var(--fg-dim)",
                            fontFamily: "var(--font-mono)",
                            fontVariantNumeric: "tabular-nums",
                          }}
                        >
                          ≈ {fmtUSD(creditsForTopup(inr))}
                        </span>
                        {hasBonus && (
                          <span
                            style={{
                              position: "absolute",
                              top: 8,
                              right: 8,
                              fontSize: 9,
                              fontWeight: 700,
                              color: "var(--accent)",
                              background: "var(--accent-soft)",
                              border: "1px solid var(--accent-line)",
                              borderRadius: 999,
                              padding: "1px 5px",
                            }}
                          >
                            +5%
                          </span>
                        )}
                      </button>
                    );
                  })}
                </div>

                {/* Custom amount */}
                <div style={{ marginTop: 14 }}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      height: 42,
                      padding: "0 12px",
                      borderRadius: "var(--r-2)",
                      border: `1px solid ${customINR ? "var(--accent-line)" : "var(--border)"}`,
                      background: "var(--bg)",
                    }}
                  >
                    <span style={{ color: "var(--fg-muted)", fontSize: 15 }}>
                      ₹
                    </span>
                    <input
                      // Deliberately type="text", not type="number": a number
                      // input carries spinner arrows (and scroll-wheel/arrow-key
                      // stepping) that let the amount change without anyone
                      // typing it. The value is still numeric — non-numeric
                      // characters are rejected on input below.
                      type="text"
                      inputMode="decimal"
                      placeholder="Custom amount"
                      value={customINR}
                      onChange={(e) => {
                        const next = e.target.value;
                        // Digits with at most one decimal point; empty clears
                        // back to the selected preset.
                        if (next === "" || /^\d*\.?\d*$/.test(next)) {
                          setCustomINR(next);
                        }
                      }}
                      style={{
                        flex: 1,
                        height: "100%",
                        background: "transparent",
                        border: "none",
                        outline: "none",
                        color: "var(--fg)",
                        fontSize: 14,
                        fontFamily: "var(--font-sans)",
                      }}
                    />
                    {canCheckout && (
                      <span
                        style={{
                          fontSize: 12,
                          color: "var(--fg-muted)",
                          fontFamily: "var(--font-mono)",
                          fontVariantNumeric: "tabular-nums",
                          whiteSpace: "nowrap",
                        }}
                      >
                        ≈ {fmtUSD(credits)} credits
                      </span>
                    )}
                  </div>
                  <p
                    style={{
                      margin: "8px 2px 0",
                      fontSize: 11,
                      color: overMax ? "var(--danger)" : "var(--fg-dim)",
                    }}
                  >
                    {overMax
                      ? `Maximum top-up is $${MAX_TOPUP_USD} (about ₹${MAX_INR.toLocaleString("en-IN")}).`
                      : "Get 5% bonus credits on top-ups of ₹1000 or more."}
                  </p>
                </div>

                {lastPurchase?.amountINR !== undefined && (
                  <button
                    type="button"
                    onClick={() => openCheckoutFor(lastPurchase.amountINR!)}
                    style={{
                      width: "100%",
                      height: 36,
                      marginTop: 14,
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--accent-line)",
                      background: "var(--accent-soft)",
                      color: "var(--accent)",
                      fontSize: 12.5,
                      fontWeight: 500,
                      cursor: "pointer",
                    }}
                  >
                    ↻ Repeat last top-up · ₹{lastPurchase.amountINR}
                  </button>
                )}

                {/* Primary CTA */}
                <button
                  type="button"
                  className="bill-cta"
                  onClick={() => setCheckoutOpen(true)}
                  disabled={!canCheckout}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    gap: 8,
                    width: "100%",
                    height: 46,
                    marginTop: 14,
                    borderRadius: "var(--r-2)",
                    border: "1px solid var(--accent-line)",
                    background: canCheckout
                      ? "linear-gradient(180deg, var(--accent), var(--accent-strong))"
                      : "var(--bg-elev-2)",
                    color: canCheckout ? "var(--accent-fg)" : "var(--fg-dim)",
                    fontSize: 14,
                    fontWeight: 600,
                    cursor: canCheckout ? "pointer" : "default",
                    boxShadow: canCheckout
                      ? "0 8px 24px var(--accent-glow)"
                      : "none",
                    fontFamily: "var(--font-sans)",
                  }}
                >
                  {canCheckout ? (
                    <>
                      Continue to checkout · ₹{checkoutAmountINR.toFixed(2)}
                      <IconArrow size={13} />
                    </>
                  ) : (
                    "Enter an amount of ₹1 or more"
                  )}
                </button>
              </div>
            </div>

            {/* SIDEBAR column */}
            <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
              {/* Coupon redemption */}
              <div
                className="bill-reveal"
                style={{ ...panelStyle, animationDelay: "0.12s" }}
              >
                <div
                  style={{
                    fontSize: 12,
                    fontWeight: 600,
                    color: "var(--fg-muted)",
                    marginBottom: 12,
                  }}
                >
                  Have a coupon?
                </div>
                <div style={{ display: "flex", gap: 8 }}>
                  <input
                    type="text"
                    placeholder="Coupon code"
                    value={couponCode}
                    onChange={(e) => {
                      setCouponCode(e.target.value);
                      if (couponState !== "idle") setCouponState("idle");
                    }}
                    onKeyDown={(e) => e.key === "Enter" && applyCoupon()}
                    style={{
                      flex: 1,
                      height: 38,
                      padding: "0 12px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--border)",
                      background: "var(--bg)",
                      color: "var(--fg)",
                      fontSize: 13,
                      fontFamily: "var(--font-mono)",
                      outline: "none",
                      textTransform: "uppercase",
                    }}
                  />
                  <button
                    type="button"
                    onClick={applyCoupon}
                    disabled={!couponCode.trim() || couponState === "loading"}
                    style={{
                      height: 38,
                      padding: "0 16px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--accent-line)",
                      background: "var(--accent-soft)",
                      color: "var(--accent)",
                      fontSize: 12.5,
                      fontWeight: 600,
                      cursor:
                        !couponCode.trim() || couponState === "loading"
                          ? "default"
                          : "pointer",
                      opacity:
                        !couponCode.trim() || couponState === "loading"
                          ? 0.5
                          : 1,
                      fontFamily: "var(--font-sans)",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {couponState === "loading" ? "Applying…" : "Apply"}
                  </button>
                </div>
                {couponMessage && (
                  <p
                    style={{
                      margin: "8px 2px 0",
                      fontSize: 11.5,
                      color:
                        couponState === "success"
                          ? "var(--accent)"
                          : "var(--danger)",
                    }}
                  >
                    {couponMessage}
                  </p>
                )}
              </div>

              {/* How credits work */}
              <div
                className="bill-reveal"
                style={{ ...panelStyle, animationDelay: "0.15s" }}
              >
                <div
                  style={{
                    fontSize: 12,
                    fontWeight: 600,
                    color: "var(--fg-muted)",
                    marginBottom: 14,
                  }}
                >
                  How credits work
                </div>
                <ul
                  style={{
                    margin: 0,
                    padding: 0,
                    listStyle: "none",
                    display: "flex",
                    flexDirection: "column",
                    gap: 12,
                  }}
                >
                  {HOW_IT_WORKS.map((item) => (
                    <li
                      key={item}
                      style={{
                        display: "flex",
                        gap: 10,
                        fontSize: 12.5,
                        lineHeight: 1.5,
                        color: "var(--fg-muted)",
                      }}
                    >
                      <span
                        aria-hidden
                        style={{
                          flexShrink: 0,
                          width: 5,
                          height: 5,
                          marginTop: 7,
                          borderRadius: 999,
                          background: "var(--accent)",
                        }}
                      />
                      {item}
                    </li>
                  ))}
                </ul>
              </div>

              {/* Billing history */}
              <div className="bill-reveal" style={{ animationDelay: "0.2s" }}>
                <PurchaseHistory onBuyAgain={openCheckoutFor} />
              </div>
            </div>
          </div>
        </div>
      </div>

      {checkoutOpen && (
        <CheckoutModal
          open
          amountINR={checkoutAmountINR}
          onClose={() => setCheckoutOpen(false)}
        />
      )}
    </div>
  );
}
