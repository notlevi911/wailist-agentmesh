import type { PaymentMethod } from "./types";

// Display metadata for the checkout's payment providers. Order here is the
// display order.
//
// `enabled` here is only the fallback used before the server answers (and if
// it never does). Real availability is deployment state -- NOWPayments needs a
// live FX rate to be priced at all -- so it comes from
// GET /payments/providers via usePaymentProviders.
//
// Stripe and PayPal are not implemented on the backend, so they are not
// reported by that endpoint and stay permanently "Coming soon" here.
export interface PaymentProvider {
  id: PaymentMethod;
  label: string;
  sublabel: string;
  enabled: boolean;
}

export const PAYMENT_PROVIDERS: PaymentProvider[] = [
  {
    id: "cashfree",
    label: "Cashfree",
    sublabel: "UPI, cards & netbanking · INR",
    enabled: true,
  },
  {
    id: "nowpayments",
    label: "NOWPayments",
    sublabel: "Crypto · USD",
    enabled: true,
  },
  {
    id: "paypal",
    label: "PayPal",
    sublabel: "Coming soon",
    enabled: false,
  },
  {
    id: "stripe",
    label: "Stripe",
    sublabel: "Coming soon",
    enabled: false,
  },
];

// The default selected provider -- the first enabled one.
export const DEFAULT_PROVIDER: PaymentMethod =
  PAYMENT_PROVIDERS.find((p) => p.enabled)?.id ?? "cashfree";

// Providers that settle in USD. The top-up UI is denominated in rupees, so
// these convert the chosen amount at the server's live rate before charging.
export const USD_PROVIDERS: ReadonlySet<PaymentMethod> = new Set<PaymentMethod>(
  ["nowpayments"],
);

// Mirrors minCryptoAmountUSDCents in handlers/payments.go -- NOWPayments
// rejects an invoice below this with a 400, so the Pay button must not offer
// it as if it would succeed.
export const MIN_CRYPTO_AMOUNT_USD_CENTS = 100;

// Mirrors maxCryptoAmountUSDCents in handlers/payments.go -- same reasoning
// as the minimum above, at the other end of the range.
export const MAX_CRYPTO_AMOUNT_USD_CENTS = 600_000;
