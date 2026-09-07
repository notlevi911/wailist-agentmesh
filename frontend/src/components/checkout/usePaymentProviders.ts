"use client";
import { useEffect, useState } from "react";
import { payments } from "@/lib/api";
import { PAYMENT_PROVIDERS, type PaymentProvider } from "./paymentProviders";

export interface ProviderState {
  providers: PaymentProvider[];
  /** Live INR->USD rate, used to price the USD gateways. 0 until loaded. */
  usdPerINR: number;
  loading: boolean;
}

// What a provider's sublabel becomes when this deployment cannot take money
// through it -- including Stripe and PayPal, which have no backend at all and
// so are never reported as available.
const UNAVAILABLE_SUBLABEL = "Coming soon";

// Resolves which providers this deployment can actually take money through.
//
// The static list carries labels and ordering; availability is deployment
// state (whether an FX rate could be fetched, and which gateways are built),
// so it is asked for rather than assumed. On failure everything is left
// disabled: offering a button that can only fail is worse than showing none.
export function usePaymentProviders(): ProviderState {
  const [state, setState] = useState<ProviderState>({
    // Cashfree needs no rate lookup and has never depended on this endpoint,
    // so it starts from its own static default rather than "disabled" --
    // otherwise every checkout open shows it as broken for the round trip.
    providers: PAYMENT_PROVIDERS.map((p) =>
      p.id === "cashfree"
        ? { ...p }
        : { ...p, enabled: false, sublabel: UNAVAILABLE_SUBLABEL },
    ),
    usdPerINR: 0,
    loading: true,
  });

  useEffect(() => {
    let cancelled = false;
    payments
      .listProviders()
      .then((res) => {
        if (cancelled) return;
        const enabledByID = new Map(res.providers.map((p) => [p.id, p.enabled]));
        setState({
          providers: PAYMENT_PROVIDERS.map((p) => {
            // An id the server didn't mention is one this build knows about
            // and that deployment does not -- treat it as off. This is how
            // Stripe and PayPal stay "Coming soon" without being special-cased.
            const enabled = enabledByID.get(p.id) ?? false;
            return {
              ...p,
              enabled,
              sublabel: enabled ? p.sublabel : UNAVAILABLE_SUBLABEL,
            };
          }),
          usdPerINR: res.usd_per_inr ?? 0,
          loading: false,
        });
      })
      .catch(() => {
        // The request itself failed, telling us nothing about availability --
        // unlike a stale FX rate (still a 200), this doesn't mean the rate
        // lookup is down. Cashfree needs no rate and has its own working
        // flow, so a transient failure here shouldn't disable it too.
        if (!cancelled) {
          setState((s) => ({
            ...s,
            providers: s.providers.map((p) => {
              if (p.id !== "cashfree") return p;
              const fallback = PAYMENT_PROVIDERS.find((d) => d.id === "cashfree");
              return fallback ? { ...fallback } : p;
            }),
            loading: false,
          }));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
