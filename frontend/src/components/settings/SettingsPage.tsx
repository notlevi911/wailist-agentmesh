"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Topbar } from "@/components/Topbar";
import { getAccountRevision, publishAccountThreshold, useAuth } from "@/hooks/useAuth";
import { settings, type UserSettings, type UserSettingsPatch } from "@/lib/api";
import { AccountSection } from "./sections/Account";
import { BillingSection } from "./sections/Billing";
import { ExecutionSection } from "./sections/Execution";
import { ConnectionsSection } from "./sections/Connections";
import { DeveloperSection } from "./sections/Developer";
import { SettingsSection, useActiveForm } from "./ui";
import styles from "./SettingsPage.module.css";

export function SettingsPage() {
  const router = useRouter();
  const { user, loading, offline, retry, deleteAccount, completeOnboarding } = useAuth();
  const [draft, setDraft] = useState({ owner: "", confirmation: "", password: "" });
  const confirmation = draft.owner === user?.id ? draft.confirmation : "";
  const password = draft.owner === user?.id ? draft.password : "";
  function setConfirmation(value: string) {
    setDraft((previous) => ({ owner: user?.id ?? "", confirmation: value, password: previous.owner === user?.id ? previous.password : "" }));
  }
  function setPassword(value: string) {
    setDraft((previous) => ({ owner: user?.id ?? "", password: value, confirmation: previous.owner === user?.id ? previous.confirmation : "" }));
  }
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [cleanupIncomplete, setCleanupIncomplete] = useState(false);
  const submitting = useRef(false);

  useEffect(() => {
    if (!loading && !offline && !user && !submitting.current) {
      router.replace("/signin?next=%2Fsettings");
    }
  }, [loading, offline, user, router]);

  useEffect(() => {
    if (!loading && !offline && user && window.location.hash === "#delete-title") {
      document.getElementById("delete-title")?.scrollIntoView({ block: "start" });
    }
  }, [loading, offline, user]);

  const canDelete = !loading && !offline && user?.hasPassword !== undefined &&
    confirmation === "DELETE" && (!user.hasPassword || password.length > 0);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canDelete || submitting.current) return;
    submitting.current = true;
    setDeleting(true);
    setError("");
    try {
      const incomplete = await deleteAccount(confirmation, password);
      setPassword("");
      if (incomplete) setCleanupIncomplete(true);
      else router.replace("/signin");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete account. Try again.");
      setPassword("");
      submitting.current = false;
      setDeleting(false);
    }
  }

  return (
    <div className={`am-viewport ${styles.viewport}`}>
      <Topbar />
      <div className={styles.scroll}>
        <main className={styles.page}>
          <Link href="/account" className={styles.back}>Back to account</Link>
          <h1>Settings</h1>
          {cleanupIncomplete ? (
            <div role="alert" className={styles.card}>
              <h2>Account deleted</h2>
              <p>
                Your server account is deleted, but this device could not clear all local data.
                Clear this site&apos;s stored data in your browser, or clear the Android app&apos;s
                storage in system settings before using another account.
              </p>
              <Link href="/signin">Go to sign in</Link>
            </div>
          ) : loading ? <p role="status">Loading account...</p> : offline ? (
            <div role="alert">
              <p>Connect to the server to manage your account.</p>
              <button type="button" onClick={retry}>Retry</button>
            </div>
          ) : user && (
            <div key={user.id}>
            <AccountSection user={user} saveProfile={completeOnboarding} disabled={deleting} />
            <Preferences owner={user.id} disabled={deleting} />
            <ConnectionsSection disabled={deleting} />
            <DeveloperSection />
            <section aria-labelledby="delete-title" className={styles.card}>
              <h2 id="delete-title">Delete account</h2>
              <p>
                Permanently delete <strong>{user.email}</strong> and its workflows,
                run history, saved credentials and account records. This cannot be undone.
                Any remaining credit balance will be lost.
              </p>
              <p>Release all active machines in the Tendril console before deleting your account.</p>
              <p>
                <Link href="/delete-account">What gets deleted and how to get help</Link>
              </p>
              {user.hasPassword === undefined ? (
                <p role="alert">Could not verify account requirements. Reload this page to try again.</p>
              ) : (
                <form onSubmit={submit} aria-busy={deleting}>
                  <label htmlFor="delete-confirmation">Type DELETE to confirm</label>
                  <input
                    id="delete-confirmation"
                    value={confirmation}
                    onChange={(event) => setConfirmation(event.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                    disabled={deleting}
                    required
                  />
                  {user.hasPassword && (
                    <>
                      <label htmlFor="delete-password">Current password</label>
                      <input
                        id="delete-password"
                        type="password"
                        autoComplete="current-password"
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                        disabled={deleting}
                        required
                      />
                    </>
                  )}
                  {error && <p role="alert" className={styles.error}>{error}</p>}
                  <button type="submit" className={styles.deleteButton} disabled={!canDelete || deleting}>
                    {deleting ? "Deleting account..." : "Delete my account"}
                  </button>
                </form>
              )}
            </section>
            </div>
          )}
        </main>
      </div>
    </div>
  );
}

function Preferences({ owner, disabled }: { owner: string; disabled: boolean }) {
  const [value, setValue] = useState<UserSettings | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const active = useActiveForm();
  useEffect(() => {
    let current = true;
    const revision = getAccountRevision();
    settings.get().then((loaded) => {
      if (!current || revision !== getAccountRevision()) return;
      setValue(loaded);
      publishAccountThreshold(owner, loaded.lowBalanceUsdMicros, revision);
    }).catch(() => { if (current) setError("Could not load spending preferences."); });
    return () => { current = false; };
  }, [owner, attempt]);
  async function save(patch: UserSettingsPatch) {
    const revision = getAccountRevision();
    const saved = await settings.update(patch);
    if (!active.current || revision !== getAccountRevision()) return;
    setValue((previous) => previous && ({
      ...previous,
      ...(patch.lowBalanceUsdMicros !== undefined ? { lowBalanceUsdMicros: saved.lowBalanceUsdMicros } : {}),
      ...("maxCallSpendUsdMicros" in patch ? { maxCallSpendUsdMicros: saved.maxCallSpendUsdMicros } : {}),
    }));
    if (patch.lowBalanceUsdMicros !== undefined) publishAccountThreshold(owner, saved.lowBalanceUsdMicros, revision);
  }
  if (error) return <SettingsSection id="preferences" title="Spending preferences">
    <p role="alert">{error}</p>
    <button type="button" disabled={disabled} onClick={() => { setError(""); setAttempt((n) => n + 1); }}>Retry preferences</button>
  </SettingsSection>;
  if (!value) return <p role="status">Loading spending preferences...</p>;
  return <><BillingSection value={value} save={save} disabled={disabled} /><ExecutionSection value={value} save={save} disabled={disabled} /></>;
}
