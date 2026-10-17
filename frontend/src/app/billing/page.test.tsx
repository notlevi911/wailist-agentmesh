import { afterEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";

const state = vi.hoisted(() => ({
  native: false,
  balanceKnown: true,
  balanceLoading: false,
  balanceFailed: false,
  readOnly: false,
  openExternal: vi.fn<
    (url: string, options?: { onClose?: () => void }) => Promise<void>
  >(async () => {}),
  refreshBalance: vi.fn(async () => {}),
  refreshPurchases: vi.fn(async () => {}),
}));

vi.mock("@/lib/nativeAuth", () => ({
  get IS_NATIVE() {
    return state.native;
  },
}));
vi.mock("@/lib/openExternal", () => ({
  WEB_BILLING_URL: "https://www.agent-mesh.app/billing",
  openExternal: state.openExternal,
}));
vi.mock("@/hooks/useReadOnly", () => ({
  useReadOnly: () => state.readOnly,
}));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({
    balanceUSD: 12,
    balanceKnown: state.balanceKnown,
    balanceLoading: state.balanceLoading,
    balanceFailed: state.balanceFailed,
    lastPurchase: undefined,
    refreshBalance: state.refreshBalance,
    refreshPurchases: state.refreshPurchases,
    // The phone screen only renders payment history once it has loaded,
    // so the mock has to supply one for Buy again to exist there.
    purchases: [
      {
        id: "p1",
        createdAt: "2026-08-03T10:00:00.000Z",
        amountINR: 500,
        creditsUSD: 5.24,
        method: "cashfree",
        status: "completed",
      },
    ],
    purchasesKnown: true,
  }),
}));
// The page now also reads the workflow list (for 30-day spend) and the
// live FX rate (so the quote matches what the ledger will credit).
vi.mock("@/lib/api", () => ({
  credits: {},
  workflows: { list: vi.fn(async () => []) },
  payments: {
    listProviders: vi.fn(async () => ({
      usd_per_inr: 0.010423,
      providers: [{ id: "cashfree", enabled: true, currency: "INR" }],
    })),
  },
}));
vi.mock("@/components/Topbar", () => ({ Topbar: () => null }));
vi.mock("@/components/checkout/CheckoutModal", () => ({
  CheckoutModal: () => <div>checkout dialog</div>,
}));
vi.mock("@/components/billing/PurchaseHistory", () => ({
  PurchaseHistory: ({
    onBuyAgain,
  }: {
    onBuyAgain: (amountINR: number) => void;
  }) => (
    <button type="button" onClick={() => onBuyAgain(1000)}>
      Buy again
    </button>
  ),
}));

import BillingPage from "./page";
import { workflows as workflowsApi } from "@/lib/api";

afterEach(() => {
  cleanup();
  state.native = false;
  state.balanceKnown = true;
  state.balanceLoading = false;
  state.balanceFailed = false;
  state.readOnly = false;
  state.openExternal.mockClear();
  state.refreshBalance.mockClear();
  state.refreshPurchases.mockClear();
});

// The app does not take payment itself: in-app checkout needs its own
// payment-provider project. So it pays on the website, in an in-app browser
// tab, and re-reads the balance and history when that tab closes.
describe("BillingPage in the Android app", () => {
  // ListWorkflows now answers successfully with statsUnavailable when its
  // aggregation fails, and every omitted spend sums to zero. Committing that
  // put "$0.00" on the 30-day figure for a total nobody had computed.
  it("leaves the 30-day spend unknown when the list could not total it", async () => {
    state.native = true;
    state.readOnly = true;
    vi.mocked(workflowsApi.list).mockResolvedValueOnce([
      { id: "wf-1", name: "A", nodes: [], edges: [], statsUnavailable: true },
      { id: "wf-2", name: "B", nodes: [], edges: [], statsUnavailable: true },
    ]);
    render(<BillingPage />);
    await act(async () => {});

    const spent = screen.getByText("Spent · 30d").nextElementSibling;
    expect(spent?.textContent).toBe("-");
    expect(spent?.textContent).not.toContain("$0");
  });

  it("shows the 30-day spend when the list did total it", async () => {
    state.native = true;
    state.readOnly = true;
    vi.mocked(workflowsApi.list).mockResolvedValueOnce([
      { id: "wf-1", name: "A", nodes: [], edges: [], spend: "3.50" },
      { id: "wf-2", name: "B", nodes: [], edges: [] },
    ]);
    render(<BillingPage />);
    await act(async () => {});

    expect(
      screen.getByText("Spent · 30d").nextElementSibling?.textContent,
    ).toContain("3.50");
  });

  it("tops up on the website and re-reads credits when the tab closes", () => {
    state.native = true;
    state.readOnly = true;
    render(<BillingPage />);

    // No amount picker and no Pay: the amount is chosen on the site.
    expect(screen.queryByRole("button", { name: /^Pay / })).toBeNull();
    expect(screen.queryByLabelText("Amount in rupees")).toBeNull();

    fireEvent.click(
      screen.getByRole("button", { name: /Add credits on the website/ }),
    );

    expect(state.openExternal).toHaveBeenCalledTimes(1);
    const [url, options] = state.openExternal.mock.calls[0];
    expect(url).toBe("https://www.agent-mesh.app/billing");
    expect(screen.queryByText("checkout dialog")).toBeNull();

    options?.onClose?.();
    expect(state.refreshBalance).toHaveBeenCalled();
    expect(state.refreshPurchases).toHaveBeenCalled();
  });

  it("sends Buy again to the website instead of the checkout dialog", () => {
    state.native = true;
    state.readOnly = true;
    render(<BillingPage />);

    fireEvent.click(screen.getByRole("button", { name: "Buy again" }));

    expect(state.openExternal).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("checkout dialog")).toBeNull();
  });
});

// A phone browser can take payment, so it keeps the checkout on the page.
describe("BillingPage on a phone browser", () => {
  it("checks out on the page, and never opens the website", () => {
    state.readOnly = true;
    render(<BillingPage />);

    expect(
      screen.queryByRole("button", { name: /Add credits on the website/ }),
    ).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^Pay / }));
    expect(screen.getByText("checkout dialog")).toBeTruthy();
    expect(state.openExternal).not.toHaveBeenCalled();
  });

  it("sends Buy again to the checkout dialog", () => {
    state.readOnly = true;
    render(<BillingPage />);

    fireEvent.click(screen.getByRole("button", { name: "Buy again" }));
    expect(screen.getByText("checkout dialog")).toBeTruthy();
    expect(state.openExternal).not.toHaveBeenCalled();
  });

  // Buy again sets the amount. The field, the chosen segment and the Pay
  // button must all say the same figure afterwards; the field used to keep
  // showing the old preset.
  it("shows Buy again's amount in the field and on Pay", () => {
    state.readOnly = true;
    render(<BillingPage />);

    fireEvent.click(screen.getByRole("button", { name: "Buy again" }));

    expect(screen.getByLabelText("Amount in rupees")).toHaveProperty(
      "value",
      "1000",
    );
    expect(screen.getByRole("button", { name: /^Pay ₹1,000$/ })).toBeTruthy();
    expect(
      screen
        .getByRole("radio", { name: "₹1,000" })
        .getAttribute("aria-checked"),
    ).toBe("true");
  });

  // Choosing a preset has to fill the field, not clear it. Cleared, the amount
  // showed only as the input's placeholder -- painted in --fg-dim, so the
  // figure about to be charged read as a suggestion rather than a value.
  it("fills the amount field from the chosen preset", () => {
    state.readOnly = true;
    render(<BillingPage />);

    fireEvent.click(screen.getByRole("radio", { name: "₹1,000" }));

    expect(screen.getByLabelText("Amount in rupees")).toHaveProperty(
      "value",
      "1000",
    );
    expect(screen.getByRole("button", { name: /^Pay ₹1,000$/ })).toBeTruthy();
  });

  // Without the guard "5,000" parses to 5 and the button offers to charge ₹5.
  // The keystroke is refused outright, so the field keeps what it had.
  it("refuses an amount that is not digits", () => {
    state.readOnly = true;
    render(<BillingPage />);

    const field = screen.getByLabelText("Amount in rupees");
    fireEvent.click(screen.getByRole("radio", { name: "₹1,000" }));
    fireEvent.change(field, { target: { value: "5,000" } });

    expect(field).toHaveProperty("value", "1000");
    expect(screen.getByRole("button", { name: /^Pay ₹1,000$/ })).toBeTruthy();
  });

  // Nothing is tapped: the field still has to open showing the amount.
  it("opens with the default amount already in the field", () => {
    state.readOnly = true;
    render(<BillingPage />);

    expect(screen.getByLabelText("Amount in rupees")).toHaveProperty(
      "value",
      "5000",
    );
  });
});

// The phone screen mounts PurchaseHistory only once the history is known, so
// the page has to ask for it; otherwise a fresh session never loads it.
describe("BillingPage history", () => {
  it("asks for the payment history on arrival", () => {
    state.readOnly = true;
    render(<BillingPage />);
    expect(state.refreshPurchases).toHaveBeenCalled();
  });
});

describe("BillingPage on the web", () => {
  it("keeps the amount picker, and Buy again opens checkout", () => {
    render(<BillingPage />);

    expect(screen.getByText("Choose an amount")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: /Add credits on the website/ }),
    ).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Buy again" }));

    expect(screen.getByText("checkout dialog")).toBeTruthy();
    expect(state.openExternal).not.toHaveBeenCalled();
  });
});


describe("desktop balance recovery", () => {
  it("offers an independent retry after first failure and disables it while loading", async () => {
    state.balanceKnown = false; state.balanceFailed = true;
    const view = render(<BillingPage />); await act(async () => {});
    expect(screen.getByText("Could not load your balance.")).toBeTruthy();
    expect(screen.getByText("Unavailable")).toBeTruthy();
    const historyCalls = state.refreshPurchases.mock.calls.length;
    const balanceCalls = state.refreshBalance.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Retry balance" }));
    expect(state.refreshBalance).toHaveBeenCalledTimes(balanceCalls + 1);
    expect(state.refreshPurchases).toHaveBeenCalledTimes(historyCalls);
    state.balanceLoading = true; view.rerender(<BillingPage />);
    expect((screen.getByRole("button", { name: "Retry balance" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Retrying…")).toBeTruthy();
    state.balanceLoading = false; state.balanceFailed = false; state.balanceKnown = true; view.rerender(<BillingPage />);
    expect(screen.queryByRole("button", { name: "Retry balance" })).toBeNull();
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("labels a retained amount as last known after a refresh failure", async () => {
    state.balanceFailed = true;
    render(<BillingPage />); await act(async () => {});
    expect(screen.getByText("$12.00")).toBeTruthy();
    expect(screen.getByText("Could not refresh your balance. Showing the last known amount.")).toBeTruthy();
    expect(screen.getByText("Unavailable")).toBeTruthy();
    expect(screen.queryByText("Active")).toBeNull();
  });
});
