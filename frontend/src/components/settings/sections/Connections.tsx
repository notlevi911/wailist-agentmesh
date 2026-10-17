"use client";
import { useEffect, useRef, useState } from "react";
import { oauth2, type OAuthCredentialSummary } from "@/lib/api";
import { SettingsSection, formatDateUTC, useActiveForm } from "../ui";

function Connection({ credential, removed, disabled }: { credential: OAuthCredentialSummary; removed: (id: string) => void; disabled: boolean }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const active = useActiveForm();
  async function disconnect() {
    if (disabled || pending.current) return;
    pending.current = true; setBusy(true); setError("");
    try {
      await oauth2.deleteCredential(credential.id);
      if (active.current) removed(credential.id);
    } catch {
      if (active.current) setError("Could not disconnect this account. Try again.");
    } finally {
      pending.current = false;
      if (active.current) setBusy(false);
    }
  }
  return <li>
    <p><strong>{credential.accountLabel || credential.provider}</strong><br />
      {credential.provider}, connected {formatDateUTC(credential.createdAt)} (UTC)</p>
    {confirming ? <>
      <p>Workflows using this connection will need to reconnect.</p>
      <button type="button" disabled={disabled || busy} onClick={() => void disconnect()}>{busy ? "Disconnecting..." : "Confirm disconnect"}</button>
      <button type="button" disabled={disabled || busy} onClick={() => setConfirming(false)}>Cancel</button>
    </> : <button type="button" disabled={disabled} onClick={() => setConfirming(true)} aria-label={`Disconnect ${credential.accountLabel || credential.provider}`}>Disconnect</button>}
    {error && <p role="alert">{error}</p>}
  </li>;
}

export function ConnectionsSection({ disabled }: { disabled: boolean }) {
  const [credentials, setCredentials] = useState<OAuthCredentialSummary[] | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    oauth2.listCredentials().then((list) => { if (active) setCredentials(list); })
      .catch(() => { if (active) setError("Could not load connected accounts."); });
    return () => { active = false; };
  }, [attempt]);
  return <SettingsSection id="connections" title="Connected accounts">
    <p>Manage accounts connected from workflow nodes. Disconnecting removes the saved connection here; it does not delete the provider account or revoke access at the provider.</p>
    {error ? <div role="alert"><p>{error}</p><button type="button" disabled={disabled} onClick={() => { setError(""); setAttempt((n) => n + 1); }}>Retry connections</button></div> :
      credentials === null ? <p role="status">Loading connections...</p> :
      credentials.length === 0 ? <p>No accounts connected here. Connect an account from the workflow node that needs it.</p> :
      <ul>{credentials.map((credential) => <Connection key={credential.id} credential={credential} disabled={disabled}
        removed={(id) => setCredentials((list) => list?.filter((entry) => entry.id !== id) ?? [])} />)}</ul>}
    <p>Credentials stored directly on connector nodes are managed on those nodes.</p>
  </SettingsSection>;
}

