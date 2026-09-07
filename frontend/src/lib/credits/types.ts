import type { PaymentMethod } from "@/components/checkout/types";

// Credit wallet model. Both fields are server-owned and neither is persisted
// locally: balanceUSD comes from users.credit_balance_usd_micros and purchases
// from credit_ledger, the same rows the payment webhooks write and settle. A
// per-browser copy showed the wrong history after a sign-out or an account
// switch while the database had it right all along.

// Mirrors credit_ledger.status. 'completed' is the only state where credits
// were actually granted; the rest are shown so a user who paid and saw nothing
// land can tell which of "still settling", "the gateway declined", and "we
// stopped waiting" happened, instead of finding an empty page.
export type PurchaseStatus =
  | "pending"
  | "completed"
  | "failed"
  | "expired"
  | "partial"
  | "refunded";

export interface Purchase {
  id: string;
  createdAt: string; // ISO 8601
  // Exactly one of these is set, matching how the row was paid: the Cashfree
  // path is INR-denominated (with an FX rate recorded at purchase time), the
  // crypto path is already USD. A renderer must handle either.
  amountINR?: number;
  amountUSD?: number;
  creditsUSD: number; // credits granted
  method: PaymentMethod;
  status: PurchaseStatus;
}

export interface CreditsState {
  balanceUSD: number;
  purchases: Purchase[]; // newest first
}
