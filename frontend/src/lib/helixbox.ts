import { BASE, apiFetch } from "@/lib/api";
import { paymentRefusalCode, type PaymentRefusalCode } from "./paymentRefusal";

// HelixBox's x402 endpoints, as served by GET /helixbox/endpoints. The
// backend's internal/helixbox package is the single source of truth for all of
// this — the prices here are the same values the Bazaar card quotes and the
// console charges, which is why none of it is hardcoded on this side.

export interface HelixboxField {
  name: string;
  label: string;
  kind: "text";
  required: boolean;
  placeholder?: string;
  description?: string;
}

export interface HelixboxEndpoint {
  id: string;
  title: string;
  /** HelixBox's own sentence, verbatim from its 402 challenge. */
  description: string;
  /** AgentMesh's plain-language line about who this plan is for. */
  blurb: string;
  path: string;
  method: string;
  /** The vendor's price in atomic USDC. NOT the total — see totalCostMicros. */
  amountMicros: number;
  /** How long the session lasts. Matches the expiresIn the endpoint returns. */
  durationSeconds: number;
  /** The tier the session carries: "cli", "agent" or "premium". */
  accessLevel: string;
  /** The console form's inputs. Today: the CLI pairing code. */
  fields: HelixboxField[];
  /**
   * How this endpoint's request shape is known:
   * - "live"   a real paid call to this exact endpoint succeeded
   * - "source" transcribed from HelixBox's own published handler code
   *
   * "source" is not a hedge — it is stronger than their spec document and far
   * stronger than their 402 challenge, both of which were wrong about the
   * request body and cost two paid calls to disprove.
   */
  verified: "live" | "source";
}

export interface HelixboxSpec {
  provider: string;
  host: string;
  asset: string;
  /** AgentMesh's flat markup per x402 call, applied to every plan. */
  platformFeeUsdMicros: number;
  endpoints: HelixboxEndpoint[];
}

export interface HelixboxRunResult {
  endpoint: string;
  title: string;
  accessLevel: string;
  durationSeconds: number;
  response: unknown;
  /**
   * False when the call completed without any payment settling. That is not a
   * cheaper success: it means the endpoint did not answer with a payment
   * challenge, so nothing was paid and nothing was billed.
   */
  settled: boolean;
  settledUsdMicros: number;
  platformFeeUsdMicros: number;
  totalUsdMicros: number;
  txId?: string;
  explorerURL?: string;
  platformFeeTxId?: string;
  platformFeeExplorerURL?: string;
}

// What a plan really costs the user: HelixBox's price plus AgentMesh's flat
// per-call markup. For the two hourly plans the markup is the larger half of
// the total, so showing the vendor price alone would understate them 7x.
export function totalCostMicros(
  endpoint: Pick<HelixboxEndpoint, "amountMicros">,
  platformFeeUsdMicros: number,
): number {
  return endpoint.amountMicros + platformFeeUsdMicros;
}

// formatUsd renders atomic USDC (6 decimals) as a plain dollar figure.
export function formatUsd(micros: number): string {
  return `$${(micros / 1e6).toFixed(2)}`;
}

// formatDuration turns a session length into the words a buyer would use.
// Deliberately coarse: HelixBox sells round hours and whole weeks, and
// "1 hour" reads better on a price card than "3600 seconds" or "1h 0m".
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "";
  const units: Array<[number, string]> = [
    [604800, "week"],
    [86400, "day"],
    [3600, "hour"],
    [60, "minute"],
  ];
  for (const [size, name] of units) {
    if (seconds >= size && seconds % size === 0) {
      const n = seconds / size;
      return `${n} ${name}${n === 1 ? "" : "s"}`;
    }
  }
  // Not a round multiple of anything — fall back to the largest unit that
  // fits, rounded, rather than printing a bare second count.
  for (const [size, name] of units) {
    if (seconds >= size) {
      const n = Math.round(seconds / size);
      return `about ${n} ${name}${n === 1 ? "" : "s"}`;
    }
  }
  return `${seconds} seconds`;
}

// formatRemaining says how much of a bought session is left, from a wall-clock
// expiry. Returns null once it has run out, which the caller renders as an
// expired state rather than "0 minutes".
export function formatRemaining(expiresAt: number, now: number): string | null {
  const left = Math.floor((expiresAt - now) / 1000);
  if (left <= 0) return null;
  const days = Math.floor(left / 86400);
  const hours = Math.floor((left % 86400) / 3600);
  const minutes = Math.floor((left % 3600) / 60);
  if (days > 0) return hours > 0 ? `${days}d ${hours}h left` : `${days}d left`;
  if (hours > 0) return `${hours}h ${minutes}m left`;
  if (minutes > 0) return `${minutes}m left`;
  return "under a minute left";
}

export class HelixboxRunError extends Error {
  readonly status: number;
  readonly code: PaymentRefusalCode | null;
  constructor(message: string, status: number, code?: unknown) {
    super(message);
    this.name = "HelixboxRunError";
    this.status = status;
    this.code = paymentRefusalCode(status, code);
  }
}

// ── The record of what was bought ───────────────────────────────────────────
//
// HelixBox does not answer a question and does not mint a credential: it
// extends the paid time on a session the user already started with
// `npx helixbox-cli`. So what is worth keeping is the receipt — which pairing
// code was topped up, until when, and for how much.
//
// The backend does not store response bodies to hand back later, and a
// purchase that scrolls away is a purchase the user cannot prove. Per-browser
// (localStorage), because that is where the buyer is.

// Where the session record lives. Versioned so a shape change cannot make an
// old entry render as a broken card — bumped to v2 when the record stopped
// carrying a `token` (which never existed) and started carrying the `code` the
// purchase was applied to.
const STORE_KEY = "agentmesh_helixbox_sessions_v2";

/** Kept small on purpose: this is a receipt drawer, not a history feature. */
const MAX_STORED_SESSIONS = 12;

export interface HelixboxSession {
  /** Local id, so two purchases on one code cannot collide as a React key. */
  key: string;
  endpoint: string;
  title: string;
  accessLevel: string;
  /** The pairing code this purchase was applied to. */
  code: string;
  /** Wall-clock ms the session is paid until, as HelixBox reported it. */
  expiresAt: number;
  boughtAt: number;
  totalUsdMicros: number;
  txId?: string;
  explorerURL?: string;
}

/**
 * paidUntil reads the wall-clock time the purchase bought, in epoch ms.
 *
 * HelixBox replies {"code":"AfXavNVgBJ","expiresAt":1757320000000}. Note that
 * expiresAt is an ABSOLUTE timestamp in milliseconds, not the `expiresIn`
 * duration in seconds that HelixBox's spec document describes — the document
 * is wrong, and their handler (manager/src/x402-app.ts) is the authority.
 *
 * Both spellings are accepted anyway: a paid session whose expiry we failed to
 * read would be shown as expired, which is worse than being generous here.
 * Returns null when nothing usable came back.
 */
export function paidUntil(response: unknown, now: number): number | null {
  if (!response || typeof response !== "object") return null;
  const obj = response as Record<string, unknown>;

  // The real field: absolute epoch ms.
  const at = obj["expiresAt"] ?? obj["expires_at"];
  if (typeof at === "number" && Number.isFinite(at) && at > 0) {
    // Guard against a server that answers in SECONDS. Anything below this
    // threshold cannot be a millisecond timestamp in this decade, and reading
    // it as one would render a paid session as expired in 1970.
    return at < 1e11 ? at * 1000 : at;
  }

  // The spec document's shape, in case a future version really does send it.
  const inSeconds = obj["expiresIn"] ?? obj["expires_in"];
  if (typeof inSeconds === "number" && Number.isFinite(inSeconds) && inSeconds > 0) {
    return now + inSeconds * 1000;
  }
  return null;
}

/**
 * sessionCode reads back the pairing code the purchase was applied to.
 *
 * Echoed by HelixBox as `code`. Shown so the buyer can confirm the money
 * landed on the session they meant — the one thing they cannot check any other
 * way, since a mistyped code is accepted and silently creates a new one.
 */
export function sessionCode(response: unknown): string | null {
  if (!response || typeof response !== "object") return null;
  const v = (response as Record<string, unknown>)["code"];
  return typeof v === "string" && v.trim() !== "" ? v.trim() : null;
}

// Every read and write is wrapped: localStorage throws outright in some
// privacy modes (and is missing entirely in some test runtimes), and a console
// that white-screens because a receipt drawer could not be read would be a
// worse bug than the one this prevents.
//
// loadSessions reads storage directly, every time. The cached snapshot the UI
// subscribes to is getSessionsSnapshot below.
export function loadSessions(): HelixboxSession[] {
  try {
    const raw = localStorage.getItem(STORE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (s): s is HelixboxSession =>
        !!s &&
        typeof s === "object" &&
        typeof (s as HelixboxSession).code === "string" &&
        typeof (s as HelixboxSession).expiresAt === "number",
    );
  } catch {
    return [];
  }
}

export function saveSession(session: HelixboxSession): HelixboxSession[] {
  const next = [session, ...loadSessions()].slice(0, MAX_STORED_SESSIONS);
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify(next));
  } catch {
    // Out of quota, or storage is blocked. The token is still on screen in
    // the result panel — this only loses the ability to find it after a
    // refresh, which is worth failing quietly rather than losing the sale.
  }
  // Cached and emitted even when the write above failed: the purchase happened
  // and the drawer must show it for as long as this page is open, whether or
  // not it will survive a reload.
  sessionCache = next;
  emitSessions();
  return next;
}

export function forgetSession(key: string): HelixboxSession[] {
  const next = loadSessions().filter((s) => s.key !== key);
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify(next));
  } catch {
    // See saveSession.
  }
  sessionCache = next;
  emitSessions();
  return next;
}

// ── Subscribing to the drawer ───────────────────────────────────────────────
//
// The stored sessions are external state, so the console reads them through
// useSyncExternalStore rather than an effect that setStates on mount — the
// same idiom useReadOnly and useIsCompact use, and the one React's
// set-state-in-effect rule asks for.
//
// The snapshot has to be REFERENCE-stable between changes: getSnapshot runs on
// every render, and loadSessions() allocates a fresh array each call, which
// would loop forever. Hence the cache, invalidated only by a real write.

const EMPTY_SESSIONS: HelixboxSession[] = [];

let sessionCache: HelixboxSession[] | null = null;
const sessionListeners = new Set<() => void>();

function emitSessions() {
  for (const listener of sessionListeners) listener();
}

/** Re-read storage and tell every subscriber. */
export function refreshSessions(): HelixboxSession[] {
  sessionCache = loadSessions();
  emitSessions();
  return sessionCache;
}

export function getSessionsSnapshot(): HelixboxSession[] {
  if (sessionCache === null) sessionCache = loadSessions();
  return sessionCache;
}

// The server has no browser storage to read, and must not guess: rendering
// session cards into the SSR markup that the client then removes would flash
// somebody else's — or nobody's — credentials. An empty drawer is the honest
// server answer, and the client fills it in on hydration.
export function getServerSessionsSnapshot(): HelixboxSession[] {
  return EMPTY_SESSIONS;
}

// One window listener for the module's lifetime, attached on first subscribe
// rather than per subscriber, so N mounted components do not mean N refreshes
// per storage event. Never removed, which costs one closure and keeps the
// bookkeeping honest.
let storageListenerAttached = false;

function ensureStorageListener() {
  if (storageListenerAttached || typeof window === "undefined") return;
  storageListenerAttached = true;
  window.addEventListener("storage", (e: StorageEvent) => {
    // key === null means the whole store was cleared.
    if (e.key === null || e.key === STORE_KEY) refreshSessions();
  });
}

export function subscribeSessions(onChange: () => void): () => void {
  ensureStorageListener();
  sessionListeners.add(onChange);
  return () => {
    sessionListeners.delete(onChange);
  };
}

export const helixbox = {
  async spec(): Promise<HelixboxSpec> {
    const res = await apiFetch(`${BASE}/helixbox/endpoints`, {
      credentials: "include",
    });
    if (!res.ok) throw new Error(`endpoints: ${res.status}`);
    return (await res.json()) as HelixboxSpec;
  },

  async run(
    endpoint: string,
    fields: Record<string, string>,
  ): Promise<HelixboxRunResult> {
    const res = await apiFetch(`${BASE}/helixbox/run`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ endpoint, fields }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new HelixboxRunError(data.error ?? `run: ${res.status}`, res.status, data.code);
    }
    return data as HelixboxRunResult;
  },
};
