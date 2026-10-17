import { beforeEach, describe, expect, it, vi } from "vitest";

const balance = vi.fn();
vi.mock("@/lib/api", () => ({ credits: { balance: () => balance(), purchases: async () => [] } }));
beforeEach(() => { vi.resetModules(); balance.mockReset(); });
function pending() {
  let resolve!: (value: number) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<number>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe("balance failure recovery", () => {
  it("distinguishes first failure, pending retry and a confirmed zero balance", async () => {
    balance.mockRejectedValueOnce(new Error("offline"));
    const store = await import("./store");
    const first = store.refreshBalance();
    expect(store.readCreditsFlags()).toMatchObject({ balanceLoading: true, balanceKnown: false, balanceFailed: false });
    await first;
    expect(store.readCreditsFlags()).toMatchObject({ balanceLoading: false, balanceKnown: false, balanceFailed: true });
    const retry = pending(); balance.mockReturnValueOnce(retry.promise);
    const refresh = store.refreshBalance();
    expect(store.readCreditsFlags()).toMatchObject({ balanceLoading: true, balanceKnown: false, balanceFailed: true });
    retry.resolve(0); await refresh;
    expect(store.readCreditsFlags()).toMatchObject({ balanceLoading: false, balanceKnown: true, balanceFailed: false });
    expect(store.readCredits().balanceUSD).toBe(0);
  });

  it("preserves the last known amount and purchase state after a failed refresh", async () => {
    balance.mockResolvedValueOnce(12.5).mockRejectedValueOnce(new Error("offline"));
    const store = await import("./store");
    await store.refreshPurchases(); await store.refreshBalance(); await store.refreshBalance();
    expect(store.readCredits().balanceUSD).toBe(12.5);
    expect(store.readCreditsFlags()).toMatchObject({ balanceKnown: true, balanceFailed: true, balanceLoading: false, purchasesKnown: true, purchasesFailed: false });
  });

  it.each(["resolve", "reject"] as const)("ignores a late %s after signing out and starting a new account request", async (outcome) => {
    const old = pending(); const current = pending();
    balance.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const store = await import("./store");
    const oldRefresh = store.refreshBalance(); store.resetCredits();
    expect(store.readCreditsFlags()).toMatchObject({ balanceKnown: false, balanceFailed: false, balanceLoading: false });
    const currentRefresh = store.refreshBalance();
    if (outcome === "resolve") old.resolve(99); else old.reject(new Error("old account offline"));
    await oldRefresh;
    expect(store.readCredits().balanceUSD).toBe(0);
    expect(store.readCreditsFlags()).toMatchObject({ balanceKnown: false, balanceFailed: false, balanceLoading: true });
    current.resolve(3); await currentRefresh;
    expect(store.readCredits().balanceUSD).toBe(3);
    expect(store.readCreditsFlags()).toMatchObject({ balanceKnown: true, balanceFailed: false, balanceLoading: false });
  });

  it.each(["resolve", "reject"] as const)("ignores an older same-account %s after a newer refresh succeeds", async (outcome) => {
    const old = pending(); balance.mockReturnValueOnce(old.promise).mockResolvedValueOnce(8);
    const store = await import("./store");
    const first = store.refreshBalance(); await store.refreshBalance();
    if (outcome === "resolve") old.resolve(1); else old.reject(new Error("late failure"));
    await first;
    expect(store.readCredits().balanceUSD).toBe(8);
    expect(store.readCreditsFlags()).toMatchObject({ balanceKnown: true, balanceFailed: false, balanceLoading: false });
  });
});
