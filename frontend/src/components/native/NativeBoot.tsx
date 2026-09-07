"use client";
import { useEffect } from "react";
import { IS_NATIVE, setAuthToken, markAuthReady } from "@/lib/nativeAuth";

// Restores the session the native shell persisted, once, on first mount.
//
// The join between two halves that stay ignorant of each other: the shell owns
// durable storage and the OS, the web bundle owns the API client. The token is
// handed across here rather than either side importing the other.
//
// The import is dynamic and guarded by IS_NATIVE, a build-time constant. In a
// browser build that branch is dead, so the Capacitor chunk is never fetched;
// on device it loads once. A static import would pull @capacitor/* into every
// page of the web app to satisfy a branch that never runs there.
//
// boot() talking to the Capacitor bridge is the one step here with no
// built-in timeout of its own -- a WebView-bridge call that never calls back
// (a known failure mode, not a hypothetical one) would otherwise leave
// authReady unresolved forever and useAuth stuck showing a permanent loading
// spinner instead of ever falling back to signed-out. Racing it against a
// timeout turns "hangs forever" into "fails after 10s", which the existing
// .catch/.finally below already handle correctly.
const BOOT_TIMEOUT_MS = 10_000;
function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("native boot timed out")), ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (err) => {
        clearTimeout(timer);
        reject(err);
      },
    );
  });
}

// Renders nothing. Mounted in the root layout because the session has to be
// restored before any page makes its first authenticated call, not when some
// particular screen happens to appear.
export function NativeBoot() {
  useEffect(() => {
    if (!IS_NATIVE) return;
    let cancelled = false;
    void withTimeout(import("@/native").then(({ boot }) => boot()), BOOT_TIMEOUT_MS)
      .then((token) => {
        if (!cancelled && token) setAuthToken(token);
      })
      .catch((err) => {
        // A shell that cannot boot must not take the app down with it. The
        // viewer still works signed out; the alternative is a blank screen
        // with the reason visible only in logcat.
        console.error("native shell failed to boot", err);
      })
      .finally(() => {
        // Unconditional, unlike the setAuthToken call above: authReady is a
        // module-level, one-shot signal shared with every mount of this
        // component, not per-mount state. Gating it on `cancelled` would
        // permanently starve useAuth's auth.me() call if this effect were
        // ever cleaned up before boot() settled -- not reachable today with
        // NativeBoot mounted once in the root layout, but nothing enforces
        // that invariant here, and a signal that can hang forever is cheap
        // to just not build in the first place.
        markAuthReady();
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return null;
}
