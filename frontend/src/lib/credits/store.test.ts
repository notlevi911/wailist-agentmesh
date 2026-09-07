import { beforeEach, describe, expect, it, vi } from "vitest";

// `store.ts` keeps its wallet state in a module-level variable (mirroring a
// real singleton store), so each test needs a fresh module instance --
// otherwise state written by one test would leak into the next. Reset both
// the module registry and localStorage before every test, then re-import.
const STORAGE_KEY = "agentmesh_credits_v1";

const purchasesMock = vi.fn();
const balanceMock = vi.fn();

vi.mock("@/lib/api", () => ({
  credits: {
    purchases: (...args: unknown[]) => purchasesMock(...args),
    balance: (...args: unknown[]) => balanceMock(...args),
  },
}));

beforeEach(() => {
  window.localStorage.clear();
  vi.resetModules();
  purchasesMock.mockReset();
  balanceMock.mockReset();
  balanceMock.mockResolvedValue(0);
});

async function freshStore() {
  return await import("./store");
}

// One credit_ledger row as GET /credits/purchases returns it.
function ledgerRow(over: Record<string, unknown> = {}) {
  return {
    id: "row-1",
    provider: "cashfree",
    providerOrderId: "order-1",
    status: "completed",
    amountInrPaise: 50_000, // ₹500.00
    fxRateUsdPerInr: 0.012,
    creditUsdMicros: 6_000_000, // $6.00
    createdAt: "2026-09-01T10:00:00Z",
    ...over,
  };
}

describe("refreshPurchases", () => {
  it("maps stored units to display units", async () => {
    purchasesMock.mockResolvedValue([ledgerRow()]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();

    const { purchases } = readCredits();
    expect(purchases).toHaveLength(1);
    expect(purchases[0].amountINR).toBe(500);
    expect(purchases[0].creditsUSD).toBe(6);
    expect(purchases[0].status).toBe("completed");
  });

  // The whole point of the change: history is server-owned, so nothing about
  // it may survive in a browser that a different account later signs in to.
  // The whole point of the change: this store writes nothing to the browser,
  // so nothing about one account's money can survive for the next one.
  it("writes nothing to localStorage", async () => {
    purchasesMock.mockResolvedValue([ledgerRow(), ledgerRow({ id: "row-2" })]);
    const { refreshPurchases, refreshBalance } = await freshStore();
    await refreshPurchases();
    await refreshBalance();

    expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(window.localStorage.length).toBe(0);
  });

  // A blob written by the previous localStorage-backed build can still be
  // sitting in a returning user's browser. Reading any of it back would show
  // one account's receipts (and balance) to whoever signs in next on that
  // machine, so the store must ignore the key entirely.
  it("ignores state left behind by the older localStorage build", async () => {
    window.localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        balanceUSD: 42,
        purchases: [{ id: "stale", amountINR: 999, creditsUSD: 12 }],
        autoRecharge: { enabled: true, thresholdUSD: 2 },
      }),
    );

    const snap = (await freshStore()).readCredits();
    expect(snap.purchases).toEqual([]);
    expect(snap.balanceUSD).toBe(0);
  });

  it("keeps the last known list when the fetch fails", async () => {
    purchasesMock.mockResolvedValueOnce([ledgerRow()]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();
    expect(readCredits().purchases).toHaveLength(1);

    purchasesMock.mockRejectedValueOnce(new Error("offline"));
    await refreshPurchases();
    expect(readCredits().purchases).toHaveLength(1);
  });

  it("maps a crypto row to USD with no INR amount", async () => {
    purchasesMock.mockResolvedValue([
      ledgerRow({
        provider: "nowpayments",
        amountInrPaise: undefined,
        fxRateUsdPerInr: undefined,
        amountUsdCents: 1_500, // $15.00
      }),
    ]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();

    const p = readCredits().purchases[0];
    expect(p.amountINR).toBeUndefined();
    expect(p.amountUSD).toBe(15);
    expect(p.method).toBe("nowpayments");
  });

  // A gateway added backend-first must not render as "undefined" in the label
  // maps the UI keys by provider.
  it("falls back to a known method for an unrecognised provider", async () => {
    purchasesMock.mockResolvedValue([ledgerRow({ provider: "some-new-psp" })]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();
    expect(readCredits().purchases[0].method).toBe("cashfree");
  });

  it("preserves a non-completed status rather than assuming paid", async () => {
    purchasesMock.mockResolvedValue([ledgerRow({ status: "failed" })]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();
    expect(readCredits().purchases[0].status).toBe("failed");
  });
});

describe("legacy localStorage migration", () => {
  // The old build's keys are dead but not harmless: they hold one account's
  // purchase history and settlement rows in a browser someone else may sign
  // in to. Reading is already prevented; this clears them out for good.
  it("clears the old credits and settlements keys on first read", async () => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ balanceUSD: 9 }));
    window.localStorage.setItem("agentmesh_settlements_u1", "[]");
    window.localStorage.setItem("agentmesh_settlements_u2", "[]");
    window.localStorage.setItem("unrelated_key", "keep me");

    (await freshStore()).readCredits();

    expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(window.localStorage.getItem("agentmesh_settlements_u1")).toBeNull();
    expect(window.localStorage.getItem("agentmesh_settlements_u2")).toBeNull();
    // Only this store's own keys, never someone else's.
    expect(window.localStorage.getItem("unrelated_key")).toBe("keep me");
  });
});

describe("lastPurchase", () => {
  // Regression: rows now carry every credit_ledger status, so the newest row
  // is not necessarily a payment that landed. Offering "Repeat last top-up"
  // for a failed attempt labels it with money the user never spent.
  it("skips a failed row and picks the last completed one", async () => {
    purchasesMock.mockResolvedValue([
      ledgerRow({ id: "newest-failed", status: "failed", amountInrPaise: 200000 }),
      ledgerRow({ id: "older-ok", status: "completed", amountInrPaise: 50000 }),
    ]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();

    const rows = readCredits().purchases;
    const last = rows.find(
      (p) => p.status === "completed" && p.amountINR !== undefined,
    );
    expect(last?.id).toBe("older-ok");
    expect(last?.amountINR).toBe(500);
  });

  it("skips a completed crypto row, which has no INR amount to repeat", async () => {
    purchasesMock.mockResolvedValue([
      ledgerRow({
        id: "crypto",
        provider: "nowpayments",
        amountInrPaise: undefined,
        amountUsdCents: 2000,
      }),
      ledgerRow({ id: "inr", amountInrPaise: 70000 }),
    ]);
    const { refreshPurchases, readCredits } = await freshStore();
    await refreshPurchases();

    const last = readCredits().purchases.find(
      (p) => p.status === "completed" && p.amountINR !== undefined,
    );
    expect(last?.id).toBe("inr");
  });
});

describe("resetCredits", () => {
  // The leak this whole change removes from localStorage also exists in
  // memory: module state outlives a client-side route change, so signing in
  // as a second account in the same tab would show the first one's money.
  it("drops balance, history and the known flags", async () => {
    purchasesMock.mockResolvedValue([ledgerRow()]);
    balanceMock.mockResolvedValue(42);
    const {
      refreshPurchases,
      refreshBalance,
      resetCredits,
      readCredits,
      readCreditsFlags,
    } = await freshStore();
    await refreshPurchases();
    await refreshBalance();
    expect(readCredits().purchases).toHaveLength(1);
    expect(readCredits().balanceUSD).toBe(42);
    expect(readCreditsFlags()).toEqual({
      balanceKnown: true,
      purchasesKnown: true,
      purchasesFailed: false,
    });

    resetCredits();

    expect(readCredits().purchases).toEqual([]);
    expect(readCredits().balanceUSD).toBe(0);
    // The flags matter as much as the data: leaving purchasesKnown true makes
    // the next account's empty store render as "this account has no
    // purchases" rather than as still loading.
    expect(readCreditsFlags()).toEqual({
      balanceKnown: false,
      purchasesKnown: false,
      purchasesFailed: false,
    });
  });

  // resetCredits alone does not stop a request already in flight. Without an
  // epoch guard the in-flight response lands afterwards and writes the signed
  // -out account's rows straight back, with purchasesKnown set.
  it("discards a refresh that was already in flight when it ran", async () => {
    let release!: (rows: unknown[]) => void;
    purchasesMock.mockReturnValueOnce(
      new Promise((res) => {
        release = res as (rows: unknown[]) => void;
      }),
    );

    const { refreshPurchases, resetCredits, readCredits, readCreditsFlags } =
      await freshStore();

    const inFlight = refreshPurchases();
    resetCredits(); // user signs out mid-request
    release([ledgerRow()]); // account A's rows arrive too late
    await inFlight;

    expect(readCredits().purchases).toEqual([]);
    expect(readCreditsFlags().purchasesKnown).toBe(false);
  });

  it("discards an in-flight balance the same way", async () => {
    let release!: (v: number) => void;
    balanceMock.mockReturnValueOnce(
      new Promise((res) => {
        release = res as (v: number) => void;
      }),
    );

    const { refreshBalance, resetCredits, readCredits, readCreditsFlags } =
      await freshStore();

    const inFlight = refreshBalance();
    resetCredits();
    release(99);
    await inFlight;

    expect(readCredits().balanceUSD).toBe(0);
    expect(readCreditsFlags().balanceKnown).toBe(false);
  });
});

describe("refreshPurchases failure handling", () => {
  // A first fetch that fails used to leave purchasesKnown false forever, so a
  // panel gated on it rendered nothing at all -- a backend blip looked exactly
  // like an account that had never paid.
  it("flags the failure so the UI can distinguish it from an empty account", async () => {
    purchasesMock.mockRejectedValueOnce(new Error("offline"));
    const store = await freshStore();
    await store.refreshPurchases();

    expect(store.readCredits().purchases).toEqual([]);
    // The assertion that carries the behaviour: without purchasesFailed, a
    // panel gated on purchasesKnown renders nothing at all and a backend blip
    // is indistinguishable from an account that never paid.
    expect(store.readCreditsFlags()).toEqual({
      balanceKnown: false,
      purchasesKnown: false,
      purchasesFailed: true,
    });

    // And a later success clears it.
    purchasesMock.mockResolvedValueOnce([ledgerRow()]);
    await store.refreshPurchases();
    expect(store.readCredits().purchases).toHaveLength(1);
    expect(store.readCreditsFlags().purchasesFailed).toBe(false);
    expect(store.readCreditsFlags().purchasesKnown).toBe(true);
  });
});
