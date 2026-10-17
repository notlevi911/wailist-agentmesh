import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Purchase } from "@/lib/credits/types";

const historyProps = vi.hoisted(() => ({ last: null as unknown }));

vi.mock("@/components/billing/PurchaseHistory", () => ({
  PurchaseHistory: (props: { limit?: number; heading?: string | null }) => {
    historyProps.last = props;
    return <div data-testid="history" />;
  },
}));

import { BillingPhonePage, type BillingPhoneProps } from "./BillingPhonePage";

const noop = () => {};

// The rate the backend reported live on 2026-09-20.
const LIVE = 0.010423;

const purchase = (id: string): Purchase => ({
  id,
  createdAt: "2026-08-03T10:00:00.000Z",
  amountINR: 500,
  creditsUSD: 5.24,
  method: "cashfree",
  status: "completed",
});

function renderPage(over: Partial<BillingPhoneProps> = {}) {
  const props: BillingPhoneProps = {
    balanceUSD: 5.24,
    balanceKnown: true,
    balanceLoading: false,
    balanceFailed: false,
    onRetryBalance: vi.fn(async () => {}),
    isLow: false,
    spent30dUSD: 8.71,
    returnState: null,
    usdPerINR: LIVE,
    presets: [1000, 5000, 10000, 20000],
    amountINR: 5000,
    onPreset: vi.fn(),
    customINR: "",
    onCustomChange: noop,
    effectiveINR: 5000,
    overMax: false,
    maxINR: 95942,
    canCheckout: true,
    onCheckout: vi.fn(),
    couponCode: "",
    onCouponChange: noop,
    couponState: "idle",
    couponMessage: "",
    onApplyCoupon: vi.fn(),
    onBuyAgain: noop,
    native: false,
    onTopUpOnWeb: vi.fn(),
    purchases: [purchase("p1")],
    purchasesKnown: true,
    howItWorks: ["Credits are spent as your agents call paid tools."],
    ...over,
  };
  const utils = render(<BillingPhonePage {...props} />);
  return { ...utils, props };
}

afterEach(() => {
  cleanup();
  historyProps.last = null;
});

describe("BillingPhonePage", () => {
  it("leads with the balance beside what the agents spent", () => {
    const { container } = renderPage();
    expect(screen.getByText("$5.24")).toBeTruthy();
    expect(screen.getByText("Balance")).toBeTruthy();
    expect(screen.getByText("Spent · 30d")).toBeTruthy();
    expect(screen.getByText("$8.71")).toBeTruthy();
    expect(
      container.querySelector(".bilp-stats")?.getAttribute("data-state"),
    ).toBe("ok");
  });

  // Zero is a real figure; not knowing is not zero.
  it("shows a dash for spend until it is known", () => {
    const { container } = renderPage({ spent30dUSD: null });
    const stat = screen.getByText("Spent · 30d").parentElement!;
    expect(stat.textContent).toContain("-");
    expect(container.textContent).not.toContain("$0.00");
  });

  it("warns when the balance is low", () => {
    const { container } = renderPage({ isLow: true, balanceUSD: 1.2 });
    expect(screen.getByText("Low")).toBeTruthy();
    expect(
      container.querySelector(".bilp-stats")?.getAttribute("data-state"),
    ).toBe("low");
  });

  it("shows a dash, not $0.00, until the balance is known", () => {
    const { container } = renderPage({ balanceKnown: false, balanceUSD: 0 });
    expect(screen.getByText("-")).toBeTruthy();
    expect(container.textContent).not.toContain("$0.00");
  });
});

// The regression this screen was rebuilt for. The estimate used to be
// amountINR / 83 * 1.05, quoting $63.25 for ₹5,000 while the ledger credited
// about $52.12 -- directly above the Pay button.
describe("the quote", () => {
  it("converts at the rate the server will charge, with no bonus", () => {
    const { container } = renderPage();
    expect(container.textContent).toContain("≈ $52.12");
    expect(container.textContent).not.toContain("63.25");
  });

  it("never mentions a bonus anywhere on the screen", () => {
    const { container } = renderPage();
    expect(container.textContent).not.toMatch(/bonus|5%/i);
  });

  it("quotes nothing at all until the rate arrives", () => {
    const { container } = renderPage({ usdPerINR: 0 });
    expect(container.textContent).toContain("≈ -");
    expect(container.textContent).not.toMatch(/≈ \$/);
  });

  it("names the amount on the Pay button", () => {
    const onCheckout = vi.fn();
    renderPage({ onCheckout });
    fireEvent.click(screen.getByRole("button", { name: /^Pay ₹5,000$/ }));
    expect(onCheckout).toHaveBeenCalledTimes(1);
  });

  it("refuses an amount over the maximum, and says the maximum", () => {
    renderPage({ overMax: true, canCheckout: false, maxINR: 95942 });
    expect(screen.getByRole("alert").textContent).toContain("₹95,942");
    expect(screen.getByRole("button", { name: "Pay" })).toHaveProperty(
      "disabled",
      true,
    );
  });
});

describe("the amount control", () => {
  it("is a radio group that marks and reports the chosen amount", () => {
    const onPreset = vi.fn();
    renderPage({ onPreset });
    const chosen = screen.getByRole("radio", { name: "₹5,000" });
    expect(chosen.getAttribute("aria-checked")).toBe("true");
    // Shown short so four fit a phone row, but announced in full.
    expect(chosen.textContent).toBe("₹5k");
    fireEvent.click(screen.getByRole("radio", { name: "₹1,000" }));
    expect(onPreset).toHaveBeenCalledWith(1000);
  });

  // The amount used to live only in the input's placeholder, which is painted
  // in --fg-dim: the figure about to be charged looked like a suggestion.
  it("shows the chosen amount as a value, not as a placeholder", () => {
    renderPage({ customINR: "5000" });
    expect(screen.getByLabelText("Amount in rupees")).toHaveProperty(
      "value",
      "5000",
    );
  });

  // The segment tracks the effective amount, so a filled field still marks
  // its preset -- and a hand-typed 5000 marks ₹5k too.
  it("marks the preset matching the effective amount", () => {
    renderPage({ customINR: "5000" });
    const checked = (name: string) =>
      screen.getByRole("radio", { name }).getAttribute("aria-checked");
    expect(checked("₹5,000")).toBe("true");
    expect(checked("₹1,000")).toBe("false");
  });
});

describe("the coupon", () => {
  it("sits above the payment history, and opens from a row", () => {
    const { container } = renderPage({ purchases: [purchase("p1")] });
    const text = container.textContent ?? "";
    expect(text.indexOf("Have a coupon?")).toBeGreaterThan(-1);
    expect(text.indexOf("Have a coupon?")).toBeLessThan(
      text.indexOf("Last payment"),
    );
    // Collapsed until asked for, so it costs one line rather than a field.
    expect(screen.queryByLabelText("Coupon code")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Have a coupon/ }));
    expect(screen.getByLabelText("Coupon code")).toBeTruthy();
  });

  it("applies a code and reports what came back", () => {
    const onApplyCoupon = vi.fn();
    renderPage({
      couponCode: "WELCOME",
      onApplyCoupon,
      couponState: "success",
      couponMessage: "Coupon applied: $5.00 added to your balance.",
    });
    fireEvent.click(screen.getByRole("button", { name: /Have a coupon/ }));
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onApplyCoupon).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/\$5\.00 added/)).toBeTruthy();
  });
});

describe("payments", () => {
  it("shows only the latest, so the list cannot run off the screen", () => {
    renderPage({ purchases: [purchase("a"), purchase("b"), purchase("c")] });
    expect(screen.getByText("Last payment")).toBeTruthy();
    expect(historyProps.last).toMatchObject({ limit: 1, heading: null });
  });

  it("offers See all only when there is more than one, and expands it", () => {
    renderPage({ purchases: [purchase("a"), purchase("b"), purchase("c")] });
    fireEvent.click(
      screen.getByRole("button", { name: "See all payments (3)" }),
    );
    expect(historyProps.last).toMatchObject({ limit: undefined });
    expect(screen.getByText("Payments")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /See all/ })).toBeNull();
  });

  it("does not offer See all for a single payment", () => {
    renderPage({ purchases: [purchase("only")] });
    expect(screen.queryByRole("button", { name: /See all/ })).toBeNull();
  });

  // PurchaseHistory holds the failure message and its retry.
  it("keeps the section when the history failed to load", () => {
    renderPage({ purchasesKnown: false, purchasesFailed: true, purchases: [] });
    expect(screen.getByTestId("history")).toBeTruthy();
  });

  it("says nothing at all before the payments have loaded", () => {
    renderPage({ purchasesKnown: false, purchases: [] });
    expect(screen.queryByText("Last payment")).toBeNull();
    expect(screen.queryByTestId("history")).toBeNull();
  });
});

describe("how credits work", () => {
  it("is collapsed until asked for", () => {
    renderPage();
    const row = screen.getByRole("button", { name: /How credits work/ });
    expect(row.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText(/agents call paid tools/)).toBeNull();
    fireEvent.click(row);
    expect(screen.getByText(/agents call paid tools/)).toBeTruthy();
  });
});

// The Android app pays on the website, so the amount picker gives way to one
// button that opens it.
describe("in the Android app", () => {
  it("offers the website instead of an amount and a Pay button", () => {
    const onTopUpOnWeb = vi.fn();
    renderPage({ native: true, onTopUpOnWeb });

    expect(screen.queryByRole("radiogroup", { name: "Amount" })).toBeNull();
    expect(screen.queryByLabelText("Amount in rupees")).toBeNull();
    expect(screen.queryByRole("button", { name: /^Pay/ })).toBeNull();

    fireEvent.click(
      screen.getByRole("button", { name: /Add credits on the website/ }),
    );
    expect(onTopUpOnWeb).toHaveBeenCalledTimes(1);
  });

  it("keeps the balance, the coupon and the payment history", () => {
    const { container } = renderPage({ native: true });
    expect(container.querySelector(".bilp-stats")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Have a coupon/ })).toBeTruthy();
    expect(screen.getByText("Last payment")).toBeTruthy();
  });
});

// Credits is not a tab, so it has no bottom bar and needs its own way back.
describe("the way back", () => {
  it("returns to wherever Credits was opened from", () => {
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const length = vi.spyOn(window.history, "length", "get").mockReturnValue(3);
    renderPage();
    fireEvent.click(screen.getByRole("link", { name: /Back/ }));
    expect(back).toHaveBeenCalledTimes(1);
    back.mockRestore();
    length.mockRestore();
  });

  it("falls back to Workflows when there is nothing to go back to", () => {
    renderPage();
    expect(
      screen.getByRole("link", { name: /Back/ }).getAttribute("href"),
    ).toBe("/workflows");
  });
});


describe("phone balance recovery", () => {
  it("shows first failure and an observable retry before recovery", () => {
    const view = renderPage({ balanceKnown: false, balanceFailed: true });
    expect(screen.getByText("Could not load your balance.")).toBeTruthy();
    expect(screen.getByText("Unavailable")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry balance" }));
    expect(view.props.onRetryBalance).toHaveBeenCalledOnce();
    view.rerender(<BillingPhonePage {...view.props} balanceLoading />);
    expect((screen.getByRole("button", { name: "Retry balance" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Retrying…")).toBeTruthy();
    view.rerender(<BillingPhonePage {...view.props} balanceKnown balanceFailed={false} />);
    expect(screen.queryByRole("button", { name: "Retry balance" })).toBeNull();
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("keeps and labels the last known amount after refresh failure", () => {
    renderPage({ balanceFailed: true });
    expect(screen.getByText("$5.24")).toBeTruthy();
    expect(screen.getByText("Could not refresh your balance. Showing the last known amount.")).toBeTruthy();
    expect(screen.getByText("Unavailable")).toBeTruthy();
    expect(screen.queryByText("Active")).toBeNull();
  });
});
