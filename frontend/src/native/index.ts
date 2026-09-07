// The native shell's entry point.
//
// Called once from the web bundle when it is running inside Capacitor. Its job
// is to reconnect two halves that are otherwise unaware of each other: the
// session the app persisted, and whatever the OS queued while the app was
// closed.
import { loadToken, saveToken, clearToken } from "./auth";
import { flush, start, stop } from "./geofence";
import { setGeofence, clearGeofence } from "./api";

export interface NativeShell {
  onSignedIn(token: string): Promise<void>;
  onSignedOut(): Promise<void>;
  setGeofence(
    workflowId: string,
    fence: { lat: number; lng: number; radiusM: number },
  ): Promise<boolean>;
  clearGeofence(workflowId: string): Promise<void>;
}

/**
 * Boots the shell. Safe to call more than once.
 *
 * Returns the token it restored, so the caller can hydrate the web bundle's
 * API client with it -- the shell deliberately does not reach into that module
 * itself, because the direction of the dependency matters: the web bundle
 * knows nothing about Capacitor, and keeping it that way is what lets the same
 * code run in a browser.
 */
export async function boot(): Promise<string | null> {
  const token = await loadToken();
  // Drain anything GeofenceReceiver appended while there was no WebView alive.
  // Not awaited: a flush that cannot reach the network keeps its queue, and
  // the app must still start.
  void flush();
  return token;
}

export const shell: NativeShell = {
  async onSignedIn(token: string) {
    await saveToken(token);
    // A queue that could not flush while signed out is now deliverable.
    void flush();
  },

  async onSignedOut() {
    await clearToken();
  },

  async setGeofence(workflowId, fence) {
    // Server first. If the backend rejects the zone -- undeployed workflow,
    // radius out of range -- registering it with the OS would leave the device
    // watching a boundary the server will never act on.
    await setGeofence(workflowId, fence);
    return start({ workflowId, ...fence });
  },

  async clearGeofence(workflowId) {
    // Server first, same reasoning as setGeofence above but in the removal
    // direction: if this throws, the device keeps watching a boundary the
    // server still has armed, which is consistent (if now stale to the user's
    // intent) rather than the alternative -- disarming the OS watch first and
    // then failing to tell the server, which leaves the server believing a
    // fence is live that will never fire again.
    await clearGeofence(workflowId);
    await stop(workflowId);
  },
};
