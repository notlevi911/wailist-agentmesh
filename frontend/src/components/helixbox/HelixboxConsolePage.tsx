"use client";
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useRouter } from "next/navigation";
import { Topbar } from "@/components/Topbar";
import { Tag, ghostBtnSm } from "@/components/ui";
import { ExternalLink } from "@/components/ExternalLink";
import {
  helixbox as helixboxApi,
  formatUsd,
  formatDuration,
  formatRemaining,
  totalCostMicros,
  paidUntil,
  sessionCode,
  saveSession,
  forgetSession,
  subscribeSessions,
  getSessionsSnapshot,
  getServerSessionsSnapshot,
  HelixboxRunError,
  type HelixboxEndpoint,
  type HelixboxField,
  type HelixboxRunResult,
  type HelixboxSession,
  type HelixboxSpec,
} from "@/lib/helixbox";
import { PAYMENT_RECOVERY, type PaymentRefusalCode } from "@/lib/paymentRefusal";

// HelixBox shares the x402 magenta the canvas tool node, the Inspector and the
// Tendril and Prism consoles all use, so a paid endpoint reads as the same kind
// of thing wherever it appears in the app.
const MAGENTA = "var(--type-x402)";
const MAGENTA_DIM = "var(--type-x402-soft)";
const AMBER = "var(--warning)";

// How often the "42m left" labels are recomputed. An hour-long session is the
// most common purchase, so a stale label is misleading within minutes — but
// nothing here needs to tick every second either.
const CLOCK_INTERVAL_MS = 30_000;

// ── Shared chrome, matching the Prism and Tendril consoles' idiom ───────────
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
        fontSize: "var(--t-0)",
        textTransform: "uppercase",
        letterSpacing: "0.1em",
        color: "var(--fg-dim)",
      }}
    >
      {children}
    </div>
  );
}

function nameplateButton(disabled: boolean): React.CSSProperties {
  return {
    height: 36,
    padding: "0 18px",
    fontSize: "var(--t-1)",
    fontWeight: 700,
    letterSpacing: "0.08em",
    textTransform: "uppercase",
    fontFamily: "var(--font-mono)",
    background: disabled ? "var(--bg-elev-2)" : MAGENTA,
    border: `1px solid ${disabled ? "var(--border-strong)" : MAGENTA}`,
    borderRadius: "var(--r-1)",
    color: disabled ? "var(--fg-dim)" : "var(--type-x402-fg)",
    cursor: disabled ? "default" : "pointer",
    whiteSpace: "nowrap",
  };
}

const txLinkStyle: React.CSSProperties = {
  color: MAGENTA,
  textDecoration: "underline",
  fontFamily: "var(--font-mono)",
  fontSize: "var(--t-1)",
  wordBreak: "break-all",
};

// CopyField shows a short code and gets it onto the clipboard in one click.
function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1800);
    return () => clearTimeout(t);
  }, [copied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      // Clipboard access can be refused outright (an insecure origin, or a
      // permission the user declined). Selecting the text is the fallback,
      // and it is already on screen and selectable — so say nothing rather
      // than raise an error for something the user can still do by hand.
    }
  };

  return (
    <div
      style={{
        display: "flex",
        gap: "var(--s-3)",
        alignItems: "stretch",
        flexWrap: "wrap",
      }}
    >
      <code
        style={{
          flex: "1 1 240px",
          minWidth: 0,
          padding: "10px 12px",
          background: "var(--bg)",
          border: `1px solid ${MAGENTA}`,
          borderRadius: "var(--r-1)",
          fontFamily: "var(--font-mono)",
          fontSize: "var(--t-2)",
          color: "var(--fg)",
          wordBreak: "break-all",
          lineHeight: 1.5,
          userSelect: "all",
        }}
      >
        {value}
      </code>
      <button
        type="button"
        onClick={copy}
        style={{ ...ghostBtnSm, height: "auto", minHeight: 38 }}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

const textInput: React.CSSProperties = {
  width: "100%",
  height: 36,
  padding: "0 11px",
  background: "var(--bg)",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-1)",
  color: "var(--fg)",
  fontSize: "var(--t-3)",
  fontFamily: "var(--font-mono)",
  outline: "none",
  boxSizing: "border-box",
};

// The pairing code input.
//
// This is the only thing the user types, and getting it wrong is expensive in
// a way nothing else here is: HelixBox validates AFTER settling, and accepts
// any string — a typo silently buys time on a session that does not exist, with
// no refund. So the field explains where the code comes from and the panel
// below says plainly that it cannot be checked first.
function PairingCodeField({
  field,
  value,
  onChange,
  disabled,
}: {
  field: HelixboxField;
  value: string;
  onChange: (next: string) => void;
  disabled: boolean;
}) {
  return (
    <div>
      <label
        htmlFor={`helixbox-${field.name}`}
        style={{
          display: "block",
          fontSize: "var(--t-2)",
          fontWeight: 600,
          color: "var(--fg)",
          marginBottom: 4,
        }}
      >
        {field.label}
      </label>
      {field.description && (
        <p
          style={{
            margin: "0 0 8px",
            fontSize: "var(--t-1)",
            lineHeight: 1.55,
            color: "var(--fg-muted)",
            maxWidth: "62ch",
          }}
        >
          {field.description}
        </p>
      )}
      <input
        id={`helixbox-${field.name}`}
        type="text"
        value={value}
        disabled={disabled}
        placeholder={field.placeholder}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="off"
        autoCapitalize="off"
        autoCorrect="off"
        spellCheck={false}
        style={textInput}
      />
    </div>
  );
}

// One plan, as a pickable card. Not the Segmented control the Prism console
// uses: these three differ on price, length AND what the session can do, which
// is more than a one-line note can carry.
function PlanOption({
  endpoint,
  fee,
  selected,
  onSelect,
  samePriceNote,
}: {
  endpoint: HelixboxEndpoint;
  fee: number;
  selected: boolean;
  onSelect: () => void;
  samePriceNote: boolean;
}) {
  const total = totalCostMicros(endpoint, fee);
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      style={{
        display: "block",
        width: "100%",
        textAlign: "left",
        padding: "14px 16px",
        borderRadius: "var(--r-2)",
        border: `1px solid ${selected ? MAGENTA : "var(--border-strong)"}`,
        background: selected ? MAGENTA_DIM : "transparent",
        color: "var(--fg)",
        cursor: "pointer",
        fontFamily: "var(--font-sans)",
        transition:
          "border-color 0.15s var(--ease), background 0.15s var(--ease)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          gap: "var(--s-4)",
          flexWrap: "wrap",
        }}
      >
        <span style={{ fontSize: "var(--t-4)", fontWeight: 600 }}>
          {endpoint.title}
        </span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-2)",
            fontWeight: 600,
            color: selected ? MAGENTA : "var(--fg-muted)",
          }}
        >
          {formatUsd(total)}
        </span>
      </div>
      <div
        style={{
          fontSize: "var(--t-1)",
          color: "var(--fg-dim)",
          fontFamily: "var(--font-mono)",
          marginTop: 3,
        }}
      >
        {formatDuration(endpoint.durationSeconds)} · {endpoint.accessLevel}{" "}
        access
      </div>
      <p
        style={{
          margin: "8px 0 0",
          fontSize: "var(--t-2)",
          lineHeight: 1.55,
          color: "var(--fg-muted)",
          maxWidth: "62ch",
        }}
      >
        {endpoint.blurb}
      </p>
      {/* Two plans at the same price look like a bug unless it is said out
          loud. It is HelixBox's own pricing, pinned by
          TestTheTwoHourlyPlansStillSharePrice. */}
      {samePriceNote && (
        <div
          style={{
            marginTop: 6,
            fontSize: "var(--t-1)",
            color: "var(--fg-dim)",
          }}
        >
          Agent access is included in the hourly price.
        </div>
      )}
    </button>
  );
}

// A session the user has already bought, kept so a refresh does not lose a
// paid-for credential.
function SavedSession({
  session,
  now,
  onForget,
}: {
  session: HelixboxSession;
  now: number;
  onForget: () => void;
}) {
  const remaining = formatRemaining(session.expiresAt, now);
  const expired = remaining === null;
  return (
    <div
      style={{
        padding: "12px 14px",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-2)",
        background: "var(--bg)",
        opacity: expired ? 0.6 : 1,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          gap: "var(--s-3)",
          flexWrap: "wrap",
        }}
      >
        <span
          style={{
            fontSize: "var(--t-3)",
            fontWeight: 600,
            color: "var(--fg)",
          }}
        >
          {session.title}
        </span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-1)",
            color: expired ? "var(--fg-dim)" : AMBER,
          }}
        >
          {expired ? "Expired" : remaining}
        </span>
      </div>
      <div style={{ marginTop: 8 }}>
        <CopyField value={session.code} />
      </div>
      <div
        style={{
          marginTop: 8,
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "var(--s-3)",
          flexWrap: "wrap",
        }}
      >
        <span style={{ fontSize: "var(--t-1)", color: "var(--fg-dim)" }}>
          {formatUsd(session.totalUsdMicros)} ·{" "}
          {new Date(session.boughtAt).toLocaleString()}
        </span>
        <button type="button" onClick={onForget} style={ghostBtnSm}>
          {expired ? "Remove" : "Forget this"}
        </button>
      </div>
    </div>
  );
}

export function HelixboxConsolePage() {
  const router = useRouter();
  const [spec, setSpec] = useState<HelixboxSpec | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [planId, setPlanId] = useState<string | null>(null);

  // Keyed by nothing: all three plans take the same single field, and a code
  // typed under one plan is still the right code under another. Clearing it on
  // a plan switch would just make the user paste it again.
  const [code, setCode] = useState("");

  const [buying, setBuying] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const [refusalCode, setRefusalCode] = useState<PaymentRefusalCode | null>(null);
  const recovery = refusalCode ? PAYMENT_RECOVERY[refusalCode] : null;
  const [result, setResult] = useState<HelixboxRunResult | null>(null);

  // Read through useSyncExternalStore rather than an effect that setStates on
  // mount: localStorage is external state, and this is the idiom useReadOnly
  // and useIsCompact already use here. The server snapshot is empty, so the
  // SSR markup and the first client render agree and the drawer fills in on
  // hydration.
  const sessions = useSyncExternalStore(
    subscribeSessions,
    getSessionsSnapshot,
    getServerSessionsSnapshot,
  );
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    let stale = false;
    helixboxApi
      .spec()
      .then((s) => {
        if (stale) return;
        setSpec(s);
        setPlanId((cur) => cur ?? s.endpoints[0]?.id ?? null);
      })
      .catch((e: unknown) => {
        if (!stale) {
          setLoadError(
            e instanceof Error ? e.message : "Could not load HelixBox's plans.",
          );
        }
      });
    return () => {
      stale = true;
    };
  }, []);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), CLOCK_INTERVAL_MS);
    return () => clearInterval(t);
  }, []);

  const plan = useMemo(
    () => spec?.endpoints.find((e) => e.id === planId) ?? null,
    [spec, planId],
  );

  // True when two plans share a price, which is worth explaining on the card
  // rather than leaving to look like a mistake.
  const hourlyPlansSharePrice = useMemo(() => {
    if (!spec) return false;
    const hourly = spec.endpoints.filter((e) => e.durationSeconds === 3600);
    return (
      hourly.length > 1 &&
      hourly.every((e) => e.amountMicros === hourly[0].amountMicros)
    );
  }, [spec]);

  const codeField = plan?.fields.find((f) => f.name === "code") ?? null;
  const requiresCode = Boolean(codeField?.required);

  const fee = spec?.platformFeeUsdMicros ?? 0;
  const total = plan ? totalCostMicros(plan, fee) : 0;

  const trimmedCode = code.trim();
  // Mirrors the backend's own check so the button is disabled rather than the
  // user discovering the problem via a 400. The backend check is the real one.
  const missingCode = requiresCode && trimmedCode === "";

  const handleBuy = async () => {
    if (!plan || buying || missingCode) return;
    setBuying(true);
    setRunError(null);
    setRefusalCode(null);
    setResult(null);
    try {
      const res = await helixboxApi.run(plan.id, { code: trimmedCode });
      setResult(res);
      // Record it immediately, before anything else on this page can go wrong:
      // the money has moved by now and the backend keeps no copy of the reply.
      const until = paidUntil(res.response, Date.now());
      const applied = sessionCode(res.response) ?? trimmedCode;
      // Keyed on whether money MOVED, not on whether the reply parsed.
      //
      // Both halves of that matter. Gating on `until` meant a settled purchase
      // whose expiry we could not read left no receipt at all — and the drawer
      // exists precisely because nothing else records the purchase. Gating on
      // settlement alone would write a $0 receipt for a call that never paid.
      // Falling back to the plan's advertised duration keeps a readable card
      // when HelixBox's expiry shape changes under us.
      if (res.settled) {
        const expiresAt = until ?? Date.now() + res.durationSeconds * 1000;
        saveSession({
          key: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          endpoint: res.endpoint,
          title: res.title,
          accessLevel: res.accessLevel,
          code: applied,
          expiresAt,
          boughtAt: Date.now(),
          totalUsdMicros: res.totalUsdMicros,
          txId: res.txId,
          explorerURL: res.explorerURL,
        });
      }
    } catch (e) {
      setRefusalCode(e instanceof HelixboxRunError ? e.code : null);
      setRunError(
        e instanceof Error ? e.message : "Something went wrong. Try again.",
      );
    } finally {
      setBuying(false);
    }
  };

  // `now` rather than Date.now(): reading the clock during render is impure
  // (react-hooks/purity), and this only matters for the fallback duration
  // shape, where a 30-second-stale clock is immaterial.
  const resultPaidUntil = result ? paidUntil(result.response, now) : null;
  const resultCode = result ? sessionCode(result.response) : null;

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
        <div className="am-console-page">
          <div style={{ marginBottom: 18 }}>
            <button
              onClick={() => router.push("/workflows")}
              style={ghostBtnSm}
            >
              ← Workflows
            </button>
          </div>

          <Tag>helixbox · mobile ide</Tag>
          <h1 className="am-console-title">Buy a HelixBox session</h1>
          <p
            style={{
              margin: "0 0 14px",
              color: "var(--fg-muted)",
              fontSize: "var(--t-4)",
              maxWidth: 560,
              lineHeight: 1.6,
            }}
          >
            Access files, logs, Git, and a terminal from your phone. Buy a
            HelixBox session and use its token to sign in.
          </p>

          {loadError && (
            <Panel style={{ padding: 16, borderColor: "var(--danger)" }}>
              <div style={{ fontSize: "var(--t-3)", color: "var(--danger)" }}>
                {loadError}
              </div>
            </Panel>
          )}

          {!spec && !loadError && (
            <div
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: "var(--t-2)",
                color: "var(--fg-dim)",
              }}
            >
              Loading…
            </div>
          )}

          {spec && (
            <>
              {/* ── Plan ─────────────────────────────────────────────── */}
              <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                <PanelLabel>Plan</PanelLabel>
                <div
                  role="radiogroup"
                  aria-label="Plan"
                  style={{
                    marginTop: 10,
                    display: "flex",
                    flexDirection: "column",
                    gap: "var(--s-3)",
                  }}
                >
                  {spec.endpoints.map((e) => (
                    <PlanOption
                      key={e.id}
                      endpoint={e}
                      fee={fee}
                      selected={e.id === planId}
                      onSelect={() => {
                        setPlanId(e.id);
                        setResult(null);
                        setRunError(null);
                      }}
                      samePriceNote={
                        hourlyPlansSharePrice && e.accessLevel === "agent"
                      }
                    />
                  ))}
                </div>
              </Panel>

              {/* ── Which session ───────────────────────────────────── */}
              {plan && codeField && (
                <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                  <PanelLabel>Which session</PanelLabel>
                  <div style={{ marginTop: 12 }}>
                    <PairingCodeField
                      field={codeField}
                      value={code}
                      onChange={setCode}
                      disabled={buying}
                    />
                  </div>

                  {/* The one thing a buyer genuinely needs warning about,
                      stated once, next to the field it is about -- not as a
                      banner they will learn to skip. */}
                  <div
                    style={{
                      display: "flex",
                      gap: "var(--s-2)",
                      alignItems: "baseline",
                      marginTop: 12,
                      fontSize: "var(--t-1)",
                      color: "var(--fg-dim)",
                      lineHeight: 1.55,
                    }}
                  >
                    <span aria-hidden style={{ flexShrink: 0 }}>
                      ⓘ
                    </span>
                    <span>
                      Keep the CLI running and the HelixBox app open on this
                      session. The app checks for new time every couple of
                      seconds, so it unlocks on its own once you buy: there is
                      nothing to paste back into it.
                    </span>
                  </div>

                  {/* Prices are the vendor's share plus our flat fee, and for
                      the hourly plans the fee is the larger half. Showing the
                      split is the difference between a total that looks
                      arbitrary and one the buyer can check. */}
                  <div
                    style={{
                      marginTop: 16,
                      paddingTop: 16,
                      borderTop: "1px solid var(--border)",
                      display: "flex",
                      alignItems: "center",
                      gap: "var(--s-4)",
                      flexWrap: "wrap",
                    }}
                  >
                    <div
                      style={{
                        fontSize: "var(--t-1)",
                        color: "var(--fg-dim)",
                        fontFamily: "var(--font-mono)",
                      }}
                    >
                      {formatUsd(plan.amountMicros)} to HelixBox +{" "}
                      {formatUsd(fee)} AgentMesh fee
                    </div>
                    <div
                      aria-hidden
                      style={{
                        flex: 1,
                        minWidth: 24,
                        height: 1,
                        background: "var(--border)",
                      }}
                    />
                    {missingCode && (
                      <div
                        style={{
                          fontSize: "var(--t-1)",
                          color: "var(--fg-dim)",
                        }}
                      >
                        Add your pairing code to buy.
                      </div>
                    )}
                    <button
                      type="button"
                      onClick={handleBuy}
                      disabled={buying || missingCode}
                      style={nameplateButton(buying || missingCode)}
                    >
                      {buying ? "Buying…" : `Buy · ${formatUsd(total)}`}
                    </button>
                  </div>
                </Panel>
              )}

              {runError && (
                <Panel
                  style={{
                    padding: "14px 16px",
                    marginBottom: 16,
                    borderColor: "var(--danger)",
                  }}
                >
                  <PanelLabel>Didn&rsquo;t buy</PanelLabel>
                  <div
                    style={{
                      marginTop: 6,
                      fontSize: "var(--t-2)",
                      color: "var(--danger)",
                      lineHeight: 1.55,
                    }}
                  >
                    {runError}
                  </div>
                  {recovery ? (
                    <div style={{ marginTop: 10 }}>
                      <div
                        style={{
                          fontSize: "var(--t-1)",
                          color: "var(--fg-muted)",
                          lineHeight: 1.55,
                        }}
                      >
                        {recovery.message}
                      </div>
                      <button
                        type="button"
                        onClick={() => router.push(recovery.href)}
                        style={{ ...ghostBtnSm, marginTop: 8 }}
                      >
                        {recovery.label}
                      </button>
                    </div>
                  ) : (
                    <div
                      style={{
                        marginTop: 8,
                        fontSize: "var(--t-1)",
                        color: "var(--fg-muted)",
                        lineHeight: 1.55,
                      }}
                    >
                      If you were charged, it shows up under Credits. Worth a
                      look before you try again.
                    </div>
                  )}
                </Panel>
              )}

              {/* ── What you bought ──────────────────────────────────── */}
              {result && (
                <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "baseline",
                      justifyContent: "space-between",
                      gap: "var(--s-4)",
                      flexWrap: "wrap",
                    }}
                  >
                    <PanelLabel>Your session</PanelLabel>
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: "var(--t-1)",
                        color: result.settled ? "var(--fg-muted)" : AMBER,
                      }}
                    >
                      {result.settled
                        ? `${formatUsd(result.totalUsdMicros)} charged`
                        : "Free, you were not charged"}
                    </span>
                  </div>

                  {/* An unsettled call is not a free win — it means HelixBox
                      never asked for payment, so this did not come from a paid
                      x402 call at all. */}
                  {!result.settled && (
                    <div
                      style={{
                        marginTop: 10,
                        padding: "10px 12px",
                        border: `1px solid ${AMBER}`,
                        borderRadius: "var(--r-1)",
                        background: "rgba(255, 181, 71, 0.07)",
                        fontSize: "var(--t-1)",
                        color: "var(--fg-muted)",
                        lineHeight: 1.55,
                      }}
                    >
                      HelixBox returned a response without requesting payment. Check
                      that the session works before using it.
                    </div>
                  )}

                  {resultPaidUntil ? (
                    <div style={{ marginTop: 12 }}>
                      <div
                        style={{
                          fontSize: "var(--t-2)",
                          color: "var(--fg-muted)",
                          lineHeight: 1.6,
                          marginBottom: 10,
                        }}
                      >
                        {result.title} added to your session.{" "}
                        {formatDuration(result.durationSeconds)} of{" "}
                        {result.accessLevel} access, paid until{" "}
                        <strong style={{ color: "var(--fg)", fontWeight: 600 }}>
                          {new Date(resultPaidUntil).toLocaleString()}
                        </strong>
                        .
                      </div>
                      {/* Echoed back by HelixBox, not repeated from the form:
                          this is the buyer's only confirmation that the money
                          landed on the session they meant. */}
                      {resultCode && (
                        <>
                          <div
                            style={{
                              fontSize: "var(--t-1)",
                              color: "var(--fg-dim)",
                              marginBottom: 6,
                            }}
                          >
                            Applied to pairing code
                          </div>
                          <CopyField value={resultCode} />
                        </>
                      )}
                      <div
                        style={{
                          marginTop: 10,
                          fontSize: "var(--t-1)",
                          color: "var(--fg-dim)",
                          lineHeight: 1.55,
                        }}
                      >
                        Your HelixBox CLI picks this up on its own: nothing to
                        paste back into it. The receipt is kept in this browser
                        under Your sessions below.
                      </div>
                    </div>
                  ) : (
                    // Paid, but no expiry came back. Saying so beats an empty
                    // panel: the money has moved and the buyer needs to know
                    // there is something to chase.
                    <div
                      style={{
                        marginTop: 12,
                        padding: "10px 12px",
                        border: `1px solid ${AMBER}`,
                        borderRadius: "var(--r-1)",
                        background: "rgba(255, 181, 71, 0.07)",
                        fontSize: "var(--t-2)",
                        color: "var(--fg-muted)",
                        lineHeight: 1.55,
                      }}
                    >
                      The payment went through, but HelixBox did not say how
                      long your session is now paid for. The full reply is below.
                      Keep the transaction link as your receipt and contact
                      HelixBox with it.
                    </div>
                  )}

                  {/* The raw reply, folded away. There is nothing else in it
                      once the token is pulled out, but a buyer chasing a
                      missing session needs to be able to see exactly what
                      came back. */}
                  <details style={{ marginTop: 12 }}>
                    <summary
                      style={{
                        cursor: "pointer",
                        fontSize: "var(--t-1)",
                        color: "var(--fg-dim)",
                      }}
                    >
                      Show HelixBox&rsquo;s full reply
                    </summary>
                    <pre
                      style={{
                        marginTop: 8,
                        marginBottom: 0,
                        padding: "10px 12px",
                        background: "var(--bg)",
                        border: "1px solid var(--border)",
                        borderRadius: "var(--r-1)",
                        fontSize: "var(--t-1)",
                        fontFamily: "var(--font-mono)",
                        color: "var(--fg-muted)",
                        overflowX: "auto",
                        lineHeight: 1.5,
                      }}
                    >
                      {JSON.stringify(result.response, null, 2)}
                    </pre>
                  </details>

                  {(result.txId || result.platformFeeTxId) && (
                    <div
                      style={{
                        marginTop: 12,
                        display: "flex",
                        flexDirection: "column",
                        gap: "var(--s-1)",
                      }}
                    >
                      {result.txId && (
                        <div
                          style={{
                            fontSize: "var(--t-1)",
                            color: "var(--fg-dim)",
                          }}
                        >
                          Paid to HelixBox{" "}
                          <ExternalLink
                            href={result.explorerURL}
                            style={txLinkStyle}
                          >
                            {result.txId}
                          </ExternalLink>
                        </div>
                      )}
                      {result.platformFeeTxId && (
                        <div
                          style={{
                            fontSize: "var(--t-1)",
                            color: "var(--fg-dim)",
                          }}
                        >
                          AgentMesh fee{" "}
                          <ExternalLink
                            href={result.platformFeeExplorerURL}
                            style={txLinkStyle}
                          >
                            {result.platformFeeTxId}
                          </ExternalLink>
                        </div>
                      )}
                    </div>
                  )}
                </Panel>
              )}

              {/* ── Your sessions ────────────────────────────────────── */}
              {sessions.length > 0 && (
                <Panel style={{ padding: "18px 20px" }}>
                  <PanelLabel>Your sessions</PanelLabel>
                  <p
                    style={{
                      margin: "8px 0 0",
                      fontSize: "var(--t-1)",
                      color: "var(--fg-dim)",
                      lineHeight: 1.55,
                    }}
                  >
                    Kept in this browser only.
                  </p>
                  <div
                    style={{
                      marginTop: 12,
                      display: "flex",
                      flexDirection: "column",
                      gap: "var(--s-3)",
                    }}
                  >
                    {sessions.map((s) => (
                      <SavedSession
                        key={s.key}
                        session={s}
                        now={now}
                        onForget={() => forgetSession(s.key)}
                      />
                    ))}
                  </div>
                </Panel>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
