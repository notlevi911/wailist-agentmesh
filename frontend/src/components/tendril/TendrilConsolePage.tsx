"use client";
import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Topbar } from "@/components/Topbar";
import { Tag, ghostBtnSm } from "@/components/ui";
import { TerminalTab } from "@/components/canvas/TerminalTab";
import {
  tendril as tendrilApi,
  estimateLeaseCostUSD,
  estimateLeaseHoursCostUSD,
  TendrilMachine,
  TendrilLeaseSummary,
  TendrilReleaseResult,
  TendrilTopupResult,
} from "@/lib/tendril";

// Tendril's own accent, shared with the canvas node type (PalettePanel /
// Inspector) so this reads as the same feature rather than a disconnected
// sub-app.
const MAGENTA = "#E879F9";
const MAGENTA_DIM = "rgba(232, 121, 249, 0.08)";
const GREEN = "#34D399";

function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${sec}s`;
  return `${sec}s`;
}

// reservedUntilMs is startedAt + the hours THIS renter actually picked —
// never Tendril's own fundedUntil, which reflects how long the shared pool
// wallet (every AgentMesh user's topups combined) could fund the machine,
// not what this one rent reserved. Confirmed live: a 1-hour rent reported a
// ~2-hour fundedUntil.
//
// Passing that point does NOT mean the machine stopped either way — Tendril's
// own docs: reaching the funded window doesn't kill the session outright, it
// re-reads the live balance and only opens an (unfunded, platform-absorbed)
// grace window once credit is truly gone. So the label past that point
// describes what's true — still metering, past the reserved estimate — not
// "expired".
function formatRemaining(reservedUntilMs: number, now: number): string {
  const remainingMs = reservedUntilMs - now;
  if (remainingMs <= 0) return "past reserved estimate — still billing live";
  return `${formatDuration(remainingMs / 1000)} left on the reservation`;
}

// ── Shared chrome ───────────────────────────────────────────────────────────
function Panel({
  children,
  style,
}: {
  children: React.ReactNode;
  style?: React.CSSProperties;
}) {
  return (
    <div
      style={{
        background: "var(--bg-elev-1)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-3)",
        ...style,
      }}
    >
      {children}
    </div>
  );
}

function PanelLabel({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        textTransform: "uppercase",
        letterSpacing: "0.1em",
        color: "var(--fg-dim)",
      }}
    >
      {children}
    </div>
  );
}

function Dot({ color }: { color: string }) {
  return (
    <span
      className="pulse"
      style={{
        width: 7,
        height: 7,
        borderRadius: 999,
        background: color,
        boxShadow: `0 0 6px ${color}`,
        flexShrink: 0,
      }}
    />
  );
}

const monoInput: React.CSSProperties = {
  height: 32,
  padding: "0 10px",
  background: "var(--bg)",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-1)",
  color: "var(--fg)",
  fontSize: 13,
  fontFamily: "var(--font-mono)",
  outline: "none",
};

function nameplateButton(disabled: boolean): React.CSSProperties {
  return {
    height: 32,
    padding: "0 16px",
    fontSize: 11,
    fontWeight: 700,
    letterSpacing: "0.08em",
    textTransform: "uppercase",
    fontFamily: "var(--font-mono)",
    background: disabled ? "var(--bg-elev-2)" : MAGENTA,
    border: `1px solid ${disabled ? "var(--border-strong)" : MAGENTA}`,
    borderRadius: "var(--r-1)",
    color: disabled ? "var(--fg-dim)" : "#1a0a1a",
    cursor: disabled ? "default" : "pointer",
    whiteSpace: "nowrap",
  };
}

// Same link treatment LogDrawer uses for a step's settlement txid, so a
// real on-chain reference reads the same way everywhere in the app.
const txLinkStyle: React.CSSProperties = {
  color: MAGENTA,
  textDecoration: "underline",
  fontFamily: "var(--font-mono)",
  fontSize: 11,
  whiteSpace: "nowrap",
};

function quietButton(danger = false): React.CSSProperties {
  return {
    height: 32,
    padding: "0 14px",
    fontSize: 11,
    fontWeight: 600,
    letterSpacing: "0.04em",
    textTransform: "uppercase",
    fontFamily: "var(--font-mono)",
    background: "transparent",
    border: `1px solid ${danger ? "var(--danger)" : "var(--border-strong)"}`,
    borderRadius: "var(--r-1)",
    color: danger ? "var(--danger)" : "var(--fg-muted)",
    cursor: "pointer",
  };
}

// A dedicated control panel for Tendril, opened from the workflow list like
// any other workflow row — but once inside, there is no graph: buying
// credit, renting a machine, running a job, and releasing are each one
// press, with no chaining between them. The money still moves through
// AgentMesh's own wallet (Wallet 1 -> Wallet 2 -> Tendril), same path any
// canvas workflow would use — this is a different front door onto the same
// relay, not a different payment path.
export function TendrilConsolePage() {
  const router = useRouter();
  const [credit, setCredit] = useState<number | null>(null);
  const [machines, setMachines] = useState<TendrilMachine[]>([]);
  const [leases, setLeases] = useState<TendrilLeaseSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [showTerminal, setShowTerminal] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  const [addingCredit, setAddingCredit] = useState(false);
  const [topupAmount, setTopupAmount] = useState("10");
  const [topupBusy, setTopupBusy] = useState(false);
  const [topupMsg, setTopupMsg] = useState<string | null>(null);
  const [topupResult, setTopupResult] = useState<TendrilTopupResult | null>(null);

  const [rentHours, setRentHours] = useState("1");
  const [rentBusy, setRentBusy] = useState<string | null>(null);
  const [rentMsg, setRentMsg] = useState<string | null>(null);

  const [payload, setPayload] = useState("print(sum(range(100)))");
  const [runBusy, setRunBusy] = useState(false);
  const [runMsg, setRunMsg] = useState<string | null>(null);
  const [runOutput, setRunOutput] = useState<string | null>(null);

  const [releaseBusy, setReleaseBusy] = useState(false);
  const [releaseResult, setReleaseResult] = useState<TendrilReleaseResult | null>(null);

  const refresh = useCallback(async () => {
    const [c, m, l] = await Promise.allSettled([
      tendrilApi.credit(),
      tendrilApi.machines(),
      tendrilApi.leases(),
    ]);
    if (c.status === "fulfilled") setCredit(c.value);
    if (m.status === "fulfilled") setMachines(m.value);
    if (l.status === "fulfilled") setLeases(l.value);
    setLoading(false);
  }, []);

  useEffect(() => {
    // Fetch-on-mount + poll — the "subscribe to an external system" case.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    refresh();
    const poll = setInterval(refresh, 15_000);
    return () => clearInterval(poll);
  }, [refresh]);

  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, []);

  const activeLease = leases[0] ?? null;
  const hours = parseFloat(rentHours) || 0;

  const handleTopup = async () => {
    setTopupBusy(true);
    setTopupMsg(null);
    setTopupResult(null);
    try {
      const result = await tendrilApi.topup(topupAmount);
      setTopupResult(result);
      setAddingCredit(false);
      await refresh();
    } catch (e) {
      setTopupMsg(e instanceof Error ? e.message : "Could not add credit.");
    } finally {
      setTopupBusy(false);
    }
  };

  const handleRent = async (machineId: string) => {
    setRentBusy(machineId);
    setRentMsg(null);
    setReleaseResult(null);
    try {
      await tendrilApi.rent(machineId, rentHours);
      await refresh();
    } catch (e) {
      setRentMsg(e instanceof Error ? e.message : "Could not rent that machine.");
    } finally {
      setRentBusy(null);
    }
  };

  const handleRun = async () => {
    setRunBusy(true);
    setRunMsg(null);
    setRunOutput(null);
    try {
      const result = await tendrilApi.run(payload);
      setRunOutput(JSON.stringify(result, null, 2));
      await refresh();
    } catch (e) {
      setRunMsg(e instanceof Error ? e.message : "Run failed.");
    } finally {
      setRunBusy(false);
    }
  };

  const handleRelease = async () => {
    if (!activeLease) return;
    setReleaseBusy(true);
    try {
      const result = await tendrilApi.release(activeLease.id);
      setReleaseResult(result);
      setShowTerminal(false);
      await refresh();
    } finally {
      setReleaseBusy(false);
    }
  };

  return (
    <div
      className="am-viewport"
      style={{
        height: "100dvh",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background: "var(--bg)",
      }}
    >
      <Topbar />
      <div style={{ flex: 1, overflow: "auto" }}>
        <div style={{ maxWidth: 860, margin: "0 auto", padding: "28px 24px 96px" }}>
          <button
            onClick={() => router.push("/workflows")}
            style={{ ...ghostBtnSm, marginBottom: 18 }}
          >
            ← Workflows
          </button>

          <Tag>tendril · compute</Tag>
          <h1
            style={{
              margin: "14px 0 6px",
              fontSize: 34,
              fontWeight: 500,
              letterSpacing: "-0.02em",
              color: "var(--fg)",
            }}
          >
            Rent a machine
          </h1>
          <p style={{ margin: "0 0 14px", color: "var(--fg-muted)", fontSize: 14, maxWidth: 520 }}>
            A real Linux box, by the hour, paid from your AgentMesh wallet.
            Pick one below, get a terminal, release it when you&rsquo;re done.
          </p>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: 8,
              marginBottom: 32,
              fontSize: 12,
              color: "var(--fg-dim)",
              maxWidth: 560,
            }}
          >
            <span style={{ color: MAGENTA, flexShrink: 0 }}>ⓘ</span>
            <span>
              Renting reserves the hours you pick up front. Release early and
              you&rsquo;re billed only for the seconds you actually used —
              the rest comes straight back to your Tendril credit.
            </span>
          </div>

          {/* ── Balance ledger ─────────────────────────────────────────── */}
          <Panel style={{ padding: "18px 20px", marginBottom: 20 }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
              <div>
                <PanelLabel>Tendril credit</PanelLabel>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 32,
                    fontWeight: 600,
                    color: "var(--fg)",
                    marginTop: 4,
                    letterSpacing: "-0.01em",
                  }}
                >
                  {loading ? "…" : `$${(credit ?? 0).toFixed(2)}`}
                  <span className="pulse" style={{ color: MAGENTA }}>
                    _
                  </span>
                </div>
                <div style={{ fontSize: 11.5, color: "var(--fg-dim)", marginTop: 4 }}>
                  Separate from your AgentMesh credits — spendable only on
                  machine time.
                </div>
              </div>

              {!addingCredit ? (
                <button
                  style={quietButton()}
                  onClick={() => {
                    setAddingCredit(true);
                    setTopupMsg(null);
                  }}
                >
                  + Add credit
                </button>
              ) : (
                <div style={{ display: "flex", flexDirection: "column", gap: 6, alignItems: "flex-end" }}>
                  <div style={{ display: "flex", gap: 6 }}>
                    <span
                      style={{
                        ...monoInput,
                        display: "flex",
                        alignItems: "center",
                        color: "var(--fg-dim)",
                        paddingRight: 4,
                        borderRight: "none",
                        borderTopRightRadius: 0,
                        borderBottomRightRadius: 0,
                      }}
                    >
                      $
                    </span>
                    <input
                      autoFocus
                      style={{
                        ...monoInput,
                        width: 80,
                        borderTopLeftRadius: 0,
                        borderBottomLeftRadius: 0,
                        marginLeft: -6,
                      }}
                      type="number"
                      min="0.1"
                      step="0.5"
                      value={topupAmount}
                      onChange={(e) => setTopupAmount(e.target.value)}
                    />
                    <button
                      style={nameplateButton(topupBusy)}
                      disabled={topupBusy}
                      onClick={handleTopup}
                    >
                      {topupBusy ? "Adding…" : "Add"}
                    </button>
                    <button style={quietButton()} onClick={() => setAddingCredit(false)}>
                      cancel
                    </button>
                  </div>
                </div>
              )}
            </div>
            {topupMsg && (
              <div style={{ fontSize: 12, marginTop: 10, color: "var(--danger)" }}>
                {topupMsg}
              </div>
            )}
            {topupResult && (
              <div
                style={{
                  marginTop: 10,
                  paddingTop: 10,
                  borderTop: "1px solid var(--border)",
                  display: "flex",
                  flexDirection: "column",
                  gap: 4,
                }}
              >
                <div style={{ fontSize: 12, color: "var(--fg-muted)" }}>
                  Added {topupResult.toppedUp} USDC.
                </div>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 12, fontSize: 11 }}>
                  {topupResult.txId && (
                    <span style={{ display: "flex", alignItems: "center", gap: 5 }}>
                      <span style={{ color: "var(--fg-dim)" }}>Wallet 1 → Wallet 2</span>
                      {topupResult.explorerURL ? (
                        <a
                          href={topupResult.explorerURL}
                          target="_blank"
                          rel="noopener noreferrer"
                          style={txLinkStyle}
                        >
                          {topupResult.txId.slice(0, 10)}…
                        </a>
                      ) : (
                        <code style={{ fontFamily: "var(--font-mono)", color: "var(--fg-muted)" }}>
                          {topupResult.txId.slice(0, 10)}…
                        </code>
                      )}
                    </span>
                  )}
                  {topupResult.outboundTxId && (
                    <span style={{ display: "flex", alignItems: "center", gap: 5 }}>
                      <span style={{ color: "var(--fg-dim)" }}>Wallet 2 → Tendril</span>
                      {topupResult.outboundExplorerURL ? (
                        <a
                          href={topupResult.outboundExplorerURL}
                          target="_blank"
                          rel="noopener noreferrer"
                          style={txLinkStyle}
                        >
                          {topupResult.outboundTxId.slice(0, 10)}…
                        </a>
                      ) : (
                        <code style={{ fontFamily: "var(--font-mono)", color: "var(--fg-muted)" }}>
                          {topupResult.outboundTxId.slice(0, 10)}…
                        </code>
                      )}
                    </span>
                  )}
                </div>
              </div>
            )}
          </Panel>

          {/* ── Machine market ──────────────────────────────────────────── */}
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "baseline",
              marginBottom: 10,
            }}
          >
            <PanelLabel>Online machines</PanelLabel>
            <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11.5, color: "var(--fg-dim)" }}>
              rent for
              <input
                style={{ ...monoInput, width: 50, height: 26 }}
                type="number"
                min="0.5"
                step="0.5"
                max="24"
                value={rentHours}
                onChange={(e) => setRentHours(e.target.value)}
              />
              hour(s)
            </label>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 8, marginBottom: 24 }}>
            {machines.length === 0 && !loading && (
              <Panel style={{ padding: 16, textAlign: "center", color: "var(--fg-dim)", fontSize: 13 }}>
                No machines online right now.
              </Panel>
            )}
            {machines.map((m) => {
              const cost = estimateLeaseCostUSD(m.pricePerHourUsd, hours);
              // Affordability is checked against the Tendril-credit-only
              // portion (hours, no gate fee) -- the gate fee is billed
              // separately in AgentMesh credit, not drawn from `credit`.
              const hoursCost = estimateLeaseHoursCostUSD(
                m.pricePerHourUsd,
                hours,
              );
              const overBudget = credit != null && hoursCost > credit;
              const busy = rentBusy === m.id;
              return (
                <Panel
                  key={m.id}
                  style={{
                    padding: "14px 18px",
                    display: "flex",
                    alignItems: "center",
                    gap: 16,
                  }}
                >
                  <Dot color={GREEN} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 13,
                        fontWeight: 600,
                        color: "var(--fg)",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {m.label || m.id}
                    </div>
                    <div
                      style={{
                        display: "flex",
                        gap: 10,
                        marginTop: 2,
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        color: "var(--fg-dim)",
                      }}
                    >
                      <span>{m.cpuCores} vCPU</span>
                      <span style={{ opacity: 0.4 }}>│</span>
                      <span>{Math.round(m.ramMb / 1024)} GB</span>
                      <span style={{ opacity: 0.4 }}>│</span>
                      <span style={{ color: MAGENTA }}>${m.pricePerHourUsd.toFixed(2)}/hr</span>
                      {m.gpu && (
                        <>
                          <span style={{ opacity: 0.4 }}>│</span>
                          <span>{m.gpu}</span>
                        </>
                      )}
                    </div>
                  </div>
                  <div style={{ textAlign: "right", fontSize: 11, color: overBudget ? "var(--danger)" : "var(--fg-dim)", fontFamily: "var(--font-mono)" }}>
                    ${cost.toFixed(2)}
                  </div>
                  <button
                    style={nameplateButton(busy)}
                    disabled={busy}
                    onClick={() => handleRent(m.id)}
                  >
                    {busy ? "Renting…" : "Rent"}
                  </button>
                </Panel>
              );
            })}
          </div>
          {rentMsg && (
            <div style={{ fontSize: 12, marginTop: -14, marginBottom: 20, color: "var(--danger)" }}>
              {rentMsg}
            </div>
          )}

          {/* ── Active session ─────────────────────────────────────────── */}
          {activeLease && (
            <Panel style={{ overflow: "hidden" }}>
              <div
                style={{
                  padding: "14px 18px",
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  borderBottom: "1px solid var(--border)",
                  background: MAGENTA_DIM,
                }}
              >
                <Dot color={GREEN} />
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, fontWeight: 600, color: "var(--fg)" }}>
                  {activeLease.tendrilNodeLabel || activeLease.tendrilNodeId}
                </span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--fg-dim)" }}>
                  ${(activeLease.rateUsdMicrosPerHour / 1e6).toFixed(2)}/hr
                </span>
                <div style={{ flex: 1 }} />
                <button
                  style={{ ...quietButton(true), opacity: releaseBusy ? 0.6 : 1 }}
                  disabled={releaseBusy}
                  onClick={handleRelease}
                >
                  {releaseBusy ? "Releasing…" : "Release"}
                </button>
              </div>

              {/* Elapsed-vs-reserved bar: the visual version of the same
                  promise the info note above makes in words. Clamped to
                  100% past the reserved window — the session is still
                  billing live at that point (Tendril's watchdog, not a
                  hard cutoff), not overrunning some hard cap. */}
              <div style={{ padding: "12px 18px 0" }}>
                {(() => {
                  const startedMs = new Date(activeLease.startedAt).getTime();
                  // The renter's own window — hoursPurchased, not Tendril's
                  // fundedUntil (see formatRemaining's doc comment above).
                  const reservedUntilMs =
                    startedMs + activeLease.hoursPurchased * 3600_000;
                  const totalMs = Math.max(1, reservedUntilMs - startedMs);
                  const elapsedMs = Math.max(0, now - startedMs);
                  const pct = Math.min(100, (elapsedMs / totalMs) * 100);
                  const pastReserved = now >= reservedUntilMs;
                  return (
                    <>
                      <div
                        style={{
                          height: 4,
                          borderRadius: 999,
                          background: "var(--bg)",
                          border: "1px solid var(--border)",
                          overflow: "hidden",
                        }}
                      >
                        <div
                          style={{
                            height: "100%",
                            width: `${pct}%`,
                            background: pastReserved ? "var(--warm)" : MAGENTA,
                            transition: "width 1s linear",
                          }}
                        />
                      </div>
                      <div
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          marginTop: 6,
                          fontFamily: "var(--font-mono)",
                          fontSize: 10.5,
                          color: "var(--fg-dim)",
                        }}
                      >
                        <span>{formatDuration(elapsedMs / 1000)} used</span>
                        <span style={{ color: pastReserved ? "var(--warm)" : "var(--fg-dim)" }}>
                          {formatRemaining(reservedUntilMs, now)}
                        </span>
                      </div>
                    </>
                  );
                })()}
              </div>

              <div style={{ padding: 18 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <code
                    style={{
                      flex: 1,
                      background: "var(--bg)",
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-1)",
                      padding: "8px 12px",
                      fontSize: 12,
                      fontFamily: "var(--font-mono)",
                      color: "var(--fg-muted)",
                      overflow: "auto",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {activeLease.sshCommand}
                  </code>
                  <button
                    style={quietButton()}
                    onClick={() => navigator.clipboard?.writeText(activeLease.sshCommand)}
                  >
                    copy
                  </button>
                  <button style={quietButton()} onClick={() => setShowTerminal((s) => !s)}>
                    {showTerminal ? "hide terminal" : "open terminal"}
                  </button>
                </div>

                {showTerminal && (
                  <div
                    style={{
                      height: 320,
                      marginTop: 14,
                      border: "1px solid var(--border)",
                      borderRadius: "var(--r-2)",
                      overflow: "hidden",
                    }}
                  >
                    <TerminalTab leaseId={activeLease.id} onClose={() => setShowTerminal(false)} />
                  </div>
                )}

                <div style={{ marginTop: 20 }}>
                  <PanelLabel>Run a job</PanelLabel>
                  <textarea
                    style={{
                      ...monoInput,
                      width: "100%",
                      height: 76,
                      padding: 10,
                      marginTop: 8,
                      resize: "vertical",
                      boxSizing: "border-box",
                    }}
                    value={payload}
                    onChange={(e) => setPayload(e.target.value)}
                  />
                  <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
                    <button style={nameplateButton(runBusy)} disabled={runBusy} onClick={handleRun}>
                      {runBusy ? "Running…" : "Run"}
                    </button>
                  </div>
                  {runMsg && (
                    <div style={{ fontSize: 12, marginTop: 8, color: "var(--danger)" }}>{runMsg}</div>
                  )}
                  {runOutput && (
                    <pre
                      style={{
                        marginTop: 8,
                        padding: 10,
                        background: "var(--bg)",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--r-2)",
                        fontSize: 11,
                        fontFamily: "var(--font-mono)",
                        color: "var(--fg-muted)",
                        overflow: "auto",
                        maxHeight: 200,
                      }}
                    >
                      {runOutput}
                    </pre>
                  )}
                </div>
              </div>
            </Panel>
          )}

          {!activeLease && releaseResult && (
            <Panel
              style={{
                padding: "14px 18px",
                display: "flex",
                alignItems: "center",
                gap: 10,
                borderColor: "rgba(52, 211, 153, 0.35)",
                background: "rgba(52, 211, 153, 0.06)",
              }}
            >
              <Dot color={GREEN} />
              <div style={{ fontSize: 12.5, color: "var(--fg)" }}>
                Released after {formatDuration(releaseResult.usedSeconds)} —
                charged{" "}
                <strong style={{ fontFamily: "var(--font-mono)" }}>
                  ${(releaseResult.charged / 1e6).toFixed(2)}
                </strong>
                , refunded{" "}
                <strong style={{ fontFamily: "var(--font-mono)", color: GREEN }}>
                  ${(releaseResult.refunded / 1e6).toFixed(2)}
                </strong>{" "}
                back to your Tendril credit.
              </div>
              <div style={{ flex: 1 }} />
              <button style={quietButton()} onClick={() => setReleaseResult(null)}>
                dismiss
              </button>
            </Panel>
          )}

          {!activeLease && !releaseResult && machines.length > 0 && !loading && (
            <div style={{ fontSize: 12, color: "var(--fg-dim)", marginTop: -4 }}>
              Rent one to get a terminal.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
