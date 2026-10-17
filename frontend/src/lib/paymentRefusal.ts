export const PAYMENT_RECOVERY = {
  spending_limit: {
    message: "You were not charged. Review your per-call spending limit in Settings before trying again.",
    label: "Review spending limit",
    href: "/settings",
  },
  insufficient_credits: {
    message: "You were not charged. Add credit and try again.",
    label: "Add credits",
    href: "/billing",
  },
} as const;

export type PaymentRefusalCode = keyof typeof PAYMENT_RECOVERY;

export function paymentRefusalCode(status: number, code: unknown): PaymentRefusalCode | null {
  return status === 402 && (code === "spending_limit" || code === "insufficient_credits") ? code : null;
}
