"use client";
import { useState } from "react";
import Link from "next/link";
import { ghostBtn } from "@/components/ui/buttons";
import { IconArrow, IconWallet } from "@/components/ui";
import { BalanceLoadNotice } from "@/components/billing/BalanceLoadNotice";
import { PurchaseHistory } from "@/components/billing/PurchaseHistory";
import { creditsForTopup } from "@/lib/credits/fx";
import type { Purchase } from "@/lib/credits/types";

// The Credits screen on a phone.
//
// A run that stops for want of credit is the one thing this app has to be able
// to fix from outside, so the balance leads and paying is directly under it.
//
// The dollar figure beside the amount is quoted at the rate the SERVER will
// charge at, never at a local constant. It used to come from a hardcoded 1/83
// plus a 5% bonus the backend never granted, which promised roughly 21% more
// credit than the ledger recorded -- on the screen where someone decides
// whether to pay.

const fmtUSD = (n: number) => `$${n.toFixed(2)}`;
const fmtINR = (n: number) => `₹${n.toLocaleString("en-IN")}`;
// Four amounts have to fit one phone row, and ₹10,000 does not. ₹10k does.
const shortINR = (n: number) =>
  n >= 1000 && n % 1000 === 0 ? `₹${n / 1000}k` : fmtINR(n);

export interface BillingPhoneProps {
  balanceUSD: number;
  balanceKnown: boolean;
  balanceLoading: boolean;
  balanceFailed: boolean;
  onRetryBalance: () => Promise<void>;
  isLow: boolean;
  /** Spent across every workflow over the same 30 days the list counts. */
  // null while unknown: still loading, or the load failed.
  spent30dUSD: number | null;
  returnState: { tone: "pending" | "error"; message: string } | null;
  /** The server's live rate. 0 until it arrives, and then nothing is quoted. */
  usdPerINR: number;
  presets: readonly number[];
  amountINR: number;
  onPreset: (inr: number) => void;
  customINR: string;
  onCustomChange: (value: string) => void;
  effectiveINR: number;
  overMax: boolean;
  maxINR: number;
  canCheckout: boolean;
  onCheckout: () => void;
  couponCode: string;
  onCouponChange: (value: string) => void;
  couponState: "idle" | "loading" | "success" | "error";
  couponMessage: string;
  onApplyCoupon: () => void;
  onBuyAgain: (amountINR: number) => void;
  /** The Android app, which pays on the website rather than on this screen. */
  native: boolean;
  onTopUpOnWeb: () => void;
  /** Newest first. Only the first shows until "See all" is pressed. */
  purchases: readonly Purchase[];
  purchasesKnown: boolean;
  // The history could not be loaded. PurchaseHistory then says so and offers
  // a retry, so the section has to be there to hold it.
  purchasesFailed?: boolean;
  howItWorks: readonly string[];
}

export function BillingPhonePage(p: BillingPhoneProps) {
  const [couponOpen, setCouponOpen] = useState(false);
  const [allPayments, setAllPayments] = useState(false);
  const [howOpen, setHowOpen] = useState(false);

  const state = !p.balanceKnown || p.balanceFailed || p.balanceLoading ? "unknown" : p.isLow ? "low" : "ok";
  const credits =
    p.usdPerINR > 0 && p.canCheckout
      ? creditsForTopup(p.effectiveINR, p.usdPerINR)
      : null;

  return (
    <main className="bilp-page">
      {/* Credits is not a tab -- it is reached from the Workflows "+", from
          Account and from a low-balance notification -- so there is no bottom
          bar here. Back returns to wherever it was opened from; with nothing
          to go back to, Workflows is home. */}
      <Link
        href="/workflows"
        className="bilp-back"
        style={{ ...ghostBtn, minHeight: 44, textDecoration: "none" }}
        onClick={(e) => {
          if (window.history.length <= 1) return;
          e.preventDefault();
          window.history.back();
        }}
      >
        ← Back
      </Link>
      <h1 className="bilp-title">Credits</h1>
      <p className="bilp-sub">What you hold, and what your agents spent.</p>

      <section className="bilp-stats" data-state={state}>
        <div className="bilp-stat">
          <span className="bilp-stat__label">Balance</span>
          <span className="bilp-stat__row">
            <span className="bilp-stat__value">
              {p.balanceKnown ? fmtUSD(p.balanceUSD) : "-"}
            </span>
            <span className="bilp-pill">
              <span className="bilp-pill__dot" aria-hidden />
              {p.balanceLoading
                ? (p.balanceKnown ? "Refreshing" : "Checking")
                : p.balanceFailed
                  ? "Unavailable"
                  : state === "unknown" ? "Checking" : state === "low" ? "Low" : "Active"}
            </span>
          </span>
        </div>
        <div className="bilp-stat">
          <span className="bilp-stat__label">Spent · 30d</span>
          <span className="bilp-stat__value">
            {p.spent30dUSD === null ? "-" : fmtUSD(p.spent30dUSD)}
          </span>
        </div>
      </section>

      <BalanceLoadNotice known={p.balanceKnown} failed={p.balanceFailed} loading={p.balanceLoading} onRetry={p.onRetryBalance} />

      {p.returnState && (
        <p className="bilp-note" data-tone={p.returnState.tone} role="status">
          {p.returnState.message}
        </p>
      )}

      {/* The app pays on the website: in-app checkout needs its own payment
          provider project. So the whole amount picker gives way to one
          button, and the amount is chosen on the site. */}
      {p.native ? (
        <section className="bilp-section">
          <h2 className="bilp-eyebrow">Top up</h2>
          <button type="button" className="bilp-pay" onClick={p.onTopUpOnWeb}>
            <IconWallet size={15} />
            Add credits on the website
            <span aria-hidden>↗</span>
          </button>
          <p className="bilp-note">
            Opens agent-mesh.app in a browser tab. Pay by card, UPI or crypto,
            signing in with this account if asked. Your balance here updates
            when you close the tab.
          </p>
        </section>
      ) : (
        <section className="bilp-section">
          <h2 className="bilp-eyebrow">Amount</h2>

          <div className="bilp-seg" role="radiogroup" aria-label="Amount">
            {p.presets.map((inr) => {
              // Against the effective amount, not against emptiness: choosing a
              // preset now fills the field, so a "no custom value" test would
              // light nothing. Typing 5000 by hand lights ₹5k too, which is
              // what someone who typed it would expect.
              const on = p.effectiveINR === inr;
              return (
                <button
                  key={inr}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  aria-label={fmtINR(inr)}
                  className="bilp-seg__item"
                  onClick={() => p.onPreset(inr)}
                >
                  {shortINR(inr)}
                </button>
              );
            })}
          </div>

          <div className="bilp-amount bilp-amount--figure">
            <span className="bilp-amount__prefix" aria-hidden>
              ₹
            </span>
            <input
              className="bilp-amount__input bilp-amount__input--figure"
              inputMode="decimal"
              aria-label="Amount in rupees"
              placeholder={String(p.amountINR)}
              value={p.customINR}
              onChange={(e) => p.onCustomChange(e.target.value)}
            />
            <span className="bilp-amount__usd">
              {credits === null ? "≈ -" : `≈ ${fmtUSD(credits)}`}
            </span>
          </div>

          {p.overMax && (
            <p className="bilp-note" data-tone="error" role="alert">
              The most you can add at once is {fmtINR(p.maxINR)}.
            </p>
          )}

          <button
            type="button"
            className="bilp-pay"
            onClick={p.onCheckout}
            disabled={!p.canCheckout}
          >
            <IconWallet size={15} />
            {p.canCheckout ? `Pay ${fmtINR(p.effectiveINR)}` : "Pay"}
          </button>
        </section>
      )}

      {/* Above the payment history, so the discount is offered before the
          receipt rather than after it. */}
      <section className="bilp-section">
        {couponOpen ? (
          <>
            <h2 className="bilp-eyebrow">Coupon</h2>
            <div className="bilp-amount">
              <input
                className="bilp-amount__input"
                placeholder="Code"
                aria-label="Coupon code"
                autoCapitalize="characters"
                autoCorrect="off"
                value={p.couponCode}
                onChange={(e) => p.onCouponChange(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && p.onApplyCoupon()}
              />
              <button
                type="button"
                className="bilp-link"
                onClick={p.onApplyCoupon}
                disabled={!p.couponCode.trim() || p.couponState === "loading"}
              >
                {p.couponState === "loading" ? "Applying…" : "Apply"}
              </button>
            </div>
          </>
        ) : (
          <button
            type="button"
            className="bilp-row"
            onClick={() => setCouponOpen(true)}
          >
            <span>Have a coupon?</span>
            <span className="bilp-row__action">Add</span>
          </button>
        )}
        {p.couponMessage && (
          <p
            className="bilp-note"
            data-tone={p.couponState === "error" ? "error" : "ok"}
            role="status"
          >
            {p.couponMessage}
          </p>
        )}
      </section>

      {/* One payment by default. The whole list runs off a phone screen, and
          what someone checks after paying is whether THIS one landed. */}
      {((p.purchasesKnown && p.purchases.length > 0) ||
        (p.purchasesFailed && p.purchases.length === 0)) && (
        <section className="bilp-section">
          <h2 className="bilp-heading">
            {allPayments || p.purchases.length === 0
              ? "Payments"
              : "Last payment"}
          </h2>
          <PurchaseHistory
            onBuyAgain={p.onBuyAgain}
            limit={allPayments ? undefined : 1}
            heading={null}
          />
          {!allPayments && p.purchases.length > 1 && (
            <button
              type="button"
              className="bilp-link bilp-link--block"
              onClick={() => setAllPayments(true)}
            >
              See all payments ({p.purchases.length})
            </button>
          )}
        </section>
      )}

      <section className="bilp-section">
        <button
          type="button"
          className="bilp-row"
          aria-expanded={howOpen}
          onClick={() => setHowOpen((v) => !v)}
        >
          <span>How credits work</span>
          <span className="bilp-row__chevron" data-open={howOpen} aria-hidden>
            <IconArrow size={13} />
          </span>
        </button>
        {howOpen && (
          <ul className="bilp-facts">
            {p.howItWorks.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
