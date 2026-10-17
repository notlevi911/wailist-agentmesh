"use client";
import Link from "next/link";
import { useRef, useState, type FormEvent } from "react";
import type { UserSettings, UserSettingsPatch } from "@/lib/api";
import { SettingsSection, parseUsdInput, usdInput, useActiveForm } from "../ui";

export type PreferencesProps = { value: UserSettings; save: (patch: UserSettingsPatch) => Promise<void>; disabled: boolean };

export function BillingSection({ value, save, disabled }: PreferencesProps) {
  const [amount, setAmount] = useState(usdInput(value.lowBalanceUsdMicros));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const pending = useRef(false);
  const active = useActiveForm();
  const micros = parseUsdInput(amount);
  const valid = micros !== null && micros <= value.limits.maxLowBalanceUsdMicros;
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || disabled || pending.current) return;
    pending.current = true; setBusy(true); setError(""); setMessage("");
    try {
      await save({ lowBalanceUsdMicros: micros });
      if (active.current) setMessage("Balance warning saved.");
    } catch (err) {
      if (active.current) setError(err instanceof Error ? err.message : "Could not save balance warning.");
    } finally {
      pending.current = false;
      if (active.current) setBusy(false);
    }
  }
  return <SettingsSection id="billing" title="Balance warning">
    <p>Show a low-balance warning below this amount. This does not add funds or charge a payment method.</p>
    <form onSubmit={submit}>
      <label htmlFor="settings-threshold">Low-balance threshold (USD)</label>
      <input id="settings-threshold" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} disabled={disabled || busy} required />
      <p>Use up to six decimal places. Set zero to disable the warning.</p>
      {!valid && <p role="alert">Enter an amount from 0 to {usdInput(value.limits.maxLowBalanceUsdMicros)} USD.</p>}
      <button type="submit" disabled={!valid || disabled || busy}>{busy ? "Saving..." : "Save balance warning"}</button>
      {message && <p role="status">{message}</p>}
      {error && <p role="alert">{error}</p>}
    </form>
    <p><Link href="/billing">Balance and top-ups</Link></p>
  </SettingsSection>;
}

