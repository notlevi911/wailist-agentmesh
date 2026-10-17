"use client";
import { useRef, useState, type FormEvent } from "react";
import { auth, type AuthUser } from "@/lib/api";
import { PasswordField } from "@/components/ui/PasswordField";
import { scorePassword } from "@/lib/password/strength";
import { SettingsSection, formatDateUTC, useActiveForm } from "../ui";

export function AccountSection({ user, saveProfile, disabled }: {
  user: AuthUser; saveProfile: (name: string, org: string) => Promise<void>; disabled: boolean;
}) {
  const [name, setName] = useState(user.name);
  const [org, setOrg] = useState(user.orgName);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const pending = useRef(false);
  const active = useActiveForm();
  async function save(event: FormEvent) {
    event.preventDefault();
    if (pending.current || disabled || !name.trim()) return;
    pending.current = true; setBusy(true); setMessage(""); setError("");
    try {
      await saveProfile(name.trim(), org.trim());
      if (active.current) setMessage("Profile saved.");
    } catch (err) {
      if (active.current) setError(err instanceof Error ? err.message : "Could not save profile.");
    } finally {
      pending.current = false;
      if (active.current) setBusy(false);
    }
  }
  return <SettingsSection id="account" title="Account">
    <p>{user.email}</p>
    <p>Member since {formatDateUTC(user.createdAt)} (UTC).</p>
    <form onSubmit={save} aria-label="Profile">
      <fieldset disabled={disabled || busy}>
        <label htmlFor="settings-name">Name</label>
        <input id="settings-name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} required />
        <label htmlFor="settings-org">Organization</label>
        <input id="settings-org" autoComplete="organization" value={org} onChange={(e) => setOrg(e.target.value)} />
        <button type="submit" disabled={!name.trim()}>{busy ? "Saving..." : "Save profile"}</button>
      </fieldset>
      {message && <p role="status">{message}</p>}
      {error && <p role="alert">{error}</p>}
    </form>
    {user.hasPassword === true ? <PasswordChange disabled={disabled} /> :
      <p>{user.hasPassword === false ? "You sign in through a provider. Manage your password with that provider." : "Password requirements are unavailable. Reload to try again."}</p>}
  </SettingsSection>;
}

function PasswordChange({ disabled }: { disabled: boolean }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const pending = useRef(false);
  const active = useActiveForm();
  const strength = scorePassword(next);
  const valid = !!current && strength.valid && next === confirmation;
  async function save(event: FormEvent) {
    event.preventDefault();
    if (!valid || disabled || pending.current) return;
    pending.current = true; setBusy(true); setError(""); setMessage("");
    try {
      await auth.changePassword(current, next);
      if (active.current) setMessage("Password changed. Existing signed-in sessions remain active.");
    } catch (err) {
      if (active.current) setError(err instanceof Error ? err.message : "Could not change password.");
    } finally {
      pending.current = false;
      if (active.current) { setBusy(false); setCurrent(""); setNext(""); setConfirmation(""); }
    }
  }
  return <form onSubmit={save} aria-label="Change password">
    <h3>Change password</h3>
    <fieldset disabled={disabled || busy}>
      <PasswordField id="settings-current-password" label="Current password for password change" autoComplete="current-password"
        value={current} onChange={(e) => setCurrent(e.target.value)} disabled={disabled || busy} required />
      <PasswordField id="settings-new-password" label="New password" autoComplete="new-password" value={next}
        onChange={(e) => setNext(e.target.value)} disabled={disabled || busy} aria-describedby="password-hint" required />
      <p id="password-hint">Use 8 to 72 bytes. Non-ASCII characters can use multiple bytes.</p>
      {next && <p>{strength.tooLong ? "Password exceeds 72 bytes." : !strength.valid ? "Password is too short." :
        `Length and variety: ${strength.score} of 4. This does not check for common or breached passwords.`}</p>}
      <PasswordField id="settings-repeat-password" label="Repeat new password" autoComplete="new-password" value={confirmation}
        onChange={(e) => setConfirmation(e.target.value)} disabled={disabled || busy} required />
      {confirmation && confirmation !== next && <p>Passwords do not match.</p>}
      <button type="submit" disabled={!valid}>{busy ? "Changing..." : "Change password"}</button>
    </fieldset>
    {message && <p role="status">{message}</p>}
    {error && <p role="alert">{error}</p>}
  </form>;
}

