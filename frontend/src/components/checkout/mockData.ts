import type { CartItem, OrderTotals } from "./types";

// Builds the checkout cart from the amount chosen on the billing page. AgentMesh
// users buy INR credit top-ups (funded via Razorpay, spent through x402
// micropayments), so the cart holds a single credit line whose price is the
// selected/custom amount rather than a hardcoded bundle.
export function buildCreditCart(amountINR: number): CartItem[] {
  return [
    {
      id: "credit-topup",
      title: "AgentMesh Credits",
      detail: "Credit top-up",
      unitPrice: amountINR,
      quantity: 1,
    },
  ];
}

export function computeTotals(items: CartItem[]): OrderTotals {
  const subtotal = items.reduce(
    (sum, item) => sum + item.unitPrice * item.quantity,
    0,
  );
  // Digital credits: no shipping or discount lines -- you pay the subtotal.
  return { subtotal, total: subtotal };
}
