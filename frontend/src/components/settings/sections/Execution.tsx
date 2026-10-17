"use client";
import { useRef, useState, type FormEvent } from "react";
import { SettingsSection, parseUsdInput, usdInput, useActiveForm } from "../ui";
import type { PreferencesProps } from "./Billing";

export function ExecutionSection({ value, save, disabled }: PreferencesProps) {
  const [enabled, setEnabled] = useState(value.maxCallSpendUsdMicros !== null);
  const [amount, setAmount] = useState(usdInput(value.maxCallSpendUsdMicros ?? value.limits.minCallSpendUsdMicros));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const pending = useRef(false);
  const active = useActiveForm();
  const micros = parseUsdInput(amount);
  const valid = !enabled || (micros !== null && micros >= value.limits.minCallSpendUsdMicros && micros <= value.limits.maxCallSpendUsdMicros);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || disabled || pending.current) return;
    pending.current = true; setBusy(true); setMessage(""); setError("");
    try {
      await save({ maxCallSpendUsdMicros: enabled ? micros : null });
      if (active.current) setMessage("Per-call limit saved.");
    } catch (err) {
      if (active.current) setError(err instanceof Error ? err.message : "Could not save per-call limit.");
    } finally {
      pending.current = false;
      if (active.current) setBusy(false);
    }
  }
  return <SettingsSection id="execution" title="Per-call spend limit">
    <p>Block a call when its charge to account credits exceeds this amount, including platform fees. Each run uses the limit saved when its first paid step starts. Changes apply to later runs and resumed attempts.</p>
    <p>This is not a total workflow budget. Provider charges using your own keys and machine rental credits are separate.</p>
    <form onSubmit={submit}>
      <label><input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} disabled={disabled || busy} /> Limit each paid call</label>
      {enabled && <>
        <label htmlFor="settings-ceiling">Maximum per call (USD)</label>
        <input id="settings-ceiling" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} disabled={disabled || busy} required />
        <p>From {usdInput(value.limits.minCallSpendUsdMicros)} to {usdInput(value.limits.maxCallSpendUsdMicros)} USD, up to six decimal places.</p>
        {!valid && <p role="alert">Enter an amount within the supported range.</p>}
      </>}
      <button type="submit" disabled={!valid || disabled || busy}>{busy ? "Saving..." : "Save per-call limit"}</button>
      {message && <p role="status">{message}</p>}
      {error && <p role="alert">{error}</p>}
    </form>
  </SettingsSection>;
}

