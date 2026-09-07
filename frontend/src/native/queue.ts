// The offline queue for location fixes.
//
// A geofence crossing happens exactly where signal tends to be worst: a
// basement car park, a tunnel, a trail. Fire-and-forget would silently drop
// the one event the whole feature exists to deliver, so every fix is persisted
// before it is sent and only removed once the server has accepted it.
//
// The server is the other half of this: it orders fixes by the DEVICE's
// timestamp and ignores any older than the last one it acted on, so flushing a
// backlog cannot re-fire a crossing that was already handled. That is why
// recordedAt is captured here, at observation time, and never at send time.
import { Preferences } from "@capacitor/preferences";
import { Geofence } from "./nativeGeofence";

const QUEUE_KEY = "agentmesh.geofence.queue";

// Bounded so a phone that is offline for a week cannot grow this without
// limit. The oldest fixes are the least interesting -- the server ignores
// anything older than the last crossing it handled anyway -- so the cap drops
// from the front.
const MAX_QUEUED = 200;

// Past this, a fix describes a journey nobody is waiting on any more. Sending
// it would at best be a no-op and at worst fire a workflow about somewhere the
// user was days ago.
const MAX_AGE_MS = 24 * 60 * 60 * 1000;

export interface Fix {
  workflowId: string;
  lat: number;
  lng: number;
  accuracyM?: number;
  /** ISO 8601, captured when the OS reported the fix, never when it is sent. */
  recordedAt: string;
  /**
   * A per-process counter GeofenceReceiver.java stamps on every fix it
   * appends. Android's fused location provider can hand two consecutive
   * geofence transitions the same cached Location object with no fresh GPS
   * fix in between, which means recordedAt alone -- even at millisecond
   * precision -- is not always unique for two fixes queued close together.
   * Folded into remove()'s dedup key below so an ENTER and an EXIT sharing
   * an identical location+timestamp still get distinct keys. Optional
   * because a fix already sitting in storage from before this field existed
   * won't have it; dedup falls back to the old two-part key for those.
   */
  seq?: number;
}

// A corrupt queue must not brick the trigger. Losing the backlog is
// recoverable; refusing to record anything ever again is not.
function parseFixes(value: string | null): Fix[] {
  if (!value) return [];
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed) ? (parsed as Fix[]) : [];
  } catch {
    return [];
  }
}

async function read(): Promise<Fix[]> {
  const { value } = await Preferences.get({ key: QUEUE_KEY });
  return parseFixes(value);
}

async function write(fixes: Fix[]): Promise<void> {
  await Preferences.set({ key: QUEUE_KEY, value: JSON.stringify(fixes) });
}

// Migrates whatever GeofenceReceiver.java queued while the app was dead into
// the main queue above. drainNativeQueue() reads and clears the native side's
// queue in one atomic native call (synchronized against
// GeofenceReceiver.append() on the Java side), so there is no read-then-write
// window here for a concurrent append to land in and get silently dropped --
// unlike a plain @capacitor/preferences read/write pair, which cannot
// coordinate with a write happening outside the WebView at all. Anything
// GeofenceReceiver appends after this call's clear starts a fresh queue and
// is picked up whole on the next drainNative().
async function drainNative(): Promise<void> {
  const { value } = await Geofence.drainNativeQueue();
  const snapshot = parseFixes(value);
  if (snapshot.length === 0) return;

  const all = [...(await read()), ...snapshot];
  await write(all.slice(-MAX_QUEUED));
}

export async function pending(now = Date.now()): Promise<Fix[]> {
  await drainNative();
  const all = await read();
  const fresh = all.filter((f) => now - Date.parse(f.recordedAt) < MAX_AGE_MS);
  // Persist the drop, not just the read-side filter: the privacy disclosure
  // shown before Android's permission dialog promises a fix "is deleted...
  // within a day if it never can be sent." Filtering fresh out of the return
  // value without writing it back left every stale fix sitting in
  // @capacitor/preferences indefinitely -- the promise wasn't kept, only the
  // symptom (an old fix never being SENT) was masked.
  if (fresh.length !== all.length) await write(fresh);
  return fresh.sort(
    (a, b) => Date.parse(a.recordedAt) - Date.parse(b.recordedAt),
  );
}

// Matches sent/stored fixes on workflow + timestamp + seq (when present) --
// see Fix.seq's doc comment for why timestamp alone isn't always unique.
function dedupKey(f: Fix): string {
  return `${f.workflowId}@${f.recordedAt}@${f.seq ?? ""}`;
}

// Removes exactly the fixes that were accepted, matched on their timestamp and
// workflow. Anything enqueued while a flush was in flight survives.
export async function remove(sent: Fix[]): Promise<void> {
  const gone = new Set(sent.map(dedupKey));
  await write((await read()).filter((f) => !gone.has(dedupKey(f))));
}

export async function clear(): Promise<void> {
  await Preferences.remove({ key: QUEUE_KEY });
}
