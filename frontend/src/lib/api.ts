// TODO: Replace all stubs with real FastAPI calls when backend is ready.
// Base URL will come from env: process.env.NEXT_PUBLIC_API_URL

import type { BuildProgress } from "@/components/canvas/chat/buildProgress";
import {
  Workflow,
  UsageRange,
  UsageSummary,
  UsagePoint,
  WorkflowSpend,
  EndpointUsage,
  Settlement,
  CostEstimate,
  RunPage,
  UpcomingRun,
} from "./types";
import { WORKFLOWS, SAMPLE_WORKFLOW, buildUsage } from "./data";
import {
  fixtureRunDetail,
  fixtureRunPage,
  fixtureUpcoming,
  recordStartedRun,
} from "./runFixtures";
import { fixtureWorkflow } from "./workflowFixtures";
import { assertWritable } from "./readonly";
import { IS_NATIVE, authHeaders } from "./nativeAuth";
import type { PaymentMethod } from "@/components/checkout/types";
import type { RunCosts } from "./runCosts";

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
  hasPassword?: boolean;
  createdAt?: string;
  lowBalanceUsdMicros?: number;
}

// Thrown by auth.me() when the server answered without confirming a session.
// The status is kept so a caller can tell being signed out (401, 403) from the
// server failing (5xx), which says nothing about the session.
export class AuthCheckError extends Error {
  constructor(readonly status: number) {
    super("unauthorized");
    this.name = "AuthCheckError";
  }
}

// Whether a failed session check is a connection problem rather than an
// answer: the request never reached the server (fetch rejects with a
// TypeError), or the server itself failed.
export function isConnectionFailure(err: unknown): boolean {
  if (err instanceof TypeError) return true;
  return err instanceof AuthCheckError && err.status >= 500;
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
      if (!res.ok) throw new AuthCheckError(res.status);
      return res.json();
    }
    return {
      id: "dev",
      email: "dev@local",
      name: "Dev",
      orgName: "Acme Capital",
      needsOnboarding: false,
      hasPassword: false,
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

  changePassword: async (currentPassword: string, newPassword: string): Promise<void> => {
    if (!BASE) throw new Error("Password changes require a connected backend.");
    const res = await apiFetch(`${BASE}/auth/password`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ currentPassword, newPassword }),
    });
    if (res.status !== 204) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error ?? "Could not change password.");
    }
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

  deleteAccount: async (confirmation: string, password: string): Promise<void> => {
    if (!BASE) throw new Error("Account deletion requires a connected backend.");
    const res = await apiFetch(`${BASE}/auth/me`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ confirmation, password }),
    });
    if (res.status !== 204) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error ?? "Could not delete account. Try again.");
    }
  },

  // Full URL to kick off a backend OAuth flow. Empty string when no backend
  // is configured (mock mode) -- callers should guard on the http prefix.
  oauthURL: (provider: "github" | "google"): string =>
    BASE ? `${BASE}/auth/oauth/${provider}` : "",

  // The external browser must start on the frontend proxy's origin so its
  // state cookie is also sent to the provider callback.
  nativeOAuthURL: async (provider: "github" | "google"): Promise<string> => {
    if (!BASE) throw new Error("Social sign in is not configured.");
    const res = await apiFetch(`${BASE}/auth/oauth/${provider}/url`);
    if (!res.ok) throw new Error("Social sign in is not configured.");
    const data = (await res.json()) as { url: string };
    return data.url;
  },

  // Swap the one-time code from an ai.agentmesh.app://auth deep link for a
  // session token. Native only: the web flow ends in a cookie and never sees a
  // code at all. Returns null when the backend refuses -- an expired code and a
  // wrong verifier are deliberately indistinguishable there, so there is
  // nothing more specific to hand back. See native/oauth.ts and the Go side's
  // handlers/oauth_native.go.
  oauthExchange: async (
    code: string,
    verifier: string,
  ): Promise<string | null> => {
    if (!BASE) return null;
    const res = await apiFetch(`${BASE}/auth/oauth/exchange`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code, verifier }),
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string };
    return data.token ?? null;
  },
};

// -- Workflows ------------------------------------------------------------
export interface UserSettings {
  lowBalanceUsdMicros: number;
  maxCallSpendUsdMicros: number | null;
  limits: {
    minCallSpendUsdMicros: number;
    maxCallSpendUsdMicros: number;
    maxLowBalanceUsdMicros: number;
  };
}

export type UserSettingsPatch = Partial<Pick<UserSettings, "lowBalanceUsdMicros" | "maxCallSpendUsdMicros">>;

export const settings = {
  get: async (): Promise<UserSettings> => {
    if (!BASE) throw new Error("Settings require a connected backend.");
    const res = await apiFetch(`${BASE}/settings`);
    const data = await res.json();
    if (!res.ok) throw new Error(data.error ?? "Could not load settings.");
    return data;
  },
  update: async (patch: UserSettingsPatch): Promise<UserSettings> => {
    assertWritable("PATCH", "/settings");
    if (!BASE) throw new Error("Settings require a connected backend.");
    const res = await apiFetch(`${BASE}/settings`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error ?? "Could not save settings.");
    return data;
  },
};

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
    // Each workflow in the mock list opens its own graph; anything else (the
    // canvas's sample) still gets the weather workflow.
    return fixtureWorkflow(id) ?? JSON.parse(JSON.stringify(SAMPLE_WORKFLOW));
  },

  // GET /workflows/:id/estimate -- static low/high cost band for one run.
  estimate: async (id: string): Promise<CostEstimate> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/estimate`, {
        credentials: "include",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error ?? "estimate fetch failed");
      return data;
    }
    await delay(80);
    return {
      lowUsdMicros: 0,
      highUsdMicros: 0,
      lines: [],
      hasUnpricedX402: false,
    };
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
    const runId = `r-${Math.floor(1800 + Math.random() * 100)}`;
    recordStartedRun(runId, id);
    return { runId };
  },

  // TODO: POST /workflows/:id/build
  build: async (
    id: string,
    message: string,
    buildId?: string,
  ): Promise<{ reply: string; workflow: Workflow }> => {
    assertWritable("POST", `/workflows/${id}/build`);
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/build`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        // buildId lets the chat poll buildProgress for this build's steps.
        // timeZone is what the builder reads "every morning at 9" in.
        body: JSON.stringify({
          message,
          ...(buildId ? { buildId } : {}),
          ...(browserTimeZone() ? { timeZone: browserTimeZone() } : {}),
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new BuildRequestError(
          data.error ?? "build failed",
          typeof data.error === "string",
        );
      }
      return data;
    }
    await delay(300);
    // Echo the current mock workflow back untouched: the caller replaces its
    // nodes/edges with whatever comes back, so returning an empty graph here
    // would wipe the demo canvas on the first chat message.
    const current = await workflows.get(id);
    return {
      reply:
        "Mock build response: connect a real backend to build workflows from chat.",
      workflow: current,
    };
  },

  // The steps a chat build has taken so far -- see chat/buildProgress.ts.
  buildProgress: async (id: string, buildId: string): Promise<BuildProgress> => {
    if (!BASE) return { steps: [] };
    const res = await apiFetch(
      `${BASE}/workflows/${id}/build/progress?buildId=${encodeURIComponent(buildId)}`,
      { credentials: "include" },
    );
    if (!res.ok) throw new Error(`build progress ${res.status}`);
    return res.json();
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

  // The console chat transcript, stored server-side so the conversation
  // follows the user across browsers and devices. The client owns the whole
  // transcript and replaces it on every change.
  chat: {
    load: async (
      id: string,
      mode: "build" | "run",
    ): Promise<{ sessionId: string; messages: unknown[] } | null> => {
      if (!BASE) return null;
      const res = await apiFetch(`${BASE}/workflows/${id}/chat?mode=${mode}`, {
        credentials: "include",
      });
      if (!res.ok) throw new Error(`chat transcript ${res.status}`);
      const data = await res.json().catch(() => null);
      if (!data || !Array.isArray(data.messages)) return null;
      return { sessionId: data.sessionId ?? "", messages: data.messages };
    },
    save: async (
      id: string,
      mode: "build" | "run",
      session: { sessionId: string; messages: unknown[] },
    ): Promise<void> => {
      assertWritable("PUT", `/workflows/${id}/chat`);
      if (!BASE) return;
      const res = await apiFetch(`${BASE}/workflows/${id}/chat?mode=${mode}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(session),
      });
      if (!res.ok) throw new Error(`chat transcript save ${res.status}`);
    },
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

  // PUT /workflows/:id/geofence
  //
  // No assertWritable, unlike every other write above, and that absence is the
  // change: a geofence is chosen from the place it describes, so a viewer may
  // set one. See the "workflow.geofence" capability in lib/readonly.ts for the
  // reasoning, and readonly.test.ts for the tests that pin it.
  //
  // Distinct from native/api.ts's setGeofence, which the shipped Android app
  // uses via shell.setGeofence: that one also arms Android's GeofencingClient,
  // because the device doing the watching is the device that has to be told.
  // This one only records the zone on the server -- correct for a browser,
  // where nothing is watching, and honest about it in the screen's copy.
  setGeofence: async (
    id: string,
    fence: { lat: number; lng: number; radiusM: number },
  ): Promise<void> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/geofence`, {
        method: "PUT",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(fence),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error ?? "could not save the zone");
      }
      return;
    }
    await delay(200);
  },

  // DELETE /workflows/:id/geofence
  clearGeofence: async (id: string): Promise<void> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/workflows/${id}/geofence`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error ?? "could not remove the zone");
      }
      return;
    }
    await delay(150);
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
  status: "pending" | "running" | "success" | "failed" | "degraded";
  output?: unknown;
  durationMs?: number;
  ts: string;
}

/**
 * Which run-log statuses mean a step has finished and its result is final.
 *
 * Written as a Record over every status in RunLogRecord rather than as an
 * inline `status === "success" || status === "failed"` test, so that adding a
 * status to the union above fails to compile here instead of being silently
 * dropped. That is not hypothetical: "degraded" was added to the union and to
 * the live SSE path, while both readers of the stored rows kept their own
 * two-value allowlist and discarded every degraded step -- so a run that lost
 * a source was replayed from the database as a clean one.
 */
const SETTLED_LOG_STATUS: Record<RunLogRecord["status"], boolean> = {
  pending: false,
  running: false,
  success: true,
  failed: true,
  degraded: true,
};

/**
 * Whether a stored run-log row carries a final result. A row still marked
 * pending or running is a step the engine had not finished writing.
 */
export function isSettledLogStatus(status: RunLogRecord["status"]): boolean {
  return SETTLED_LOG_STATUS[status];
}

export interface DeadLetterRun {
  id: string;
  runId: string;
  nodeId: string;
  error: string;
  attemptCount: number;
  createdAt: string;
}

// The run as GET /runs/{runId} returns it (models.Run, without its input).
export interface RunDetail {
  id: string;
  workflowId: string;
  triggeredBy: string;
  status: string;
  startedAt: string;
  finishedAt?: string;
  // Everything debit_ledger has charged for this run so far; grows while the
  // run is still "running". Optional because a server older than this field
  // omits it, and RunSheet then falls back to the list row's figure.
  spendUsdMicros?: number;
}

// Thrown when the backend has no run history routes yet. An older server
// answers them with chi's plain-text "404 page not found"; a workflow that is
// missing or someone else's is a JSON 404 with an "error" field instead, and
// stays an ordinary error.
export class RunsUnavailableError extends Error {
  constructor() {
    super("Run history isn't available on this server yet.");
    this.name = "RunsUnavailableError";
  }
}

export interface RunHistoryOptions {
  cursor?: string | null;
  limit?: number;
}

function runHistoryQuery(options: RunHistoryOptions): string {
  const q = new URLSearchParams();
  if (options.limit) q.set("limit", String(options.limit));
  if (options.cursor) q.set("cursor", options.cursor);
  const s = q.toString();
  return s ? `?${s}` : "";
}

async function readRunPage(res: Response, fallback: string): Promise<RunPage> {
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (res.ok) return data as RunPage;
  const error =
    data !== null &&
    typeof data === "object" &&
    typeof (data as { error?: unknown }).error === "string"
      ? (data as { error: string }).error
      : null;
  if (res.status === 404 && error === null) throw new RunsUnavailableError();
  throw new Error(error ?? fallback);
}

// Thrown when the backend has no upcoming-runs route yet, told apart from a
// real failure the same way RunsUnavailableError is: an older server answers
// with chi's plain-text 404. Screens that show upcoming runs hide the section
// rather than show an error.
export class UpcomingUnavailableError extends Error {
  constructor() {
    super("Upcoming runs aren't available on this server yet.");
    this.name = "UpcomingUnavailableError";
  }
}

export const schedules = {
  // What the scheduler will run next, soonest first: up to `per` occurrences
  // of each schedule, `limit` in all (GET /schedules/upcoming). `workflowId`
  // asks for that one workflow's schedule alone.
  upcoming: async (
    options: { limit?: number; per?: number; workflowId?: string } = {},
  ): Promise<UpcomingRun[]> => {
    if (BASE) {
      const q = new URLSearchParams();
      if (options.limit) q.set("limit", String(options.limit));
      if (options.per) q.set("per", String(options.per));
      if (options.workflowId) q.set("workflowId", options.workflowId);
      const query = q.toString() ? `?${q}` : "";
      const res = await apiFetch(`${BASE}/schedules/upcoming${query}`, {
        credentials: "include",
      });
      const data = (await res.json().catch(() => null)) as {
        upcoming?: UpcomingRun[];
        error?: string;
      } | null;
      if (res.ok) return data?.upcoming ?? [];
      if (res.status === 404 && typeof data?.error !== "string") {
        throw new UpcomingUnavailableError();
      }
      throw new Error(data?.error ?? "failed to load upcoming runs");
    }
    await delay(150);
    return fixtureUpcoming(options);
  },
};

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
    run: RunDetail;
    logs: RunLogRecord[];
    deadLetters: DeadLetterRun[];
    // What the run was charged, from the debit ledger (#111). Optional: a
    // backend older than that change does not send it.
    costs?: RunCosts;
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
    // A run from the mock history, or one started in this session, comes back
    // as itself: its own status, steps, result and payments.
    const fixture = fixtureRunDetail(runId);
    if (fixture) return fixture;
    // Anything else returns a realistic finished run rather than an empty one,
    // so the console and the chat panel can be exercised with no backend
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
      run: {
        id: runId,
        workflowId: SAMPLE_WORKFLOW.id,
        triggeredBy: "manual",
        status: "success",
        startedAt: iso(8200),
        finishedAt: iso(0),
        // Matches the $0.065/call x402 weather step below.
        spendUsdMicros: 65_000,
      },
      deadLetters: [],
      // The paid weather call below, as the ledger would record it: the
      // $0.065 relay cost plus the fixed $1.50 x402 platform fee, so the
      // hover exercises the multi-kind breakdown rather than a single charge.
      costs: {
        totalUsdMicros: 1_565_000,
        steps: [
          {
            nodeId: "n4",
            totalUsdMicros: 1_565_000,
            byKind: { x402_relay_cost: 65_000, x402_platform_fee: 1_500_000 },
          },
        ],
      },
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
              "winds around 18 km/h. Mild, but the wind makes it feel cooler: " +
              "worth a light jacket if you're heading out.",
            x402Payments: [{ txId: mockTxId, amount: "0.065" }],
          },
          durationMs: 4400,
          ts: iso(1900),
        },
      ],
    };
  },

  // One workflow's runs, newest first (GET /workflows/{id}/runs).
  listForWorkflow: async (
    workflowId: string,
    options: RunHistoryOptions = {},
  ): Promise<RunPage> => {
    if (BASE) {
      const res = await apiFetch(
        `${BASE}/workflows/${encodeURIComponent(workflowId)}/runs${runHistoryQuery(options)}`,
        { credentials: "include" },
      );
      return readRunPage(res, "failed to load runs");
    }
    await delay(200);
    return fixtureRunPage({ workflowId, ...options });
  },

  // The user's newest runs across their own workflows (GET /runs).
  recent: async (options: RunHistoryOptions = {}): Promise<RunPage> => {
    if (BASE) {
      const res = await apiFetch(`${BASE}/runs${runHistoryQuery(options)}`, {
        credentials: "include",
      });
      return readRunPage(res, "failed to load recent runs");
    }
    await delay(200);
    return fixtureRunPage(options);
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
    provider?: string,
  ): Promise<OAuthCredentialSummary[]> => {
    if (!BASE) return []; // No connected-account concept in mock mode.
    const res = await apiFetch(
      `${BASE}/oauth2/credentials${provider ? `?provider=${encodeURIComponent(provider)}` : ""}`,
      { credentials: "include" },
    );
    if (!res.ok) throw new Error("Could not load connected accounts.");
    return res.json();
  },

  deleteCredential: async (id: string): Promise<void> => {
    assertWritable("DELETE", `/oauth2/credentials/${encodeURIComponent(id)}`);
    if (!BASE) throw new Error("Disconnecting requires a connected backend.");
    const res = await apiFetch(`${BASE}/oauth2/credentials/${encodeURIComponent(id)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (res.status !== 204) throw new Error("Could not disconnect this account. Try again.");
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
    // Mock mode has no server to ask, and a screen that quotes nothing
    // cannot demonstrate the top-up flow. A plausible fixture rate keeps
    // the mock build usable; nothing is ever charged against it.
    if (!BASE) {
      return {
        usd_per_inr: 0.010423,
        providers: [
          { id: "cashfree", enabled: true, currency: "INR" },
          { id: "nowpayments", enabled: true, currency: "USD" },
        ],
      };
    }
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

/** The browser's IANA timezone, or "" where the runtime cannot say. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

/**
 * A build request that did not succeed. `answered` is true when the backend
 * itself replied with an error, which means the build is over. False means
 * the reply never came from the backend at all (a proxy timeout, a dropped
 * connection), and the build may well still be running.
 */
export class BuildRequestError extends Error {
  constructor(
    message: string,
    readonly answered: boolean,
  ) {
    super(message);
    this.name = "BuildRequestError";
  }
}
