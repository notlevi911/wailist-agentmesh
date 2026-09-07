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
import { assertWritable } from "./readonly";
import { IS_NATIVE, authHeaders } from "./nativeAuth";
import type { PaymentMethod } from "@/components/checkout/types";

// In the browser, always route through /api so the cookie stays same-site.
// NEXT_PUBLIC_API_URL still controls mock vs real (empty = mock data).
// Exported so other modules (e.g. lib/bazaar.ts) share this one definition
// instead of re-deriving it and silently drifting out of sync.
const _CONFIGURED = process.env.NEXT_PUBLIC_API_URL ?? "";
// The browser routes through /api so the auth cookie stays same-site. The
// native shell cannot: its bundle is a static export served from the device,
// with no Next server behind it to proxy /api anywhere, and no same-site
// cookie to protect. It calls the backend's absolute URL and authenticates
// with a bearer token instead (see nativeAuth.ts).
export const BASE =
  !IS_NATIVE && _CONFIGURED && typeof window !== "undefined"
    ? "/api"
    : _CONFIGURED;

// apiFetch is the single place that knows how this client authenticates.
//
// Before this existed every call site spelled out `credentials: "include"`
// and nothing else, which was correct for exactly one kind of client. Routing
// them all through here means the native shell's bearer header is added once
// rather than at thirty call sites, and the web request is unchanged --
// authHeaders() returns nothing at all off-device.
// Exported so any module making its own calls to this backend -- lib/tendril.ts
// is the only one today -- goes through the same auth path instead of
// hand-rolling `fetch(..., { credentials: "include" })`, which silently drops
// the native bearer header and 401s on every native-app call.
export async function apiFetch(
  input: string,
  init: RequestInit = {},
): Promise<Response> {
  const extra = authHeaders();
  const headers = {
    ...(init.headers as Record<string, string> | undefined),
    ...extra,
  };
  return fetch(input, {
    ...init,
    // Deliberately AFTER `...init`, not before: every call site in this file
    // and lib/tendril.ts still passes its own `credentials: "include"` (predating
    // this native-aware default), which would otherwise silently win and
    // override this decision for every native call. apiFetch's whole reason to
    // exist is being the one place that knows how this client authenticates --
    // a caller's own guess must never be able to outrank it. The native client
    // authenticates with Authorization: Bearer and has no cookie to send;
    // asking for credentials anyway would oblige the server to answer a
    // non-wildcard Allow-Origin plus Allow-Credentials for no benefit, an extra
    // CORS constraint to get wrong, guarding nothing.
    credentials: IS_NATIVE ? "omit" : "include",
    ...(Object.keys(headers).length ? { headers } : {}),
  });
}

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
  // Returns the bearer token when the caller is a native client, null
  // otherwise. The web app authenticates with the HttpOnly cookie the same
  // response sets and simply ignores this -- see nativeAuth.ts for why a
  // browser is deliberately not handed a readable token.
  signIn: async (email: string, password: string): Promise<string | null> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/auth/signin`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "sign in failed");
      return data.token ?? null;
    }
    void email;
    void password;
    await delay(400);
    return null;
  },

  signUp: async (
    email: string,
    password: string,
    name: string,
    org: string,
  ): Promise<string | null> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/auth/signup`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password, name, org }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "sign up failed");
      return data.token ?? null;
    }
    void email;
    void password;
    void name;
    void org;
    await delay(500);
    return null;
  },

  me: async (): Promise<AuthUser> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/auth/me`, { credentials: "include" });
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
      const res = await apiFetch(`${BASE}/auth/me`, {
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
      await apiFetch(`${BASE}/auth/signout`, {
        method: "POST",
        credentials: "include",
      });
      return;
    }
    await delay(100);
  },

  // Full URL to kick off a backend OAuth flow. Empty string when no backend
  // is configured (mock mode) -- callers should guard on the http prefix.
  oauthURL: (provider: "github" | "google"): string =>
    BASE ? `${BASE}/auth/oauth/${provider}` : "",
};

// -- Workflows ------------------------------------------------------------
export const workflows = {
  // TODO: GET /workflows
  list: async (): Promise<Workflow[]> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows`, {
        credentials: "include",
      });
      if (!res.ok) throw new Error("workflows fetch failed");
      return res.json();
    }
    await delay(200);
    return WORKFLOWS;
  },

  // TODO: GET /workflows/:id
  get: async (id: string): Promise<Workflow> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}`, {
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
    assertWritable("POST", "/workflows");
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows`, {
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
    assertWritable("PUT", `/workflows/${id}`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}`, {
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

  // DELETE /workflows/:id — permanent. The backend refuses (409) for a
  // workflow that has Tendril lease history, since deleting it would destroy
  // the only copy of an active lease's encrypted credentials; that message is
  // surfaced to the caller rather than swallowed.
  remove: async (id: string): Promise<void> => {
    assertWritable("DELETE", `/workflows/${id}`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error ?? "workflow delete failed");
      }
      return;
    }
    await delay(200);
  },

  // TODO: POST /workflows/:id/deploy
  deploy: async (
    id: string,
  ): Promise<{
    agents: { nodeId: string; address: string; network: string }[];
  }> => {
    assertWritable("POST", `/workflows/${id}/deploy`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/deploy`, {
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
      const res = await apiFetch(`${BASE}/workflows/${id}/run`, {
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

  // TODO: POST /workflows/:id/build
  build: async (
    id: string,
    message: string,
  ): Promise<{ reply: string; workflow: Workflow }> => {
    assertWritable("POST", `/workflows/${id}/build`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/build`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "build failed");
      return data;
    }
    await delay(300);
    // Echo the current mock workflow back untouched: the caller replaces its
    // nodes/edges with whatever comes back, so returning an empty graph here
    // would wipe the demo canvas on the first chat message.
    const current = await workflows.get(id);
    return {
      reply:
        "Mock build response — connect a real backend to build workflows from chat.",
      workflow: current,
    };
  },

  // TODO: POST /workflows/:id/stop
  stop: async (id: string): Promise<void> => {
    if (BASE) {
      await apiFetch(`${BASE}/workflows/${id}/stop`, {
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
        const res = await apiFetch(`${BASE}/workflows/${id}/variables`, {
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
      assertWritable("PUT", `/workflows/${id}/variables/${key}`);
      if (BASE) {
        const res = await apiFetch(
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
      assertWritable("DELETE", `/workflows/${id}/variables/${key}`);
      if (BASE) {
        const res = await apiFetch(
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

  // PUT /workflows/:id/schedule
  setSchedule: async (
    id: string,
    cron: string,
  ): Promise<{ cron: string; nextRunAt: string }> => {
    assertWritable("PUT", `/workflows/${id}/schedule`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/schedule`, {
        method: "PUT",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cron }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "could not set schedule");
      return data;
    }
    await delay(200);
    return {
      cron,
      nextRunAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
    };
  },

  // DELETE /workflows/:id/schedule
  clearSchedule: async (id: string): Promise<void> => {
    assertWritable("DELETE", `/workflows/${id}/schedule`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/schedule`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error ?? "could not remove schedule");
      }
      return;
    }
    await delay(150);
  },
};

// -- Credits ----------------------------------------------------------------
export const credits = {
  // The authoritative balance: users.credit_balance_usd_micros, the same row
  // the engine reserves against and debits on every paid call. Anything shown
  // from another source is a guess that drifts the moment a run spends money.
  balance: async (): Promise<number> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/credits/balance`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "balance fetch failed");
      return (data.credit_usd_micros ?? 0) / 1e6;
    }
    await delay(120);
    return 0;
  },

  // Redeems a coupon code, returning the new balance and what this code
  // granted — both in USD. The credited amount is per-code configuration
  // (COUPON_CODES on the backend), so it has to come from the response rather
  // than being assumed. Throws with the server's message (e.g. "invalid coupon
  // code", "coupon already redeemed") on failure so the caller can show it
  // directly.
  redeemCoupon: async (
    code: string,
  ): Promise<{ balanceUSD: number; creditedUSD: number }> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/credits/redeem-coupon`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "coupon redemption failed");
      return {
        balanceUSD: (data.credit_usd_micros ?? 0) / 1e6,
        creditedUSD: (data.credited_usd_micros ?? 0) / 1e6,
      };
    }
    await delay(120);
    throw new Error("coupons aren't available in mock mode");
  },

  // Top-up history from credit_ledger — the same rows the payment webhooks
  // write and settle, scoped server-side to the signed-in user. This replaced
  // a localStorage copy: history kept per-browser disappeared on sign-out or a
  // device change, and showed the previous account's purchases to the next one
  // signing in on the same browser.
  purchases: async (limit = 50): Promise<PurchaseRecord[]> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/credits/purchases?limit=${limit}`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        throw new Error(
          (data as { error?: string } | null)?.error ??
            "purchase history fetch failed",
        );
      }
      return Array.isArray(data) ? (data as PurchaseRecord[]) : [];
    }
    await delay(120);
    return [];
  },
};

// PurchaseRecord is one credit_ledger row as the API returns it (see the Go
// models.CreditTransaction). Amounts stay in their stored units — paise for
// the INR path, cents for the crypto one, micros for credits granted — so the
// caller converts once, at the point of display, rather than trusting a
// pre-rounded number.
//
// amountInrPaise and amountUsdCents are mutually exclusive: a Cashfree top-up
// is INR-denominated with an FX rate attached, a crypto one is already USD, so
// each row carries exactly one of them.
export interface PurchaseRecord {
  id: string;
  provider: string;
  providerOrderId: string;
  providerPaymentId?: string;
  status: string;
  amountInrPaise?: number;
  fxRateUsdPerInr?: number;
  amountUsdCents?: number;
  creditUsdMicros: number;
  createdAt: string;
  completedAt?: string;
}

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

export interface DeadLetterRun {
  id: string;
  runId: string;
  nodeId: string;
  error: string;
  attemptCount: number;
  createdAt: string;
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
  ): Promise<{
    run: { status: string };
    logs: RunLogRecord[];
    deadLetters: DeadLetterRun[];
  }> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/runs/${runId}`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "failed to fetch run");
      return data;
    }
    await delay(150);
    // Mock mode returns a realistic finished run rather than an empty one, so
    // the console and the chat panel can be exercised with no backend
    // attached: an agent answer to render as prose, and a paid tool402 step
    // so the activity strip has a real tool count and settled amount. Mirrors
    // SAMPLE_WORKFLOW's node ids and its $0.065/call x402 weather endpoint.
    const now = Date.now();
    const iso = (msAgo: number) => new Date(now - msAgo).toISOString();
    // One id, referenced everywhere it appears. Spelling it out per-field let
    // the receipt, the explorer link and the payment list drift apart.
    const mockTxId =
      "7F2AC9D1E4B8A6350C1D9E2F4A7B8C3D5E6F1A2B3C4D5E6F7A8B9C0D1E2F3A4B";
    return {
      run: { status: "success" },
      deadLetters: [],
      logs: [
        {
          id: "rl-1",
          runId,
          stepIndex: 0,
          nodeId: "n4",
          nodeType: "tool402",
          status: "success",
          output: {
            txId: mockTxId,
            amount: "0.065",
            settledUsdMicros: 65000,
            nodeName: "x402 Weather",
            explorerURL: `https://allo.info/tx/${mockTxId}`,
            response: {
              location: "San Francisco, CA",
              tempC: 14.2,
              condition: "Partly cloudy",
              windKph: 18,
            },
          },
          durationMs: 1900,
          ts: iso(6300),
        },
        {
          id: "rl-2",
          runId,
          stepIndex: 1,
          nodeId: "n2",
          nodeType: "agent",
          status: "success",
          output: {
            message:
              "It's 14.2°C in San Francisco right now and partly cloudy, with " +
              "winds around 18 km/h. Mild, but the wind makes it feel cooler — " +
              "worth a light jacket if you're heading out.",
            x402Payments: [{ txId: mockTxId, amount: "0.065" }],
          },
          durationMs: 4400,
          ts: iso(1900),
        },
      ],
    };
  },

  resume: async (runId: string): Promise<{ runId: string }> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/runs/${runId}/resume`, {
        method: "POST",
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "resume failed");
      return data;
    }
    await delay(200);
    return { runId };
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
      const res = await apiFetch(
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
      const res = await apiFetch(
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
      const res = await apiFetch(`${BASE}/tools/x402/quote`, {
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

// -- OAuth2 connected accounts (Gmail/Sheets/Calendar/Drive) --------------
// Distinct from `auth` above: that signs a person INTO AgentMesh; this
// connects an EXTERNAL account a Google-type workflow node calls on the
// user's behalf. See backend/internal/api/handlers/oauth2creds.go.
export interface OAuthCredentialSummary {
  id: string;
  provider: string;
  accountLabel: string;
  scopes: string;
  expiresAt: string;
  createdAt: string;
}

export const oauth2 = {
  // A full-page redirect (Google's consent screen), not a fetch -- the
  // caller should set window.location.href to this, not call it as an
  // async request.
  connectURL: (provider: string): string => `${BASE}/oauth2/${provider}/start`,

  listCredentials: async (
    provider: string,
  ): Promise<OAuthCredentialSummary[]> => {
    if (!BASE) return []; // No connected-account concept in mock mode.
    const res = await apiFetch(
      `${BASE}/oauth2/credentials?provider=${encodeURIComponent(provider)}`,
      { credentials: "include" },
    );
    if (!res.ok) return [];
    return res.json().catch(() => []);
  },

  deleteCredential: async (id: string): Promise<void> => {
    if (!BASE) return;
    await apiFetch(`${BASE}/oauth2/credentials/${encodeURIComponent(id)}`, {
      method: "DELETE",
      credentials: "include",
    });
  },
};

// -- Waitlist -------------------------------------------------------------
export const waitlist = {
  // TODO: POST /waitlist
  join: async (email: string): Promise<void> => {
    if (BASE) {
      // Plain fetch, not apiFetch: this is the one genuinely public endpoint
      // here, and it must not send credentials. A credentialed cross-origin
      // request fails outright when CORS_ORIGIN is unset (the wildcard case,
      // where the server cannot send Allow-Credentials), which would break the
      // landing page's signup for a deployment where nothing else is wrong.
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
    const res = await apiFetch(`${BASE}/payments/cashfree/order`, {
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
    const res = await apiFetch(`${BASE}/payments/cashfree/verify`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ order_id: orderId }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? "payment verification failed");
    return data;
  },

  // Which gateways this deployment can actually take money through, plus the
  // live INR->USD rate. Both come from the server because both are deployment
  // state: NOWPayments quotes in USD and can only be offered when a rate is
  // available, and it has to be the same rate the backend uses rather than the
  // mock constant in lib/credits/fx.ts.
  listProviders: async (): Promise<{
    usd_per_inr: number;
    providers: { id: PaymentMethod; enabled: boolean; currency: string }[];
  }> => {
    if (!BASE) throw new Error("payments require a configured backend");
    const res = await apiFetch(`${BASE}/payments/providers`, {
      credentials: "include",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok)
      throw new Error(data.error ?? "could not load payment providers");
    return data;
  },

  // Opens a NOWPayments hosted invoice. Crypto settles on-chain with no
  // client-side completion step, so the IPN webhook is the ONLY path that
  // credits a crypto top-up -- there is deliberately no verify call here.
  createCryptoInvoice: async (
    amountUSDCents: number,
  ): Promise<{ order_id: string; invoice_url: string }> => {
    if (!BASE) throw new Error("payments require a configured backend");
    const res = await apiFetch(`${BASE}/payments/nowpayments/invoice`, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ amount_usd_cents: amountUSDCents }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error ?? "invoice creation failed");
    return data;
  },
};

// -- Usage & Credits ------------------------------------------------------
// Real endpoints don't exist yet (see plan §5 -- needs a metering change in
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
// body for a server-provided `error` message -- before this was shared, only
// summary did, and the other four threw fixed strings that discarded detail.
async function usageFetch<T>(path: string, mock: () => T): Promise<T> {
  if (BASE) {
    const res = await apiFetch(`${BASE}${path}`, { credentials: "include" });
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

  // Settlements are the latest on-chain payments, not a range-scoped metric --
  // the real endpoint takes only `limit`, and the panel deliberately ignores
  // the 24h/7d/30d selector. Any range yields the same rows in mock mode, so
  // "30d" just picks a canonical memoized payload to slice from.
  settlements: (limit = 20): Promise<Settlement[]> =>
    usageFetch(`/usage/settlements?limit=${limit}`, () =>
      mockUsage("30d").settlements.slice(0, limit),
    ),
};

// -- Helpers --------------------------------------------------------------
// Exported for the same reason BASE is: lib/bazaar.ts needs the identical
// mock-mode delay rather than a second copy that can drift out of sync.
export function delay(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
