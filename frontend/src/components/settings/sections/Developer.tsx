"use client";
import { useEffect, useState, useSyncExternalStore } from "react";
import { BASE, workflows } from "@/lib/api";
import type { Workflow } from "@/lib/types";
import { SettingsSection } from "../ui";

const subscribeOrigin = () => () => {};

export function DeveloperSection() {
  const [list, setList] = useState<Workflow[] | null>(null);
  const [selected, setSelected] = useState("");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState("");
  const origin = useSyncExternalStore(subscribeOrigin, () => window.location.origin, () => "");
  useEffect(() => {
    let active = true;
    workflows.list().then((rows) => {
      if (!active) return;
      setList(rows); setSelected(rows[0]?.id ?? "");
    }).catch(() => { if (active) setError("Could not load workflows."); });
    return () => { active = false; };
  }, [attempt]);
  const url = selected && BASE ? `${BASE.startsWith("http") ? BASE : origin + BASE}/run/${encodeURIComponent(selected)}` : "";
  async function copy() {
    try { await navigator.clipboard.writeText(url); setCopied("Endpoint copied."); }
    catch { setCopied("Copy unavailable. Select and copy the endpoint field."); }
  }
  return <SettingsSection id="developer" title="Workflow triggers">
    <p>Only deployed workflows with a webhook trigger accept this endpoint. Send the trigger&apos;s X-Webhook-Secret header, available on its canvas node. An accepted request starts a run and can spend your credits.</p>
    {error ? <div role="alert"><p>{error}</p><button type="button" onClick={() => { setError(""); setAttempt((n) => n + 1); }}>Retry workflows</button></div> :
      list === null ? <p role="status">Loading workflows...</p> :
      list.length === 0 ? <p>No workflows yet.</p> : <>
        <label htmlFor="settings-workflow">Workflow</label>
        <select id="settings-workflow" value={selected} onChange={(e) => { setSelected(e.target.value); setCopied(""); }}>
          {list.map((workflow) => <option key={workflow.id} value={workflow.id}>{workflow.name}</option>)}
        </select>
        {url ? <>
          <label htmlFor="settings-endpoint">Webhook endpoint</label>
          <input id="settings-endpoint" readOnly value={`POST ${url}`} onFocus={(e) => e.currentTarget.select()} />
          <button type="button" onClick={() => void copy()}>Copy endpoint</button>
          {copied && <p role="status">{copied}</p>}
        </> : <p>Connect a backend to view trigger endpoints.</p>}
      </>}
  </SettingsSection>;
}

