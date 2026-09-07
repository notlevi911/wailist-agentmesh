// The background geofence client.
//
// Android's own GeofencingClient watches the boundary and wakes the app on a
// crossing. We never poll GPS: polling is why location apps get uninstalled,
// and the platform batches geofence work across every app on the device.
//
// Note where the work actually happens. When the app is alive, the flush below
// sends queued fixes. When it is NOT -- the common case for a real crossing --
// the native receiver (GeofenceReceiver.java) appends straight to the same
// queue, in the same storage, and this code drains it on next launch without
// knowing or caring that native wrote it.
import { Network } from "@capacitor/network";
import type { PluginListenerHandle } from "@capacitor/core";
import { Geofence } from "./nativeGeofence";
import { pushFix } from "./api";
import { pending, remove, type Fix } from "./queue";

let flushing = false;

// Module-scoped, not per-workflow: GeofencingClient.addGeofences() is
// additive, so more than one workflow can have an armed fence at the same
// time, and stopping ONE of them must not silence flush-on-reconnect for
// the others still armed. Tracked by workflow id (not a call counter) so
// re-saving the SAME workflow's radius -- which calls start() again with no
// stop() in between -- doesn't inflate the count and leak the listener past
// its one real stop() call.
let networkListener: PluginListenerHandle | null = null;
const armedWorkflows = new Set<string>();

// Serializes every start()/stop() call's listener bookkeeping through one
// chain. Without this, two start() calls arming different workflows back to
// back could both synchronously see networkListener as null, since the check
// and the `await Network.addListener(...)` that follows it are not one atomic
// step -- the second call's check can run during the first call's await,
// before the first has assigned the handle. The same gap exists between a
// stop() that is mid-`await networkListener.remove()` and a start() deciding
// whether to skip re-registering. Routing both functions' listener-touching
// code through this lock makes each call's read-decide-write on
// networkListener/armedWorkflows atomic with respect to every other call.
let listenerLock: Promise<unknown> = Promise.resolve();
function withListenerLock<T>(fn: () => Promise<T>): Promise<T> {
  const result = listenerLock.then(fn, fn);
  listenerLock = result.catch(() => undefined);
  return result;
}

// Sends whatever is queued, oldest first, stopping at the first failure.
//
// Stopping rather than skipping is deliberate: fixes are ordered, and the
// server uses that order to decide what is a crossing. Pushing a newer fix
// past one that failed would advance the server's idea of "last seen" and make
// the skipped one permanently stale.
export async function flush(): Promise<void> {
  if (flushing) return;
  flushing = true;
  try {
    const sent: Fix[] = [];
    for (const fix of await pending()) {
      try {
        await pushFix(fix);
        sent.push(fix);
      } catch {
        // Unauthorized means nothing will succeed until the user signs in
        // again; everything else (a 429, a 5xx) is worth retrying later.
        // Both stop the flush the same way: the queue is kept either way --
        // the crossings happened, and they still matter -- so there is
        // nothing to actually do differently between the two today.
        break;
      }
    }
    if (sent.length) await remove(sent);
  } finally {
    flushing = false;
  }
}

export interface StartOptions {
  workflowId: string;
  lat: number;
  lng: number;
  radiusM: number;
}

/**
 * Registers the zone with the OS.
 *
 * Returns false when background location has not been granted. That is a
 * normal outcome rather than an error: the app is a perfectly good
 * viewer/controller without it, and permissions.ts owns explaining the trade
 * before Android's dialog appears.
 */
export async function start(opts: StartOptions): Promise<boolean> {
  const { granted } = await Geofence.hasPermission();
  if (!granted) return false;

  await Geofence.addGeofence({
    id: opts.workflowId,
    lat: opts.lat,
    lng: opts.lng,
    radiusM: opts.radiusM,
  });

  // The other half of the offline queue: without this a backlog sits there
  // until the next crossing happens to occur.
  //
  // Registered once, not once per armed workflow: the handle is kept so a
  // second (or fifth) start() call doesn't stack another listener.
  // Deliberately NOT Network.removeAllListeners() in stop() below, which the
  // review suggested -- that would also tear down listeners belonging to any
  // other code using the same plugin, and fixing our own leak by reaching
  // into everyone else's is not a fix.
  await withListenerLock(async () => {
    armedWorkflows.add(opts.workflowId);
    if (!networkListener) {
      networkListener = await Network.addListener(
        "networkStatusChange",
        (status) => {
          if (status.connected) void flush();
        },
      );
    }
  });

  // Anything the native receiver queued while the app was closed.
  await flush();
  return true;
}

export async function stop(workflowId: string): Promise<void> {
  await Geofence.removeGeofence({ id: workflowId });
  await withListenerLock(async () => {
    armedWorkflows.delete(workflowId);
    // Only once NO workflow has an armed fence left: leaving the listener up
    // for one still-armed workflow while tearing it down for another that
    // just stopped would silence flush-on-reconnect for the one still
    // running. Nothing left to flush for once the set is empty, though, and
    // leaving the listener attached then would wake the app on every network
    // change for a feature every workflow has turned off.
    if (armedWorkflows.size === 0 && networkListener) {
      await networkListener.remove();
      networkListener = null;
    }
  });
}
