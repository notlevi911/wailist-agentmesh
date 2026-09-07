import { isHandheldNow } from "./device";

// The web app is an editor on a computer and a viewer on a handheld.
//
// This is the GitHub arrangement: browse anywhere, author where you have the
// keyboard and pointer to do it with. The line is drawn by DEVICE, not by
// window width -- a laptop dragged narrow is still a laptop, and taking its
// editor away because of a window size was simply a bug. See lib/device.ts
// for how a handheld is identified and why no single browser API suffices.

// Named for what the user is trying to do, not for the endpoint behind it, so
// a call site reads as intent: `can("workflow.deploy", readOnly)`.
export type Capability =
  // Authoring — needs the room a desktop has.
  | "workflow.create"
  | "workflow.delete"
  | "workflow.editGraph"
  | "workflow.deploy"
  | "workflow.buildFromChat"
  // Operating — available on any screen.
  | "workflow.run"
  | "workflow.stop"
  | "workflow.chat"
  | "account.billing"
  | "account.settings";

// Everything absent from this set stays available. Listing what is *withheld*
// (rather than what is permitted) means a capability added later is readable
// by default, and has to be denied on purpose.
const WITHHELD: ReadonlySet<Capability> = new Set<Capability>([
  "workflow.create",
  "workflow.delete",
  "workflow.editGraph",
  "workflow.deploy",
  // Build mode rewrites the graph from a chat message, so it is authoring
  // wearing a conversation's clothes. Ordinary chat with an already-deployed
  // workflow is not, and stays open.
  "workflow.buildFromChat",
]);

// Pure on purpose: `readOnly` is passed in rather than measured here, so the
// policy can be tested without a DOM and so React components drive it from
// useReadOnly() -- a function that measured the viewport itself would not
// re-render anything when the window crossed the breakpoint.
export function can(capability: Capability, readOnly: boolean): boolean {
  if (!readOnly) return true;
  return !WITHHELD.has(capability);
}

// Defence in depth for the API layer. Hiding a control is a UX decision; this
// is the guarantee that a missed control, a stale bundle, or a deep link
// cannot still put a write on the wire from a viewer.
//
// The rules deliberately mirror backend/internal/api/readonly.go one for one.
const WRITE_RULES: ReadonlyArray<{ method: string; pattern: RegExp }> = [
  { method: "POST", pattern: /^\/workflows$/ },
  { method: "PUT", pattern: /^\/workflows\/[^/]+$/ },
  { method: "DELETE", pattern: /^\/workflows\/[^/]+$/ },
  { method: "POST", pattern: /^\/workflows\/[^/]+\/deploy$/ },
  { method: "POST", pattern: /^\/workflows\/[^/]+\/build$/ },
  { method: "PUT", pattern: /^\/workflows\/[^/]+\/schedule$/ },
  { method: "DELETE", pattern: /^\/workflows\/[^/]+\/schedule$/ },
  { method: "PUT", pattern: /^\/workflows\/[^/]+\/geofence$/ },
  { method: "DELETE", pattern: /^\/workflows\/[^/]+\/geofence$/ },
  { method: "GET", pattern: /^\/tendril\/console$/ },
  // Same exception, same reason: a GET that find-or-creates the console's
  // workflow row. /prism/console/exists never creates one, and /prism/run is
  // operating rather than authoring, so neither is listed -- exactly the line
  // /tendril/console/exists and /tendril/run already sit on.
  { method: "GET", pattern: /^\/prism\/console$/ },
];

// `path` is the API path as written at the call site (leading slash, no
// origin, no query). Query strings and trailing slashes are normalised off
// first so a caller cannot slip a write past by appending either.
export function isWriteBlocked(
  method: string,
  path: string,
  readOnly: boolean,
): boolean {
  if (!readOnly) return false;
  const m = method.toUpperCase();
  let p = path.split("?")[0].split("#")[0];
  if (p.length > 1 && p.endsWith("/")) p = p.slice(0, -1);
  return WRITE_RULES.some((r) => r.method === m && r.pattern.test(p));
}

// For callers outside React, which is really just the fetch guards below.
// A one-shot reading is the right shape there: it answers "may this call go
// out, right now", and nothing needs to re-render when the answer changes.
// Components must use useReadOnly() instead.
export function isReadOnlyNow(): boolean {
  return isHandheldNow();
}

// Defence in depth for the API layer, shared by every module that calls this
// backend directly from the WEB bundle (lib/api.ts, lib/tendril.ts). Not for
// native/api.ts: that file runs only inside the native Android shell, which
// isHandheldNow() classifies as handheld unconditionally by design (see
// lib/device.ts) -- gating it on this same check would block every native
// write permanently rather than just a web viewer's.
export function assertWritable(method: string, path: string): void {
  if (isWriteBlocked(method, path, isReadOnlyNow())) {
    throw new Error(
      "Workflows can only be edited in the AgentMesh desktop app.",
    );
  }
}
