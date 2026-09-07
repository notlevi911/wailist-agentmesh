// Types for the checkout modal. AgentMesh sells USD credits (spent per-call via
// x402 micropayments), so checkout line-items are credit bundles, not physical
// goods. Shapes stay simple so the mock data can later be swapped for the real
// selected top-up without touching the components.

// The payment providers AgentMesh settles credit top-ups through. Cashfree and
// NOWPayments are live; PayPal and Stripe are planned (rendered but disabled in
// the checkout). Buying digital credits online, there is deliberately no
// cash-on-delivery / card-form option -- the provider hosts its own payment UI.
export type PaymentMethod = "cashfree" | "nowpayments" | "paypal" | "stripe";

export interface CartItem {
  id: string;
  title: string; // bundle name, e.g. "AgentMesh Credits"
  detail: string; // credits + bonus, e.g. "$50 top-up · +$5 bonus credits"
  unitPrice: number; // USD charged per unit
  quantity: number;
}

export interface OrderTotals {
  subtotal: number;
  total: number;
}
