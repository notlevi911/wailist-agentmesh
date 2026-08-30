// TODO: Replace all stubs with real FastAPI calls when backend is ready.
// Base URL will come from env: process.env.NEXT_PUBLIC_API_URL

import {
  Workflow,
  UsageRange,
  UsageSummary,
  UsagePoint,
  WorkflowSpend,
  EndpointUsage,
  Settlement,
} from "./types";
import { WORKFLOWS, SAMPLE_WORKFLOW, buildUsage } from "./data";

// In the browser, always route through /api so the cookie stays same-site.
// NEXT_PUBLIC_API_URL still controls mock vs real (empty = mock data).
const _CONFIGURED = process.env.NEXT_PUBLIC_API_URL ?? "";
const BASE =
  _CONFIGURED && typeof window !== "undefined" ? "/api" : _CONFIGURED;

// -- Auth ------------------------------------------------------------------
export interface AuthUser {
  id: string;
  email: string;
  name: string;
  orgName: string;
  // True for an OAuth account that has never set a name/org — Google and
  // GitHub only hand back a verified email, not an organization.
  needsOnboarding: boolean;
}

export const auth = {
  signIn: async (email: string, password: string): Promise<void> => {
    if (BASE) {
      const res = await fetch(`${BASE}/auth/signin`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "sign in failed");
      return;
    }
    void email;
    void password;
    await delay(400);
  },

  signUp: async (
    email: string,
    password: string,
    name: string,
    org: string,
  ): Promise<void> => {
    if (BASE) {
      const res = await fetch(`${BASE}/auth/signup`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password, name, org }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "sign up failed");
      return;
    }
    void email;
    void password;
    void name;
    void org;
    await delay(500);
  },

  me: async (): Promise<AuthUser> => {
    if (BASE) {
      const res = await fetch(`${BASE}/auth/me`, { credentials: "include" });
      if (!res.ok) throw new Error("unauthorized");
      return res.json();
    }
    return {
      id: "dev",
      email: "dev@local",
      name: "Dev",
      orgName: "Acme Capital",
      needsOnboarding: false,
    };
  },

  // Sets name/org for the signed-in user. Used by the post-OAuth onboarding
  // prompt (OAuth accounts start with no name/org), and reusable as a
  // general profile edit.
  updateProfile: async (name: string, orgName: string): Promise<AuthUser> => {
    if (BASE) {
      const res = await fetch(`${BASE}/auth/me`, {
        method: "PATCH",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, orgName }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "could not update profile");
      return data;
    }
    return {
      id: "dev",
      email: "dev@local",
      name,
      orgName,
      needsOnboarding: false,
    };
  },

  signOut: async (): Promise<void> => {
    if (BASE) {
      await fetch(`${BASE}/auth/signout`, {
        method: "POST",
        credentials: "include",
      });
      return;
    }
    await delay(100);
  },

  // Full URL to kick off a backend OAuth flow. Empty string when no backend
  // is configured (mock mode) — callers should guard on the http prefix.
  oauthURL: (provider: "github" | "google"): string =>
    BASE ? `${BASE}/auth/oauth/${provider}` : "",
};

// -- Workflows ------------------------------------------------------------
export const workflows = {
  // TODO: GET /workflows
  list: async (): Promise<Workflow[]> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows`, { credentials: "include" });
      if (!res.ok) throw new Error("workflows fetch failed");
      return res.json();
    }
    await delay(200);
    return WORKFLOWS;
  },

  // TODO: GET /workflows/:id
  get: async (id: string): Promise<Workflow> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows/${id}`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "workflow fetch failed");
      return data;
    }
    await delay(150);
    if (id === "new")
      return { id: "wf-new", name: "Untitled workflow", nodes: [], edges: [] };
    return JSON.parse(JSON.stringify(SAMPLE_WORKFLOW));
  },

  // TODO: POST /workflows
  create: async (name: string): Promise<Workflow> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "workflow create failed");
      return data;
    }
    await delay(300);
    return { id: `wf-${Date.now()}`, name, nodes: [], edges: [] };
  },

  // TODO: PUT /workflows/:id
  update: async (id: string, wf: Partial<Workflow>): Promise<Workflow> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows/${id}`, {
        method: "PUT",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(wf),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "workflow update failed");
      return data;
    }
    await delay(200);
    return {
      id,
      name: wf.name ?? "Untitled",
      nodes: wf.nodes ?? [],
      edges: wf.edges ?? [],
    };
  },

  // TODO: POST /workflows/:id/deploy
  deploy: async (
    id: string,
  ): Promise<{
    agents: { nodeId: string; address: string; network: string }[];
  }> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows/${id}/deploy`, {
        method: "POST",
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "deploy failed");
      return data;
    }
    await delay(800);
    return { agents: [] };
  },

  // TODO: POST /workflows/:id/run
  run: async (
    id: string,
    input?: Record<string, unknown>,
  ): Promise<{ runId: string }> => {
    if (BASE) {
      const res = await fetch(`${BASE}/workflows/${id}/run`, {
        method: "POST",
        credentials: "include",
        headers: input ? { "Content-Type": "application/json" } : {},
        body: input ? JSON.stringify(input) : undefined,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "run failed");
      return data;
    }
    await delay(200);
    return { runId: `r-${Math.floor(1800 + Math.random() * 200)}` };
  },

  // TODO: POST /workflows/:id/stop
  stop: async (id: string): Promise<void> => {
    if (BASE) {
      await fetch(`${BASE}/workflows/${id}/stop`, {
        method: "POST",
        credentials: "include",
      });
      return;
    }
    await delay(100);
  },

  // Persistent per-workflow key/value state, surviving across runs. Used
  // for incremental sync cursors, counters, and cached tokens.
  variables: {
    list: async (id: string): Promise<Record<string, unknown>> => {
      if (BASE) {
        const res = await fetch(`${BASE}/workflows/${id}/variables`, {
          credentials: "include",
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error ?? "failed to load variables");
        return data.variables ?? {};
      }
      await delay(120);
      return {};
    },
    set: async (id: string, key: string, value: unknown): Promise<void> => {
      if (BASE) {
        const res = await fetch(
          `${BASE}/workflows/${id}/variables/${encodeURIComponent(key)}`,
          {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            credentials: "include",
            body: JSON.stringify({ value }),
          },
        );
        if (!res.ok) {
          const data = await res.json().catch(() => ({}));
          throw new Error(data.error ?? "failed to save variable");
        }
        return;
      }
      await delay(120);
    },
    remove: async (id: string, key: string): Promise<void> => {
      if (BASE) {
        const res = await fetch(
          `${BASE}/workflows/${id}/variables/${encodeURIComponent(key)}`,
          { method: "DELETE", credentials: "include" },
        );
        if (!res.ok && res.status !== 204) {
          const data = await res.json().catch(() => ({}));
          throw new Error(data.error ?? "failed to delete variable");
        }
        return;
      }
      await delay(120);
    },
  },
};

// -- Credits ----------------------------------------------------------------
export const credits = {
  // The authoritative balance: users.credit_balance_usd_micros, the same row
  // the engine reserves against and debits on every paid call. Anything shown
  // from another source is a guess that drifts the moment a run spends money.
  balance: async (): Promise<number> => {
    if (BASE) {
      const res = await fetch(`${BASE}/credits/balance`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "balance fetch failed");
      return (data.credit_usd_micros ?? 0) / 1e6;
    }
    await delay(120);
    return 0;
  },

  // Redeems a coupon code and returns the new balance. Throws with the
  // server's message (e.g. "invalid coupon code", "coupon already redeemed")
  // on failure so the caller can show it directly.
  redeemCoupon: async (code: string): Promise<number> => {
    if (BASE) {
      const res = await fetch(`${BASE}/credits/redeem-coupon`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "coupon redemption failed");
      return (data.credit_usd_micros ?? 0) / 1e6;
    }
    await delay(120);
    throw new Error("coupons aren't available in mock mode");
  },
};

// -- Runs -------------------------------------------------------------------
export interface RunLogRecord {
  id: string;
  runId: string;
  stepIndex: number;
  nodeId: string;
  nodeType: string;
  status: "pending" | "running" | "success" | "failed";
  output?: unknown;
  durationMs?: number;
  ts: string;
}

export const runs = {
  // The DB-backed source of truth for a run's logs — used as a reconciliation
  // fallback once the live SSE stream ends, since the stream's broker only
  // delivers events to clients subscribed at the exact moment they're
  // published (see sse/broker.go's non-blocking, unbuffered-per-subscriber
  // Publish): any run that finishes a step before/without a live subscriber
  // silently drops that step's event, with no replay. Polling this after
  // "done" (or a stream error) guarantees the console reflects what actually
  // happened server-side, not just whatever fraction of events the stream
  // happened to deliver live.
  get: async (
    runId: string,
  ): Promise<{ run: { status: string }; logs: RunLogRecord[] }> => {
    if (BASE) {
      const res = await fetch(`${BASE}/runs/${runId}`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "failed to fetch run");
      return data;
    }
    await delay(150);
    return { run: { status: "success" }, logs: [] };
  },
};

// -- Agents ---------------------------------------------------------------
export const agents = {
  // TODO: GET /workflows/:wfId/agents/:agentId/balance
  balance: async (
    wfId: string,
    agentId: string,
  ): Promise<{ address: string; balance: string; network: string }> => {
    if (BASE) {
      const res = await fetch(
        `${BASE}/workflows/${wfId}/agents/${agentId}/balance`,
        { credentials: "include" },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "balance fetch failed");
      return data;
    }
    await delay(300);
    return { address: "", balance: "0.000000", network: "testnet" };
  },

  // TODO: POST /workflows/:wfId/agents/:agentId/fund
  fund: async (
    wfId: string,
    agentId: string,
    amount: number,
  ): Promise<{ txHash: string; balance: string }> => {
    if (BASE) {
      const res = await fetch(
        `${BASE}/workflows/${wfId}/agents/${agentId}/fund`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ amount }),
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "fund failed");
      return data;
    }
    await delay(500);
    return {
      txHash: `0x${Math.random().toString(16).slice(2, 10)}`,
      balance: amount.toFixed(3),
    };
  },
};

// -- Tools ----------------------------------------------------------------
export const tools = {
  x402quote: async (
    url: string,
  ): Promise<{
    price?: string;
    unit?: string;
    asset?: string;
    network?: string;
    recipient?: string;
    raw?: string;
    description?: string;
    // The HTTP method the target declares for itself, and whether its params
    // ride in the query string or the body — both read out of the endpoint's
    // own Bazaar extension, so the canvas configures an arbitrary endpoint
    // correctly without anyone hardcoding support for it.
    method?: string;
    paramsIn?: "query" | "body";
    params?: Array<{
      name: string;
      type: string;
      required: boolean;
      description: string;
      default?: string;
    }>;
  }> => {
    if (BASE) {
      const res = await fetch(`${BASE}/tools/x402/quote`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "quote failed");
      return data;
    }
    await delay(600);
    return {
      price: "0.002",
      unit: "call",
      network: "algorand-testnet",
      recipient: "",
    };
  },
};

// -- Waitlist -------------------------------------------------------------
export const waitlist = {
  // TODO: POST /waitlist
  join: async (email: string): Promise<void> => {
    if (BASE) {
      await fetch(`${BASE}/waitlist`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      return;
    }
    void email;
    await delay(600);
  },
};

// -- Payments ---------------------------------------------------------------
export const payments = {
  createCashfreeOrder: async (
    amountINRPaise: number,
    phone: string,
  ): Promise<{
    order_id: string;
    payment_session_id: string;
    amount: number;
    currency: string;
    app_id: string;
  }> => {
    if (!BASE) throw new Error("payments require a configured backend");
    const res = await fetch(`${BASE}/payments/cashfree/order`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ amount_inr_paise: amountINRPaise, phone }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? "order creation failed");
    return data;
  },

  verifyCashfreePayment: async (
    orderId: string,
  ): Promise<{ status: string; credited_usd_micros: number }> => {
    if (!BASE) throw new Error("payments require a configured backend");
    const res = await fetch(`${BASE}/payments/cashfree/verify`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ order_id: orderId }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? "payment verification failed");
    return data;
  },
};

// -- Usage & Credits ------------------------------------------------------
// Real endpoints don't exist yet (see plan §5 — needs a metering change in
// tool402.go + provider.go). Until then these return fixtures in mock mode,
// and in real mode call the proposed /usage/* routes once the backend adds them.
// Mock fixtures depend on Date.now(); memoize per range so every panel in a
// render shares one consistent payload instead of regenerating timestamps.
const _usageCache = new Map<UsageRange, ReturnType<typeof buildUsage>>();
function mockUsage(range: UsageRange): ReturnType<typeof buildUsage> {
  let u = _usageCache.get(range);
  if (!u) {
    u = buildUsage(range);
    _usageCache.set(range, u);
  }
  return u;
}

// Bucket granularity has to track the range the chart actually plots: 24h is
// charted as 24 hourly points, 7d/30d as daily ones. Hardcoding bucket=day
// would collapse 24h to a single point once the backend honours the param.
function bucketFor(range: UsageRange): "hour" | "day" {
  return range === "24h" ? "hour" : "day";
}

// One fetch/mock branch for every usage endpoint. Always reads the response
// body for a server-provided `error` message — before this was shared, only
// summary did, and the other four threw fixed strings that discarded detail.
async function usageFetch<T>(path: string, mock: () => T): Promise<T> {
  if (BASE) {
    const res = await fetch(`${BASE}${path}`, { credentials: "include" });
    const data = await res.json().catch(() => ({}));
    if (!res.ok)
      throw new Error(
        (data as { error?: string }).error ?? `usage request failed: ${path}`,
      );
    return data as T;
  }
  await delay(220);
  return mock();
}

export const usage = {
  // Drops the memoized mock payloads so the next fetch regenerates them.
  // Called by the retry action: without this, retry re-resolves from the cache
  // and looks like a no-op in mock mode. Harmless in real mode (the cache is
  // only read for fixtures).
  invalidate: (): void => {
    _usageCache.clear();
  },

  summary: (range: UsageRange): Promise<UsageSummary> =>
    usageFetch(`/usage/summary?range=${range}`, () => mockUsage(range).summary),

  timeseries: (range: UsageRange): Promise<UsagePoint[]> =>
    usageFetch(
      `/usage/timeseries?range=${range}&bucket=${bucketFor(range)}`,
      () => mockUsage(range).timeseries,
    ),

  byWorkflow: (range: UsageRange): Promise<WorkflowSpend[]> =>
    usageFetch(
      `/usage/by-workflow?range=${range}`,
      () => mockUsage(range).byWorkflow,
    ),

  byEndpoint: (range: UsageRange): Promise<EndpointUsage[]> =>
    usageFetch(
      `/usage/by-endpoint?range=${range}`,
      () => mockUsage(range).byEndpoint,
    ),

  // Settlements are the latest on-chain payments, not a range-scoped metric —
  // the real endpoint takes only `limit`, and the panel deliberately ignores
  // the 24h/7d/30d selector. Any range yields the same rows in mock mode, so
  // "30d" just picks a canonical memoized payload to slice from.
  settlements: (limit = 20): Promise<Settlement[]> =>
    usageFetch(`/usage/settlements?limit=${limit}`, () =>
      mockUsage("30d").settlements.slice(0, limit),
    ),
};

// -- Helpers --------------------------------------------------------------
function delay(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
