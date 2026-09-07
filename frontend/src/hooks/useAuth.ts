"use client";
import { useState, useEffect, useCallback } from "react";
import { auth, AuthUser } from "@/lib/api";
import { IS_NATIVE, setAuthToken, authReady } from "@/lib/nativeAuth";
import { resetCredits } from "@/lib/credits/store";

const UI_COOKIE = "agentmesh_ui";
const TTL = 60 * 60 * 24 * 7; // 7 days -- matches backend JWT TTL

function setUICookie() {
  document.cookie = `${UI_COOKIE}=1; Path=/; SameSite=Lax; Max-Age=${TTL}`;
}

function clearUICookie() {
  document.cookie = `${UI_COOKIE}=; Path=/; SameSite=Lax; Max-Age=0`;
}

export function useAuth() {
  const [signedIn, setSignedIn] = useState(false);
  const [loading, setLoading] = useState(true);
  const [user, setUser] = useState<AuthUser | null>(null);

  useEffect(() => {
    // On native, wait for NativeBoot to finish restoring (or fail to
    // restore) the persisted token before asking who's signed in -- calling
    // auth.me() first would race it and 401 with no Authorization header
    // attached yet.
    authReady
      .then(() => auth.me())
      .then((u) => {
        setUICookie();
        setSignedIn(true);
        setUser(u);
      })
      .catch(() => {
        clearUICookie();
        setSignedIn(false);
        setUser(null);
      })
      .finally(() => setLoading(false));
  }, []);

  const signIn = useCallback(async (email: string, password: string) => {
    const token = await auth.signIn(email, password);
    // Hand the session to the native shell so it survives the app being
    // closed. Inert on the web, where the token is null and the HttpOnly
    // cookie is the session. Dynamic and IS_NATIVE-guarded for the same
    // reason as NativeBoot: a browser build must not pull Capacitor in.
    if (token && IS_NATIVE) {
      setAuthToken(token);
      // Logged, not swallowed: a failed native persist (e.g. a Keystore
      // write error) would otherwise leave the UI showing signed-in while
      // the shell never actually saved the token, silently failing to
      // survive the app being killed.
      void import("@/native")
        .then(({ shell }) => shell.onSignedIn(token))
        .catch((err) => console.error("native shell failed to persist sign-in", err));
    }
    setUICookie();
    setSignedIn(true);
  }, []);

  const signUp = useCallback(
    async (email: string, password: string, name: string, org: string) => {
      const token = await auth.signUp(email, password, name, org);
      if (token && IS_NATIVE) {
        setAuthToken(token);
        void import("@/native")
          .then(({ shell }) => shell.onSignedIn(token))
          .catch((err) => console.error("native shell failed to persist sign-in", err));
      }
      setUICookie();
      setSignedIn(true);
    },
    [],
  );

  const clearLocalSession = useCallback(() => {
    if (IS_NATIVE) {
      setAuthToken(null);
      // Logged for the mirror-image reason: a shared device that fails to
      // clear the persisted token would otherwise silently keep the old
      // user's session live in Keystore after the UI has already moved on.
      void import("@/native")
        .then(({ shell }) => shell.onSignedOut())
        .catch((err) => console.error("native shell failed to clear sign-out", err));
    }
    clearUICookie();
    // Balance and purchase history are module singletons that survive a
    // client-side route change, so they have to be dropped explicitly here --
    // otherwise the next account to sign in in this tab sees the previous
    // one's money until its own fetch lands. resetCredits also bumps the
    // store's epoch, which discards any refresh still in flight.
    resetCredits();
    setSignedIn(false);
    setUser(null);
  }, []);

  const signOut = useCallback(async () => {
    // The local teardown below runs in a finally: auth.signOut() is a network
    // call, and letting a failed request skip the cleanup would leave the
    // previous account's balance, purchase history and (on native) persisted
    // token live in a UI that has already moved on. Signing out locally is
    // the part that must not be optional -- the server-side cookie clear is
    // best-effort by comparison.
    try {
      await auth.signOut();
    } catch (err) {
      console.error("sign-out request failed; clearing local session anyway", err);
    } finally {
      clearLocalSession();
    }
  }, [clearLocalSession]);

  // Completes the post-OAuth onboarding prompt (or a later profile edit) —
  // updates the backend then reflects it locally so callers don't need a
  // full re-fetch just to clear needsOnboarding.
  const completeOnboarding = useCallback(async (name: string, org: string) => {
    const updated = await auth.updateProfile(name, org);
    setUser(updated);
  }, []);

  return {
    signedIn,
    loading,
    user,
    signIn,
    signUp,
    signOut,
    completeOnboarding,
  };
}
