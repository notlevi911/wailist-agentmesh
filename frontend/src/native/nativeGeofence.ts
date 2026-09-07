// The bridge to our own Android geofence plugin.
//
// Registered by hand rather than imported as a package because the plugin
// lives in the app module (android/app/src/main/java/ai/agentmesh/app), not in
// node_modules. It wraps Android's GeofencingClient, which does enter/leave on
// a circle for free and at OS level -- the reason this project does not carry
// a commercial background-tracking SDK for what is, in the end, one circle.
import { registerPlugin } from "@capacitor/core";

export interface PermissionResult {
  granted: boolean;
  /** Which step was refused, when it was. */
  reason?: "foreground" | "background";
}

export interface NativeGeofence {
  /** The fence id IS the workflow id, so a crossing needs no lookup table. */
  addGeofence(opts: {
    id: string;
    lat: number;
    lng: number;
    radiusM: number;
  }): Promise<void>;
  removeGeofence(opts: { id: string }): Promise<void>;
  hasPermission(): Promise<PermissionResult>;
  /** Requests foreground then background, in that order -- Android requires it. */
  requestPermission(): Promise<PermissionResult>;
  /** This app's settings page -- the only way back after a refusal. */
  openSettings(): Promise<void>;
  /**
   * Atomically reads and clears GeofenceReceiver's native-side queue.
   * Native, not @capacitor/preferences, because that queue is also written
   * by GeofenceReceiver outside any WebView -- reading it any other way
   * reintroduces the race this method exists to close. See queue.ts.
   */
  drainNativeQueue(): Promise<{ value: string }>;
}

export const Geofence = registerPlugin<NativeGeofence>("Geofence");
