// The shell's own backend client.
//
// Separate from the web bundle's lib/api.ts on purpose: this code runs in the
// background, outside any page, when the OS wakes us for a geofence crossing.
// There may be no WebView document alive at all at that moment, so it cannot
// rely on anything the page set up.
import { clearToken, loadToken } from "./auth";
import type { Fix } from "./queue";

// lib/readonly.ts's WRITE_RULES/assertWritable pattern does NOT belong here,
// despite setGeofence/clearGeofence below being the same shape of write that
// pattern guards elsewhere. That guard exists to stop the WEB bundle's
// read-only VIEWER (a phone Browser tab) from writing through a stale bundle
// or a deep link; it is keyed on isHandheldNow(), which lib/device.ts
// classifies this native Android WebView as UNCONDITIONALLY and BY DESIGN
// (see its comment: "the outcome that was wanted"). Gating this file on that
// same check would make setGeofence/clearGeofence throw on every single
// call from the shipped app -- the geofence-authoring screen IS this app's
// job (#112), not a control withheld from it.

// Baked in at build time, same value the web bundle is given. There is no
// Next server in front of the shell, so this is the backend's absolute URL.
const API_BASE = process.env.NEXT_PUBLIC_API_URL ?? "";

async function call(path: string, init: RequestInit = {}): Promise<Response> {
  const token = await loadToken();
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-AgentMesh-Client": "android",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init.headers as Record<string, string> | undefined),
    },
  });
  if (res.status === 401) {
    // Drop the dead token rather than retrying with it forever.
    await clearToken();
    // A plain Error, not a dedicated type: flush()'s catch in geofence.ts
    // used to branch on a 401 specifically, but both branches took the same
    // action (stop the flush, keep the queue), so nothing left in the
    // codebase actually distinguishes this from any other failure by type.
    throw new Error("session expired");
  }
  return res;
}

export interface PingResult {
  inside: boolean;
  triggered: boolean;
  direction?: "enter" | "leave";
  runId?: string;
  stale?: boolean;
}

// Pushes one fix. Throws on anything the client should retry later; returns
// normally for every answer the server considers final, INCLUDING "that was
// not an event" and "that was a replay" -- both mean the fix is handled and
// must come off the queue.
export async function pushFix(fix: Fix): Promise<PingResult> {
  const res = await call(`/workflows/${fix.workflowId}/trigger/location`, {
    method: "POST",
    body: JSON.stringify({
      lat: fix.lat,
      lng: fix.lng,
      accuracyM: fix.accuracyM,
      recordedAt: fix.recordedAt,
    }),
  });

  // 429 is the server asking us to slow down, not to give up: keep the fix.
  if (res.status === 429) throw new Error("rate limited");
  // 5xx is ours to retry. 4xx other than 429 is not -- a malformed or
  // unconfigured fix will fail identically forever, and keeping it would
  // block the queue behind it.
  if (res.status >= 500) throw new Error(`server error ${res.status}`);
  if (!res.ok) return { inside: false, triggered: false };

  return (await res.json()) as PingResult;
}

export interface Geofence {
  lat: number;
  lng: number;
  radiusM: number;
}

export async function setGeofence(
  workflowId: string,
  fence: Geofence,
): Promise<void> {
  const res = await call(`/workflows/${workflowId}/geofence`, {
    method: "PUT",
    body: JSON.stringify(fence),
  });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error ?? "could not save the geofence");
  }
}

export async function clearGeofence(workflowId: string): Promise<void> {
  await call(`/workflows/${workflowId}/geofence`, { method: "DELETE" });
}
