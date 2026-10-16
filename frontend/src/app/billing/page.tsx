"use client";
import { useEffect, useState } from "react";
import { IconArrow, IconWallet } from "@/components/ui";
import { Topbar } from "@/components/Topbar";
import { BalanceLoadNotice } from "@/components/billing/BalanceLoadNotice";
import { PurchaseHistory } from "@/components/billing/PurchaseHistory";
import { CheckoutModal } from "@/components/checkout/CheckoutModal";
import { useCredits } from "@/lib/credits/store";
import { creditsForTopup, maxTopupINR, MAX_TOPUP_USD } from "@/lib/credits/fx";
import { credits as creditsApi, workflows as workflowsApi } from "@/lib/api";
import { totalSpend, totalSpendKnown } from "@/lib/workflowMeta";
import { useReadOnly } from "@/hooks/useReadOnly";
import { usePaymentProviders } from "@/components/checkout/usePaymentProviders";
import { BillingPhonePage } from "@/components/billing/phone/BillingPhonePage";
import { IS_NATIVE } from "@/lib/nativeAuth";
import { openExternal, WEB_BILLING_URL } from "@/lib/openExternal";

const PRESETS_INR = [1000, 5000, 10000, 20000];

const HOW_IT_WORKS = [
  "Credits are spent as your agents call paid tools, x402 endpoints, and LLM providers.",
  "Testnet usage is always free. You only pay for mainnet calls.",
  "Every purchase generates a printable receipt for your records.",
];

const BILLING_CSS = `
.bill-reveal { animation: fade-up 0.45s var(--ease) both; }
.bill-preset { transition: transform 0.15s var(--ease), border-color 0.15s var(--ease), background 0.15s var(--ease); }
.bill-cta { transition: transform 0.12s var(--ease), box-shadow 0.2s var(--ease); }
/* Hover only where a pointer hovers: a tap on a touch screen leaves :hover set,
   and the tapped preset would stay lifted. */
@media (hover: hover) {
  .bill-preset:hover { transform: translateY(-2px); border-color: var(--border-strong); }
  .bill-cta:not(:disabled):hover { box-shadow: 0 12px 34px var(--accent-glow); }
}
.bill-cta:not(:disabled):active { transform: scale(0.99); }
.bill-grid { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: var(--s-5); align-items: start; }
@media (max-width: 900px) { .bill-grid { grid-template-columns: minmax(0, 1fr); } }
.bill-page { max-width: 1040px; margin: 0 auto; padding: 40px 24px 96px; }
@media (max-width: 520px) { .bill-page { padding: 24px 16px 64px; } }
/* The amount field, Repeat and the coupon row are 36–42px for a mouse. */
@media (pointer: coarse) { .bill-touch { min-height: 44px; } }
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

// The phone screen and the desktop page share the same full-height shell.
const viewportStyle: React.CSSProperties = {
  height: "100dvh",
  display: "flex",
  flexDirection: "column",
  overflow: "hidden",
  background: "var(--bg)",
};

export default function BillingPage() {
  const {
    balanceUSD,
    lowBalanceThresholdUSD,
    balanceKnown,
    balanceLoading,
    balanceFailed,
    lastPurchase,
    refreshBalance,
    refreshPurchases,
    purchases,
    purchasesKnown,
    purchasesFailed,
  } = useCredits();
  // The same 30-day figure the Workflows header shows, from the same
  // helper, so the two screens cannot quote different numbers.
  // null until the list answers, and after it fails: zero would read as a
  // verified account that spent nothing.
  const [spent30dUSD, setSpent30dUSD] = useState<number | null>(null);
  useEffect(() => {
    let live = true;
    void workflowsApi
      .list()
      .then((wfs) => {
        // A list can arrive intact while the aggregation behind it failed,
        // and every omitted spend then sums to zero. "$0.00" is a figure the
        // reader has no reason to doubt, so it stays unknown instead.
        if (live && totalSpendKnown(wfs)) setSpent30dUSD(totalSpend(wfs));
      })
      // A missing spend figure is cosmetic; the balance above it is not.
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);
  const readOnly = useReadOnly();
  // The rate the server will actually charge at. Quoting from
  // lib/credits/fx.ts's old constant promised about 21% more credit than
  // the ledger granted, because it also added a bonus nothing paid.
  const { usdPerINR } = usePaymentProviders();
  const MAX_INR = maxTopupINR(usdPerINR);
  const [amountINR, setAmountINR] = useState<number>(PRESETS_INR[1]);
  const [customINR, setCustomINR] = useState("");
  // The phone shows this field as the amount itself rather than as a "custom"
  // override, so while it is untouched it displays the chosen preset. Empty
  // would render as the placeholder, in --fg-dim, making the figure about to
  // be charged read as a suggestion.
  //
  // Tracked with a flag instead of seeding customINR, because seeding it would
  // also pre-fill the desktop's Custom amount box and unselect its presets,
  // and instead of `customINR || amountINR`, which repopulates itself on the
  // keystroke that empties it and cannot be cleared.
  const [amountTouched, setAmountTouched] = useState(false);
  const [checkoutOpen, setCheckoutOpen] = useState(false);

  // Read the authoritative balance (users.credit_balance_usd_micros) every time
  // this page is opened. The store keeps a cross-route copy in memory, but it
  // goes stale the moment a run spends credits in another tab — and this is the
  // page where the number has to be right.
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  // History too, from here rather than from PurchaseHistory alone: the phone
  // screen only mounts that component once the history is known, so on a
  // fresh session nothing would ever have asked for it.
  useEffect(() => {
    void refreshPurchases();
  }, [refreshPurchases]);

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
    // The phone field shows customINR only once it counts as touched. Without
    // this, after Buy again the field kept showing the old preset while the
    // selected segment and the Pay button used the purchase's amount.
    setAmountTouched(true);
    setCheckoutOpen(true);
  };

  // The Android app does not take payment itself: in-app checkout needs its
  // own payment-provider project, so the app pays on the website instead, in
  // an in-app browser tab. The balance and history are re-read when the tab
  // closes, whether or not a payment went through. Phone browsers and desktop
  // keep the checkout on this page.
  const topUpOnWeb = () => {
    void openExternal(WEB_BILLING_URL, {
      onClose: () => {
        void refreshBalance();
        void refreshPurchases();
      },
    });
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
        `Coupon applied: ${fmtUSD(creditedUSD)} added to your balance.`,
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
  const checkoutAmountINR = effectiveINR >= 1 && !overMax ? effectiveINR : 0;
  const canCheckout = checkoutAmountINR > 0;
  const credits =
    usdPerINR > 0 ? creditsForTopup(checkoutAmountINR, usdPerINR) : null;
  // Only call a balance "low" once we've actually read it — before the first
  // fetch lands, balanceUSD is 0 because nothing is known, not because the
  // account is empty.
  const isLow = balanceKnown && !balanceFailed && balanceUSD < lowBalanceThresholdUSD;

  // A phone gets its own screen rather than the two-column page squeezed:
  // the balance leads, topping up is directly under it, and the rest is flat
  // sections. Every hook above has already run, so this return is safe; the
  // desktop JSX below is untouched.
  if (readOnly) {
    return (
      <div className="am-viewport" style={viewportStyle}>
        <style>{BILLING_CSS}</style>
        <Topbar />
        <div style={{ flex: 1, overflow: "auto", background: "var(--bg)" }}>
          <BillingPhonePage
            balanceUSD={balanceUSD}
            spent30dUSD={spent30dUSD}
            usdPerINR={usdPerINR}
            purchases={purchases}
            purchasesKnown={purchasesKnown}
            purchasesFailed={purchasesFailed}
            balanceKnown={balanceKnown}
            balanceLoading={balanceLoading}
            balanceFailed={balanceFailed}
            onRetryBalance={refreshBalance}
            isLow={isLow}
            returnState={returnState}
            presets={PRESETS_INR}
            amountINR={amountINR}
            onPreset={(inr) => {
              setAmountINR(inr);
              setCustomINR("");
              setAmountTouched(false);
            }}
            // Untouched, the field shows the preset as a real value.
            customINR={amountTouched ? customINR : String(amountINR)}
            // The same guard the desktop field uses. It matters more here now
            // that this field is always populated: pasting "5,000" would
            // otherwise parse to 5 and offer to charge ₹5.
            onCustomChange={(next) => {
              if (next !== "" && !/^\d*\.?\d*$/.test(next)) return;
              setAmountTouched(true);
              setCustomINR(next);
            }}
            effectiveINR={effectiveINR}
            overMax={overMax}
            maxINR={MAX_INR}
            canCheckout={canCheckout}
            onCheckout={() => setCheckoutOpen(true)}
            couponCode={couponCode}
            onCouponChange={(v) => {
              setCouponCode(v);
              if (couponState !== "idle") setCouponState("idle");
            }}
            couponState={couponState}
            couponMessage={couponMessage}
            onApplyCoupon={applyCoupon}
            native={IS_NATIVE}
            onTopUpOnWeb={topUpOnWeb}
            onBuyAgain={IS_NATIVE ? topUpOnWeb : openCheckoutFor}
            howItWorks={HOW_IT_WORKS}
          />
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

  return (
    <div className="am-viewport" style={viewportStyle}>
      <style>{BILLING_CSS}</style>

      <Topbar />

      {/* Main scroll area */}
      <div style={{ flex: 1, overflow: "auto", background: "var(--bg)" }}>
        <div className="bill-page">
          {/* Header */}
          <div className="bill-reveal" style={{ marginBottom: 24 }}>
            <h1
              style={{
                fontSize: "var(--t-6)",
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
                fontSize: "var(--t-4)",
                color: "var(--fg-muted)",
                lineHeight: 1.5,
              }}
            >
              Testnet usage stays free.
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
                fontSize: "var(--t-3)",
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
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "var(--s-5)",
              }}
            >
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
                    borderRadius: "var(--r-full)",
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
                        gap: "var(--s-2)",
                        color: "var(--fg-muted)",
                        fontSize: "var(--t-2)",
                        fontWeight: 500,
                      }}
                    >
                      <IconWallet size={14} /> Wallet balance
                    </div>
                    <div
                      style={{
                        marginTop: 8,
                        fontFamily: "var(--font-mono)",
                        fontSize: "var(--t-7)",
                        fontWeight: 600,
                        letterSpacing: "-0.01em",
                        color: "var(--fg)",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {balanceKnown ? fmtUSD(balanceUSD) : "-"}
                    </div>
                  </div>
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      gap: "var(--s-2)",
                      height: 24,
                      padding: "0 10px",
                      borderRadius: "var(--r-full)",
                      fontSize: "var(--t-1)",
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
                        borderRadius: "var(--r-full)",
                        background: isLow ? "var(--warm)" : "var(--accent)",
                      }}
                    />
                    {balanceLoading
                      ? (balanceKnown ? "Refreshing…" : "Checking…")
                      : balanceFailed
                        ? "Unavailable"
                        : !balanceKnown
                          ? "Checking…"
                          : isLow ? "Low balance" : "Active"}
                  </span>
                </div>
                <BalanceLoadNotice known={balanceKnown} failed={balanceFailed} loading={balanceLoading} onRetry={refreshBalance} />
              </div>

              {/* Top-up panel */}
              <div
                className="bill-reveal"
                style={{ ...panelStyle, animationDelay: "0.1s" }}
              >
                <div
                  style={{
                    fontSize: "var(--t-2)",
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
                    gap: "var(--s-3)",
                  }}
                >
                  {PRESETS_INR.map((inr) => {
                    const selected = !customINR && amountINR === inr;
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
                          gap: "var(--s-0)",
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
                            fontSize: "var(--t-4)",
                            fontWeight: 700,
                            color: "var(--fg)",
                            letterSpacing: "-0.01em",
                          }}
                        >
                          ₹{inr}
                        </span>
                        <span
                          style={{
                            fontSize: "var(--t-1)",
                            color: "var(--fg-dim)",
                            fontFamily: "var(--font-mono)",
                            fontVariantNumeric: "tabular-nums",
                          }}
                        >
                          {usdPerINR > 0
                            ? `≈ ${fmtUSD(creditsForTopup(inr, usdPerINR))}`
                            : "≈ -"}
                        </span>
                      </button>
                    );
                  })}
                </div>

                {/* Custom amount */}
                <div style={{ marginTop: 14 }}>
                  <div
                    className="bill-touch"
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "var(--s-3)",
                      height: 42,
                      padding: "0 12px",
                      borderRadius: "var(--r-2)",
                      border: `1px solid ${customINR ? "var(--accent-line)" : "var(--border)"}`,
                      background: "var(--bg)",
                    }}
                  >
                    <span
                      style={{
                        color: "var(--fg-muted)",
                        fontSize: "var(--t-4)",
                      }}
                    >
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
                        // A flex item's min-width defaults to `auto`, which for
                        // an input is its intrinsic size -- so `flex: 1` could
                        // grow it but never shrink it below that floor. The
                        // "≈ $x credits" hint beside it is nowrap and cannot
                        // shrink either, so on a 375px screen the row overflowed
                        // its own border by ~69px and the hint was cut off.
                        // This is the property that exists to say "yes, you may
                        // shrink".
                        minWidth: 0,
                        height: "100%",
                        background: "transparent",
                        border: "none",
                        outline: "none",
                        color: "var(--fg)",
                        fontSize: "var(--t-4)",
                        fontFamily: "var(--font-sans)",
                      }}
                    />
                    {canCheckout && (
                      <span
                        style={{
                          fontSize: "var(--t-2)",
                          color: "var(--fg-muted)",
                          fontFamily: "var(--font-mono)",
                          fontVariantNumeric: "tabular-nums",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {credits === null ? "≈ -" : `≈ ${fmtUSD(credits)}`}
                        <span className="bill-custom-unit"> credits</span>
                      </span>
                    )}
                  </div>
                  <p
                    style={{
                      margin: "8px 2px 0",
                      fontSize: "var(--t-1)",
                      color: overMax ? "var(--danger)" : "var(--fg-dim)",
                    }}
                  >
                    {overMax
                      ? `Maximum top-up is $${MAX_TOPUP_USD} (about ₹${MAX_INR.toLocaleString("en-IN")}).`
                      : "Credits are added as soon as the payment clears."}
                  </p>
                </div>

                {lastPurchase?.amountINR !== undefined && (
                  <button
                    type="button"
                    className="bill-touch"
                    onClick={() => openCheckoutFor(lastPurchase.amountINR!)}
                    style={{
                      width: "100%",
                      height: 36,
                      marginTop: 14,
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--accent-line)",
                      background: "var(--accent-soft)",
                      color: "var(--accent)",
                      fontSize: "var(--t-2)",
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
                    gap: "var(--s-3)",
                    width: "100%",
                    height: 46,
                    marginTop: 14,
                    borderRadius: "var(--r-2)",
                    border: "1px solid var(--accent-line)",
                    background: canCheckout
                      ? "linear-gradient(180deg, var(--accent), var(--accent-strong))"
                      : "var(--bg-elev-2)",
                    color: canCheckout ? "var(--accent-fg)" : "var(--fg-dim)",
                    fontSize: "var(--t-4)",
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
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "var(--s-5)",
              }}
            >
              {/* Coupon redemption */}
              <div
                className="bill-reveal"
                style={{ ...panelStyle, animationDelay: "0.12s" }}
              >
                <div
                  style={{
                    fontSize: "var(--t-2)",
                    fontWeight: 600,
                    color: "var(--fg-muted)",
                    marginBottom: 12,
                  }}
                >
                  Have a coupon?
                </div>
                <div style={{ display: "flex", gap: "var(--s-3)" }}>
                  <input
                    type="text"
                    className="bill-touch"
                    placeholder="Coupon code"
                    value={couponCode}
                    onChange={(e) => {
                      setCouponCode(e.target.value);
                      if (couponState !== "idle") setCouponState("idle");
                    }}
                    onKeyDown={(e) => e.key === "Enter" && applyCoupon()}
                    style={{
                      flex: 1,
                      // An input's default min-width is its intrinsic size, so
                      // without this it could not shrink and pushed Apply past
                      // the card's padding on a 360px screen.
                      minWidth: 0,
                      height: 38,
                      padding: "0 12px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--border)",
                      background: "var(--bg)",
                      color: "var(--fg)",
                      fontSize: "var(--t-3)",
                      fontFamily: "var(--font-mono)",
                      outline: "none",
                      textTransform: "uppercase",
                    }}
                  />
                  <button
                    type="button"
                    className="bill-touch"
                    onClick={applyCoupon}
                    disabled={!couponCode.trim() || couponState === "loading"}
                    style={{
                      height: 38,
                      padding: "0 16px",
                      borderRadius: "var(--r-2)",
                      border: "1px solid var(--accent-line)",
                      background: "var(--accent-soft)",
                      color: "var(--accent)",
                      fontSize: "var(--t-2)",
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
                      fontSize: "var(--t-1)",
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
                    fontSize: "var(--t-2)",
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
                    gap: "var(--s-4)",
                  }}
                >
                  {HOW_IT_WORKS.map((item) => (
                    <li
                      key={item}
                      style={{
                        display: "flex",
                        gap: "var(--s-3)",
                        fontSize: "var(--t-2)",
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
                          borderRadius: "var(--r-full)",
                          background: "var(--accent)",
                        }}
                      />
                      {item}
                    </li>
                  ))}
                </ul>
              </div>

              {/* Billing history */}
              <div
                className="bill-reveal"
                style={{ animationDelay: "0.2s", marginTop: 32 }}
              >
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
