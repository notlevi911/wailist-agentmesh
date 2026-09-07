"use client";
import { useSyncExternalStore } from "react";
import type {
  CreditsState,
  Purchase,
  PurchaseStatus,
} from "@/lib/credits/types";
import type { PaymentMethod } from "@/components/checkout/types";
import { credits as creditsApi, type PurchaseRecord } from "@/lib/api";

// Credit wallet state, shared across routes via useSyncExternalStore.
//
// Nothing here is persisted to the browser. Both fields are read back from the
// server, and a local copy of either was a bug:
//
//   - balanceUSD comes from GET /credits/balance
//     (users.credit_balance_usd_micros), the same row the engine reserves
//     against and debits on every paid x402 call. A cached copy went stale the
//     instant a run spent money, showing a healthy balance for an account the
//     backend considered empty.
//   - purchases comes from GET /credits/purchases (credit_ledger), the rows the
//     Cashfree and NOWPayments webhooks write and settle. Kept in localStorage
//     this was per-browser: signing out, or signing in as a different account
//     on the same machine, showed history belonging to neither the session nor
//     the money that actually moved.
//
// An `autoRecharge` config object used to be persisted here too. It was never
// wired to a settings screen or a scheduled charge — only its threshold was
// read, as a low-balance warning — so it collapsed to the
// LOW_BALANCE_THRESHOLD_USD constant in fx.ts. Building the real feature is
// tracked in #164.

const DEFAULT_STATE: CreditsState = {
  balanceUSD: 0,
  purchases: [],
};

// Keys the previous localStorage-backed build wrote. Nothing reads them any
// more, but a returning user's browser still holds one account's purchase
// history and settlement rows, so the migration clears them once on load
// rather than leaving stale money data sitting there indefinitely.
// LEGACY_SETTLEMENTS_PREFIX matched a per-user key (`..._<userId>`), so its
// leftovers are found by prefix rather than by exact name.
const LEGACY_CREDITS_KEY = "agentmesh_credits_v1";
const LEGACY_SETTLEMENTS_PREFIX = "agentmesh_settlements_";

function clearLegacyStorage(): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.removeItem(LEGACY_CREDITS_KEY);
    // Collect first, then remove: deleting while iterating by index shifts
    // every later key down and skips half of them.
    const stale: string[] = [];
    for (let i = 0; i < window.localStorage.length; i++) {
      const k = window.localStorage.key(i);
      if (k?.startsWith(LEGACY_SETTLEMENTS_PREFIX)) stale.push(k);
    }
    stale.forEach((k) => window.localStorage.removeItem(k));
  } catch {
    /* private mode or blocked storage: nothing to clean up that we can reach */
  }
}

let state: CreditsState = DEFAULT_STATE;
const listeners = new Set<() => void>();

// commit takes an updater rather than a finished state so a caller can never
// write back a `state` it captured earlier, clobbering a concurrent refresh of
// the other field (balance and purchases are fetched independently).
function commit(update: (prev: CreditsState) => CreditsState): void {
  state = update(state);
  listeners.forEach((l) => l());
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

// One-shot, on the first client read. Not a module-level side effect: this
// module is imported during the server render too, where there is no storage
// to clean and the work would just be skipped every time.
let legacyCleared = false;

function getSnapshot(): CreditsState {
  if (!legacyCleared && typeof window !== "undefined") {
    legacyCleared = true;
    clearLegacyStorage();
  }
  return state;
}

// The server render has asked for nothing yet, so it renders the same defaults
// the client starts from — the two must agree or React reports a mismatch.
function getServerSnapshot(): CreditsState {
  return DEFAULT_STATE;
}

// readCredits is the current state without subscribing — for callers outside a
// React render (and for tests, which drive the module singleton directly).
// Components should use useCredits so they re-render when it changes.
export function readCredits(): CreditsState {
  return getSnapshot();
}

// KNOWN_METHODS bounds credit_ledger.provider to the PaymentMethod union the
// UI's label maps are keyed by. An unrecognised provider (a gateway added
// backend-first) falls back to the Cashfree label rather than indexing those
// maps with a string they have no entry for, which renders as "undefined".
const KNOWN_METHODS = new Set<string>([
  "cashfree",
  "nowpayments",
  "paypal",
  "stripe",
]);

const KNOWN_STATUSES = new Set<string>([
  "pending",
  "completed",
  "failed",
  "expired",
  "partial",
  "refunded",
]);

// toPurchase maps one credit_ledger row to the shape the billing UI renders.
// Stored units are converted exactly once, here: paise and cents are integers
// in the database precisely so no rounding happens before this point.
function toPurchase(r: PurchaseRecord): Purchase {
  return {
    id: r.id,
    createdAt: r.createdAt,
    amountINR:
      typeof r.amountInrPaise === "number" ? r.amountInrPaise / 100 : undefined,
    amountUSD:
      typeof r.amountUsdCents === "number" ? r.amountUsdCents / 100 : undefined,
    creditsUSD: r.creditUsdMicros / 1e6,
    method: (KNOWN_METHODS.has(r.provider)
      ? r.provider
      : "cashfree") as PaymentMethod,
    status: (KNOWN_STATUSES.has(r.status)
      ? r.status
      : "pending") as PurchaseStatus,
  };
}

// purchasesKnown mirrors balanceKnown: it separates "this account has never
// topped up" from "we have not asked yet", so the history panel can show an
// empty state only once the server has actually answered.
let purchasesKnown = false;

// purchasesFailed is the third state the pair above cannot express: not
// "loading", not "answered", but "we asked and could not find out". The UI
// needs it to offer a retry rather than silently showing an empty page.
let purchasesFailed = false;

// epoch identifies the account session a fetch was started under. Every
// refresh captures it and drops its result if it no longer matches.
//
// Without it, resetCredits() on sign-out does not stop a request already in
// flight: /billing fires refreshBalance and refreshPurchases on mount, and a
// sign-out a moment later leaves those responses to land afterwards and write
// the PREVIOUS account's balance and rows straight back — setting
// purchasesKnown along the way, so they render as settled fact. That is the
// leak resetCredits exists to close, just moved later in the timeline.
//
// It also settles a quieter race: two overlapping refreshes of the same field
// where the slower, older response would otherwise win.
let epoch = 0;

// refreshPurchases re-reads top-up history. Call it on mount and after
// anything that creates or settles a payment.
export async function refreshPurchases(): Promise<void> {
  const started = epoch;
  try {
    const rows = await creditsApi.purchases();
    // Someone signed out (or reset the store) while this was in flight; these
    // rows belong to an account that is no longer signed in.
    if (started !== epoch) return;
    purchasesFailed = false;
    purchasesKnown = true;
    commit((prev) => ({ ...prev, purchases: rows.map(toPurchase) }));
  } catch {
    // Leave the last known list in place; a failed poll is not evidence the
    // history changed, and blanking it would look like receipts disappeared.
    //
    // But record the failure. Without it a first fetch that fails leaves
    // purchasesKnown false forever, and a panel that waits on it renders
    // nothing at all -- no heading, no error, no retry -- so a backend blip
    // looks identical to an account that has never paid.
    if (started !== epoch) return;
    purchasesFailed = true;
    listeners.forEach((l) => l());
  }
}

// balanceKnown distinguishes "the server says $0" from "we have not asked
// yet" — without it a real empty balance and an unloaded one look identical,
// and the UI would flash $0.00 on every page load.
let balanceKnown = false;

// refreshBalance re-reads the authoritative balance. Call it on mount and
// after anything that moves money (a completed run, a verified top-up).
export async function refreshBalance(): Promise<void> {
  const started = epoch;
  try {
    const balanceUSD = await creditsApi.balance();
    // See refreshPurchases: a balance that arrives after a sign-out belongs to
    // the account that just left.
    if (started !== epoch) return;
    balanceKnown = true;
    commit((prev) => ({ ...prev, balanceUSD }));
  } catch {
    // Leave the last known value in place; a failed poll is not evidence the
    // balance changed, and blanking it would look like funds vanished.
  }
}

// resetCredits drops everything this module holds. It MUST run on sign-out.
//
// state, balanceKnown and purchasesKnown are module singletons that outlive a
// client-side route change, so without this a second account signing in in the
// same tab renders the first account's balance and purchase rows — with
// purchasesKnown already true, so they show as settled fact — until the new
// fetch resolves. That is the same cross-account leak this module removed from
// localStorage, moved into memory and shortened to one round-trip.
export function resetCredits(): void {
  // Bump first: anything already in flight is now stale and must not write
  // its result back after this returns.
  epoch++;
  balanceKnown = false;
  purchasesKnown = false;
  purchasesFailed = false;
  commit(() => DEFAULT_STATE);
}

// readCreditsFlags exposes the load/failure flags outside a React render.
// readCredits deliberately returns only CreditsState, so without this the
// flags are invisible to any non-component caller — including tests, which
// otherwise cannot tell a real reset from one that forgot to clear them.
export function readCreditsFlags(): {
  balanceKnown: boolean;
  purchasesKnown: boolean;
  purchasesFailed: boolean;
} {
  return { balanceKnown, purchasesKnown, purchasesFailed };
}

// recordPurchase is called after a payment the client believes succeeded. It
// writes nothing locally — credit_ledger is the record, and the webhook or the
// verify call is what settles the row. This just re-reads both server-owned
// values so the UI reflects what actually landed.
//
// A just-verified payment can still be 'pending' for the moment it takes the
// gateway's webhook to arrive, which is why the history renders status rather
// than assuming every row is paid.
export async function recordPurchase(): Promise<void> {
  await Promise.all([refreshBalance(), refreshPurchases()]);
}

export interface CreditsSnapshot extends CreditsState {
  balanceKnown: boolean;
  purchasesKnown: boolean;
  purchasesFailed: boolean;
  refreshBalance: typeof refreshBalance;
  refreshPurchases: typeof refreshPurchases;
  lastPurchase: Purchase | undefined;
  recordPurchase: typeof recordPurchase;
}

export function useCredits(): CreditsSnapshot {
  const snapshot = useSyncExternalStore(
    subscribe,
    getSnapshot,
    getServerSnapshot,
  );
  const known = useSyncExternalStore(
    subscribe,
    () => balanceKnown,
    () => false,
  );
  const purchasesLoaded = useSyncExternalStore(
    subscribe,
    () => purchasesKnown,
    () => false,
  );
  const purchasesErrored = useSyncExternalStore(
    subscribe,
    () => purchasesFailed,
    () => false,
  );
  return {
    ...snapshot,
    balanceKnown: known,
    purchasesKnown: purchasesLoaded,
    purchasesFailed: purchasesErrored,
    refreshBalance,
    refreshPurchases,
    // "Repeat last top-up" needs an amount to repeat, so the most recent
    // COMPLETED row with an INR amount wins. Both filters matter: a crypto
    // top-up has no INR figure the checkout could be reopened with, and a
    // failed or expired attempt is not a purchase to offer repeating — the
    // button's own label would assert a payment that never landed.
    lastPurchase: snapshot.purchases.find(
      (p) => p.status === "completed" && p.amountINR !== undefined,
    ),
    recordPurchase,
  };
}
