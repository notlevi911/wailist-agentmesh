"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useCredits } from "@/lib/credits/store";
import { LowBalanceBanner } from "@/components/billing/LowBalanceBanner";
import { IconSearch, Card, ghostBtnSm } from "@/components/ui";
import { Topbar } from "@/components/Topbar";
import { workflowHref } from "@/lib/routes";
import { usage as usageApi } from "@/lib/api";
import { scopedWorkflowLabel } from "@/lib/usageScope";
import {
  UsageRange,
  UsagePayload,
  EndpointUsage,
  UsageCategory,
  Settlement,
} from "@/lib/types";
import { AreaChart } from "./AreaChart";
import { Donut, DonutSegment } from "./Donut";
import { useIsHandheld } from "@/hooks/useIsHandheld";
import { useReadOnly } from "@/hooks/useReadOnly";
import { UsagePhonePage } from "./phone/UsagePhonePage";
import {
  ALGO_USD,
  CAT_COLOR,
  CAT_LABEL,
  TYPE_PILL,
  compactUsd,
  relTime,
  usd,
} from "./format";

const RANGES: UsageRange[] = ["24h", "7d", "30d"];

export function UsagePage() {
  const router = useRouter();
  const readOnly = useReadOnly();

  const [range, setRange] = useState<UsageRange>("30d");
  const [data, setData] = useState<UsagePayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<Error | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
  // Callers waiting for a reload to land -- pull to refresh keeps spinning
  // until its promise settles. Released when the newest batch finishes; a
  // batch a newer one replaced keeps them waiting for that one instead.
  const reloadWaiters = useRef<Array<() => void>>([]);
  const [scopedWf, setScopedWf] = useState<string | null>(null);

  // ?workflow=<id> deep-link filter (read without useSearchParams to avoid a
  // Suspense boundary requirement).
  useEffect(() => {
    if (typeof window === "undefined") return;
    const wf = new URLSearchParams(window.location.search).get("workflow");
    // eslint-disable-next-line react-hooks/set-state-in-effect -- post-mount URL read; a lazy initializer would render the filter banner on the server and break hydration
    if (wf) setScopedWf(wf);
  }, []);

  // The fetch effect only fetches -- loading/error resets live in the event
  // handlers (changeRange/retry) that trigger it, and the initial state
  // already starts as loading. Sync setState in effects cascades renders.
  useEffect(() => {
    let cancelled = false;
    const requests = [
      usageApi.summary(range),
      usageApi.timeseries(range),
      usageApi.byWorkflow(range),
      usageApi.byEndpoint(range),
      usageApi.settlements(18),
    ] as const;
    Promise.all(requests)
      .then(([summary, timeseries, byWorkflow, byEndpoint, settlements]) => {
        if (cancelled) return;
        setData({ summary, timeseries, byWorkflow, byEndpoint, settlements });
      })
      .catch(async (e) => {
        if (cancelled) return;
        // Surface the failure but keep the last good payload -- a transient error
        // on a range switch shouldn't blank a page that was already working.
        console.error("usage load failed", e);
        setLoadError(e instanceof Error ? e : new Error(String(e)));
        // Promise.all gives up at the first failure with the rest still out.
        // The reload is not over until they have answered too, so a pull keeps
        // spinning until then rather than stopping on a half-finished load.
        await Promise.allSettled(requests);
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
        const waiters = reloadWaiters.current;
        reloadWaiters.current = [];
        for (const done of waiters) done();
      });
    return () => {
      cancelled = true;
    };
  }, [range, reloadNonce]);

  const changeRange = (r: UsageRange) => {
    setLoading(true);
    setLoadError(null);
    setRange(r);
  };

  // Retry must bust the mock-mode cache, otherwise the refetch resolves from
  // the memoized payload and the figures visibly never change.
  // Resolves once the reload it starts has landed (or failed), so pull to
  // refresh spins for as long as the requests do.
  const retry = () =>
    new Promise<void>((resolve) => {
      reloadWaiters.current.push(resolve);
      usageApi.invalidate();
      setLoading(true);
      setLoadError(null);
      setReloadNonce((n) => n + 1);
    });

  // Clearing the scope must also drop ?workflow= from the URL, otherwise a
  // refresh or back-navigation silently reapplies the filter the user just cleared.
  const clearScope = () => {
    setScopedWf(null);
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    url.searchParams.delete("workflow");
    window.history.replaceState(null, "", url.pathname + url.search + url.hash);
  };

  // A phone gets its own screen: the desktop page is two wide tables that
  // only scroll sideways there. Every hook above has already run, so this
  // return is safe, and the desktop JSX below is untouched.
  if (readOnly) {
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
        <UsagePhonePage
          range={range}
          onRange={changeRange}
          data={data}
          loading={loading}
          error={loadError}
          onRetry={retry}
          scopedWorkflowId={scopedWf}
          onClearScope={clearScope}
        />
      </div>
    );
  }

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

      {/* Main */}
      <div style={{ flex: 1, overflow: "auto", background: "var(--bg)" }}>
        <div
          style={{
            maxWidth: 1280,
            margin: "0 auto",
            padding: "var(--wf-page-pad)",
          }}
        >
          {scopedWf && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 10,
                marginBottom: 16,
                padding: "8px 12px",
                background: "var(--accent-soft)",
                border: "1px solid var(--accent-line)",
                borderRadius: "var(--r-2)",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: "var(--accent)",
              }}
            >
              Workflows by spend · filtered to{" "}
              {scopedWorkflowLabel(scopedWf, data?.byWorkflow)}
              <button
                onClick={clearScope}
                style={{
                  marginLeft: "auto",
                  background: "transparent",
                  border: "none",
                  color: "var(--accent)",
                  cursor: "pointer",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  textDecoration: "underline",
                }}
              >
                clear
              </button>
            </div>
          )}

          {loadError && data && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 10,
                marginBottom: 16,
                padding: "8px 12px",
                background: "rgba(255,92,92,0.10)",
                border: "1px solid rgba(255,92,92,0.35)",
                borderRadius: "var(--r-2)",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: "var(--danger)",
              }}
            >
              couldn&apos;t refresh, showing the last loaded data
              <button
                onClick={retry}
                style={{
                  marginLeft: "auto",
                  background: "transparent",
                  border: "none",
                  color: "var(--danger)",
                  cursor: "pointer",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  textDecoration: "underline",
                }}
              >
                retry
              </button>
            </div>
          )}

          {loading && !data ? (
            <div
              style={{
                padding: 64,
                textAlign: "center",
                color: "var(--fg-dim)",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
            >
              loading usage…
            </div>
          ) : loadError && !data ? (
            <div
              style={{
                padding: 48,
                textAlign: "center",
                border: "1px dashed var(--danger)",
                borderRadius: "var(--r-3)",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
            >
              <div style={{ color: "var(--danger)", marginBottom: 8 }}>
                couldn&apos;t load usage
              </div>
              <div style={{ color: "var(--fg-dim)", marginBottom: 16 }}>
                the usage service didn&apos;t respond; this is different from
                having no usage yet
              </div>
              <button onClick={retry} style={ghostBtnSm}>
                retry
              </button>
            </div>
          ) : !data ? (
            <div
              style={{
                padding: 48,
                textAlign: "center",
                border: "1px dashed var(--border)",
                borderRadius: "var(--r-3)",
                color: "var(--fg-dim)",
                fontFamily: "var(--font-mono)",
                fontSize: 12,
              }}
            >
              no usage yet, run a workflow to see spend here
            </div>
          ) : (
            <UsageBody
              data={data}
              range={range}
              onRangeChange={changeRange}
              scopedWf={scopedWf}
              onOpenWorkflow={(id) => router.push(workflowHref(id))}
              onTopUp={() => router.push("/billing")}
              loading={loading}
            />
          )}
        </div>
      </div>
    </div>
  );
}

function UsageBody({
  data,
  range,
  onRangeChange,
  scopedWf,
  onOpenWorkflow,
  onTopUp,
  loading,
}: {
  data: UsagePayload;
  range: UsageRange;
  onRangeChange: (r: UsageRange) => void;
  scopedWf: string | null;
  onOpenWorkflow: (id: string) => void;
  onTopUp: () => void;
  loading: boolean;
}) {
  const { timeseries, byWorkflow, byEndpoint } = data;
  const { balanceUSD, refreshBalance } = useCredits();
  // One server-scoped source. /usage/settlements merges the user's own
  // workflow-run x402 payments (read back from the run_logs receipts the
  // engine persists per settlement) with their Tendril top-ups, both already
  // scoped to the signed-in user and already sorted newest-first — see
  // usage.go's UsageSettlements doc comment.
  //
  // The x402 half used to be rebuilt client-side into localStorage, which
  // followed the browser rather than the account: the rows vanished on
  // sign-out and did not follow the user to another device, even though the
  // receipts were in the database the whole time.
  const [settlements, setSettlements] = useState<Settlement[]>([]);
  // How many settlements are shown before "Show all".
  //
  // On a phone the table stacks into cards rather than scrolling sideways, and
  // 18 stacked cards is roughly a screen and a half of a list nobody scrolls to
  // the end of. Six is enough to see the shape of recent activity, which is
  // what this card is for; the rest are one tap away. Progressive disclosure is
  // the standard answer to a long table on a small screen.
  //
  // Desktop is unaffected: it keeps its five-column table and shows all 18.
  const handheld = useIsHandheld();
  const [showAllSettlements, setShowAllSettlements] = useState(false);

  // Collapse again when the range changes, because the expansion was a decision
  // about a particular set of rows and those rows are now different ones. Left
  // alone, "show all 18" silently became "show all" of whatever the next range
  // returned -- nobody asked for that, and on a wider range it is a long table
  // they did not open.
  //
  // Adjusted during render rather than in an effect: this is the pattern React
  // documents for resetting state when a value changes, it avoids the extra
  // commit an effect would cost, and this repo lints against setState in
  // effects (AuthPage carries a disable comment for exactly that rule).
  const [rangeShown, setRangeShown] = useState(range);
  if (rangeShown !== range) {
    setRangeShown(range);
    setShowAllSettlements(false);
  }

  const settlementCap =
    handheld && !showAllSettlements ? 6 : settlements.length;
  const visibleSettlements = settlements.slice(0, settlementCap);
  useEffect(() => {
    let stale = false;
    usageApi
      .settlements(18)
      .then((rows) => {
        if (!stale) setSettlements(rows);
      })
      .catch(() => {
        /* transient failure: leave whatever loaded last */
      });
    return () => {
      stale = true;
    };
  }, []);
  // The balance is server state, not local state — fetch it when this page
  // mounts so it reflects spend from runs made elsewhere in the app.
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  const workflows = scopedWf
    ? byWorkflow.filter((w) => w.workflowId === scopedWf)
    : byWorkflow;

  // Cost-by-category from endpoint totals.
  const catTotals = useMemo(() => {
    const t: Record<UsageCategory, number> = { x402: 0, llm: 0, action: 0 };
    for (const e of byEndpoint) t[e.type] += e.totalAlgo;
    return t;
  }, [byEndpoint]);
  const catTotal = catTotals.x402 + catTotals.llm + catTotals.action;
  const segments: DonutSegment[] = (
    ["x402", "llm", "action"] as UsageCategory[]
  ).map((k) => ({
    label: CAT_LABEL[k],
    value: catTotals[k],
    color: CAT_COLOR[k],
  }));

  const maxWfAlgo = Math.max(...workflows.map((x) => x.algo), 1e-9);

  return (
    <div style={{ opacity: loading ? 0.6 : 1, transition: "opacity .15s" }}>
      <LowBalanceBanner onTopUp={onTopUp} />
      {/* Header row above the Endpoints table: credits left (left) mirrors the range selector (right).
          Keep the empty headspace above it -- content starts low on the page. */}
      <div
        className="reveal reveal-delay-1"
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "flex-end",
          gap: 16,
          flexWrap: "wrap",
          // Unconditional 60px of headspace, which on a 812px phone is 7% of
          // the screen spent on nothing. Tokenised so the phone step drops it.
          paddingTop: "var(--us-headspace)",
          marginBottom: 12,
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "flex-end",
            gap: 14,
            flexWrap: "wrap",
            // Tokens, defaulting to what this was: see the 520 block beside
            // --us-headspace in globals.css.
            flex: "var(--us-credits-group-flex, 0 1 auto)",
          }}
        >
          {(() => {
            const b = data.summary.budget;
            // "Credits left" is the prepaid wallet — now the backend balance
            // (GET /credits/balance), the same row the engine debits, so this
            // page, the billing page and the canvas topbar cannot disagree.
            // Everything here is USD end to end (ALGO_USD is 1). The plan
            // allowance (b.limit) is only the progress-bar reference; it is
            // never added to the figure.
            const walletAlgo = balanceUSD / ALGO_USD;
            const left = balanceUSD > 0 || b ? walletAlgo : null;
            const limit = b ? b.limit : 0;
            const pctLeft =
              limit > 0 ? Math.max(0, Math.min(1, walletAlgo / limit)) : null;
            const tone =
              pctLeft == null
                ? "var(--accent)"
                : pctLeft < 0.1
                  ? "var(--danger)"
                  : pctLeft < 0.25
                    ? "#FB923C"
                    : "var(--accent)";
            return (
              <div
                style={{
                  flex: "var(--us-credits-flex, 0 0 auto)",
                  minWidth: "var(--us-credits-minw, 300px)",
                  maxWidth: "100%",
                  background: "var(--bg-elev-1)",
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-3)",
                  padding: "16px 20px",
                  display: "flex",
                  flexDirection: "column",
                  gap: 8,
                  position: "relative",
                }}
              >
                <button
                  type="button"
                  className="credit-topup"
                  aria-label="Credit top-up"
                  title="Credit top-up"
                  onClick={onTopUp}
                >
                  <svg
                    width="15"
                    height="15"
                    viewBox="0 0 15 15"
                    fill="none"
                    aria-hidden="true"
                  >
                    <path
                      d="M7.5 2v11M2 7.5h11"
                      stroke="currentColor"
                      strokeWidth="1.6"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    textTransform: "uppercase",
                    letterSpacing: "0.12em",
                    color: "var(--fg-dim)",
                  }}
                >
                  credits left
                </span>
                <div
                  style={{ display: "flex", alignItems: "baseline", gap: 10 }}
                >
                  <span
                    style={{
                      // Token, not a literal: see the 768 block in globals.css.
                      fontSize: "var(--us-figure-size)",
                      fontWeight: 500,
                      lineHeight: 1,
                      letterSpacing: "-0.02em",
                      color: tone,
                    }}
                  >
                    {left == null ? "…" : compactUsd(left)}
                  </span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 14,
                      color: "var(--fg-muted)",
                    }}
                  >
                    USD
                  </span>
                </div>
                {pctLeft != null && (
                  <div
                    style={{ display: "flex", alignItems: "center", gap: 10 }}
                  >
                    <div
                      style={{
                        flex: 1,
                        height: 4,
                        background: "var(--accent-soft)",
                        borderRadius: 999,
                        overflow: "hidden",
                      }}
                    >
                      <div
                        style={{
                          height: "100%",
                          width: `${pctLeft * 100}%`,
                          background: tone,
                          borderRadius: 999,
                        }}
                      />
                    </div>
                    <span
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        color: "var(--fg-dim)",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {Math.round(pctLeft * 100)}% left
                    </span>
                  </div>
                )}
              </div>
            );
          })()}
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "flex-end",
            gap: 14,
            flexWrap: "wrap",
          }}
        >
          <RangeSelector range={range} onChange={onRangeChange} />
        </div>
      </div>

      {/* ④ Endpoints table -- first */}
      <EndpointTable
        rows={byEndpoint}
        className="reveal reveal-delay-1"
        style={{ marginBottom: 16 }}
      />

      {/* ② Usage + Spend merged -- two distinct-coloured lines, combined tooltip */}
      <Card className="reveal reveal-delay-2" style={{ marginBottom: 16 }}>
        <CardHead
          title="Usage & Spend over time"
          right={
            <Legend
              items={[
                { c: "var(--accent)", label: "Spend (USD)" },
                { c: "var(--warm)", label: "Usage (calls)" },
              ]}
            />
          }
        />
        <div style={{ padding: "4px 4px 0" }}>
          <AreaChart data={timeseries} algoUsd={ALGO_USD} />
        </div>
      </Card>

      {/* ③ Two-column */}
      <div
        className="usage-split reveal reveal-delay-3"
        style={{ marginBottom: 16 }}
      >
        {/* Workflows by spend */}
        <Card>
          <CardHead title="Workflows by spend" />
          <div style={{ padding: "4px 0 8px" }}>
            {workflows.length === 0 ? (
              <Empty text="no workflow spend in range" />
            ) : (
              workflows.slice(0, 6).map((w) => (
                <button
                  key={w.workflowId}
                  onClick={() => onOpenWorkflow(w.workflowId)}
                  style={spendRowStyle}
                >
                  <span
                    style={{
                      fontSize: 13,
                      color: "var(--fg)",
                      minWidth: 0,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      flex: "0 0 40%",
                      textAlign: "left",
                    }}
                  >
                    {w.name}
                  </span>
                  <span
                    style={{
                      flex: 1,
                      height: 6,
                      background: "var(--accent-soft)",
                      borderRadius: 999,
                      overflow: "hidden",
                    }}
                  >
                    <span
                      style={{
                        display: "block",
                        height: "100%",
                        width: `${(w.algo / maxWfAlgo) * 100}%`,
                        background: "var(--accent)",
                        borderRadius: 999,
                      }}
                    />
                  </span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      color: "var(--accent)",
                      flex: "0 0 auto",
                      minWidth: 78,
                      textAlign: "right",
                    }}
                  >
                    {usd(w.algo)}{" "}
                    <span style={{ color: "var(--fg-dim)" }}>USD</span>
                  </span>
                </button>
              ))
            )}
          </div>
        </Card>

        {/* Cost by category */}
        <Card>
          <CardHead title="Cost by category" />
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 24,
              padding: "12px 4px 8px",
              flexWrap: "wrap",
            }}
          >
            <Donut
              segments={segments}
              centerLabel={usd(catTotal)}
              centerSub="USD"
            />
            <div
              style={{
                flex: 1,
                minWidth: 160,
                display: "flex",
                flexDirection: "column",
                gap: 12,
              }}
            >
              {segments.map((s) => (
                <div
                  key={s.label}
                  style={{ display: "flex", alignItems: "center", gap: 10 }}
                >
                  <span
                    style={{
                      width: 10,
                      height: 10,
                      borderRadius: 3,
                      background: s.color,
                      flexShrink: 0,
                    }}
                  />
                  <span style={{ fontSize: 13, color: "var(--fg)" }}>
                    {s.label}
                    {s.label === "LLM" && (
                      <span style={{ color: "var(--fg-dim)" }}>*</span>
                    )}
                  </span>
                  <span
                    style={{
                      marginLeft: "auto",
                      fontFamily: "var(--font-mono)",
                      fontSize: 12,
                      color: "var(--fg)",
                    }}
                  >
                    {usd(s.value)}
                  </span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      color: "var(--fg-dim)",
                      minWidth: 38,
                      textAlign: "right",
                    }}
                  >
                    {catTotal > 0 ? Math.round((s.value / catTotal) * 100) : 0}%
                  </span>
                </div>
              ))}
            </div>
          </div>
        </Card>
      </div>

      {/* ⑤ Recent settlements -- on-chain, kept in ALGO */}
      <Card style={{ marginBottom: 16 }}>
        <CardHead
          title="Recent settlements"
          right={
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                color: "var(--fg-dim)",
              }}
            >
              latest {settlements.length} · on-chain · testnet
            </span>
          }
        />
        <HScroll>
          <div
            style={{
              minWidth: "var(--us-settle-minw)",
              display: "grid",
              gridTemplateColumns: SETTLE_GRID,
              gap: 14,
              padding: "8px 10px",
              background: "var(--bg-elev-2)",
              borderRadius: "var(--r-2)",
              marginTop: 4,
              alignItems: "center",
            }}
          >
            <span style={hcell}>Endpoint</span>
            <span style={hcell}>Hash</span>
            <span style={hcell}>Workflow</span>
            <span style={{ ...hcell, textAlign: "right" }}>Amount</span>
            <span style={{ ...hcell, textAlign: "right" }}>Time</span>
          </div>
          <div style={{ minWidth: "var(--us-settle-minw)", padding: "2px 0" }}>
            {visibleSettlements.map((s, i) => (
              <div
                key={s.txId}
                style={{
                  display: "grid",
                  gridTemplateColumns: SETTLE_GRID,
                  gap: 14,
                  alignItems: "center",
                  padding: "11px 10px",
                  borderBottom:
                    i < visibleSettlements.length - 1
                      ? "1px solid var(--border-soft)"
                      : "none",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                }}
              >
                <span
                  style={{
                    color: "var(--fg-muted)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {s.endpoint}
                </span>
                <a
                  href={s.explorerURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{
                    color: "#E879F9",
                    textDecoration: "underline",
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {s.txId.slice(0, 13)}…
                </a>
                <span
                  style={{
                    color: "var(--fg-dim)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {s.workflowId || "-"}
                </span>
                <span style={{ color: "var(--fg)", textAlign: "right" }}>
                  {s.amountAlgo.toFixed(6)}{" "}
                  <span style={{ color: "var(--fg-dim)" }}>ALGO</span>
                </span>
                <span style={{ color: "var(--fg-dim)", textAlign: "right" }}>
                  {relTime(s.ts)}
                </span>
              </div>
            ))}
          </div>
        </HScroll>
        {/* Only when there is something hidden, and only where it was hidden.
            Says how many rather than just "more", so the tap is an informed
            one.

            OUTSIDE the HScroll, and that placement is the whole point. The rows
            above are held open by --us-settle-minw (720px) against a card about
            343px wide on a phone, so the pane scrolls sideways. A block box in
            normal flow does not grow to its overflowing children, so `width:
            100%` resolved against the pane's own ~343px content box rather than
            the 720px canvas: the button was laid out at the left edge of a
            surface that scrolls, and scrolling right to read Amount and Time
            carried the one CTA off screen with it -- exactly when somebody has
            scrolled far enough to want it. The fade this PR adds makes that
            scroll likelier, which is what turned a latent bug into a real one. */}
        {settlements.length > visibleSettlements.length && (
          <button
            type="button"
            onClick={() => setShowAllSettlements(true)}
            style={{
              ...ghostBtnSm,
              width: "100%",
              // ghostBtnSm is 28px, which is a pointer size. This one is
              // tapped, so it takes the 44px floor.
              minHeight: 44,
              marginTop: 8,
            }}
          >
            Show all {settlements.length} settlements
          </button>
        )}
      </Card>

      {/* ⑥ Footer note */}
      <p
        style={{
          margin: "20px 4px 0",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: "var(--fg-dim)",
          letterSpacing: "0.02em",
        }}
      >
        Usage updates after each run. LLM prices (*) are estimated from provider
        list rates using a 3:1 input-to-output token ratio.
      </p>
    </div>
  );
}

// ── Endpoints table ─────────────────────────────────────────────────────────
// "type" sorts by the rank below, not alphabetically -- a-l-x (action, llm,
// x402) reads as noise. x402 first matches the filter pills and the spend
// order the rest of the page is built around.
type SortKey =
  | "endpoint"
  | "type"
  | "calls"
  | "unitPrice"
  | "totalAlgo"
  | "pctOfSpend"
  | "successRate"
  | "lastUsedAt";

const TYPE_RANK: Record<UsageCategory, number> = { x402: 0, llm: 1, action: 2 };

// A column's first click should land on the direction that reads best for its
// data: text/category ascending (A→Z, x402 first), numbers descending (biggest
// spend first). Without this every column opened reversed.
const ASC_FIRST: readonly SortKey[] = ["endpoint", "type"];

// Unit price gets 120px so "26*/1M" fits on one line (cell is nowrap).
const EP_GRID = "1.9fr 1.15fr 66px 66px 120px 108px 116px 78px 92px";
// Reads the custom property rather than repeating its value. globals.css has
// carried a <=768px collapse for --us-settle-cols since it was written, and it
// has never once applied, because this constant hardcoded the same string and
// won. Endpoint · Hash · Workflow · Amount · Time.
const SETTLE_GRID = "var(--us-settle-cols)";

function EndpointTable({
  rows,
  className,
  style,
}: {
  rows: EndpointUsage[];
  className?: string;
  style?: React.CSSProperties;
}) {
  const [cat, setCat] = useState<"all" | UsageCategory>("all");
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; dir: "asc" | "desc" }>({
    key: "totalAlgo",
    dir: "desc",
  });

  const filtered = useMemo(() => {
    const ql = q.trim().toLowerCase();
    const out = rows.filter(
      (r) =>
        (cat === "all" || r.type === cat) &&
        (!ql ||
          r.endpoint.toLowerCase().includes(ql) ||
          r.provider.toLowerCase().includes(ql) ||
          r.host.toLowerCase().includes(ql)),
    );
    out.sort((a, b) => {
      const av = a[sort.key],
        bv = b[sort.key];
      let cmp: number;
      // Type only has 3 values, so it leaves big ties -- order those by spend
      // (desc) rather than leaving each group in arbitrary fixture order.
      if (sort.key === "type")
        cmp =
          TYPE_RANK[a.type] - TYPE_RANK[b.type] || b.totalAlgo - a.totalAlgo;
      else if (typeof av === "string" && typeof bv === "string")
        cmp = av.localeCompare(bv);
      else {
        const an = av == null ? -Infinity : Number(av);
        const bn = bv == null ? -Infinity : Number(bv);
        cmp = an - bn;
      }
      return sort.dir === "asc" ? cmp : -cmp;
    });
    return out;
  }, [rows, cat, q, sort]);

  const toggle = (key: SortKey) =>
    setSort((s) =>
      s.key === key
        ? { key, dir: s.dir === "asc" ? "desc" : "asc" }
        : { key, dir: ASC_FIRST.includes(key) ? "asc" : "desc" },
    );

  return (
    <Card className={className} style={style}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "2px 2px 14px",
          flexWrap: "wrap",
        }}
      >
        <div style={{ fontSize: 14, fontWeight: 500, color: "var(--fg)" }}>
          Endpoints &amp; APIs
        </div>
        <div
          style={{
            display: "flex",
            gap: 2,
            background: "var(--bg-elev-2)",
            padding: 3,
            borderRadius: "var(--r-2)",
            border: "1px solid var(--border)",
          }}
        >
          {(["all", "x402", "llm", "action"] as const).map((c) => (
            <button
              key={c}
              onClick={() => setCat(c)}
              style={{
                border: "none",
                background: cat === c ? "var(--bg-elev-3)" : "transparent",
                color: cat === c ? "var(--fg)" : "var(--fg-muted)",
                padding: "5px 11px",
                fontSize: 12,
                fontWeight: 500,
                borderRadius: 5,
                cursor: "pointer",
                fontFamily: "var(--font-sans)",
                textTransform: "capitalize",
              }}
            >
              {c === "llm" ? "LLM" : c}
            </button>
          ))}
        </div>
        <div style={{ flex: 1 }} />
        <div style={{ position: "relative" }}>
          <span
            style={{
              position: "absolute",
              left: 10,
              top: 9,
              color: "var(--fg-dim)",
            }}
          >
            <IconSearch size={12} />
          </span>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search endpoint / provider…"
            style={{
              height: 32,
              paddingLeft: 30,
              paddingRight: 12,
              width: "100%",
              maxWidth: 240,
              background: "var(--bg-elev-2)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-2)",
              color: "var(--fg)",
              fontFamily: "var(--font-sans)",
              fontSize: 12,
              outline: "none",
            }}
          />
        </div>
      </div>

      <HScroll>
        <div style={{ minWidth: 984 }}>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: EP_GRID,
              gap: 10,
              padding: "8px 10px",
              background: "var(--bg-elev-2)",
              borderRadius: "var(--r-2)",
              alignItems: "center",
            }}
          >
            <Th k="endpoint" sort={sort} onToggle={toggle}>
              Endpoint
            </Th>
            <span style={hcell}>Provider</span>
            <Th k="type" sort={sort} onToggle={toggle}>
              Type
            </Th>
            <Th k="calls" align="right" sort={sort} onToggle={toggle}>
              Calls
            </Th>
            <Th k="unitPrice" align="right" sort={sort} onToggle={toggle}>
              Unit price
            </Th>
            <Th k="totalAlgo" align="right" sort={sort} onToggle={toggle}>
              Total
            </Th>
            <Th k="pctOfSpend" align="right" sort={sort} onToggle={toggle}>
              % spend
            </Th>
            <Th k="successRate" align="right" sort={sort} onToggle={toggle}>
              Success
            </Th>
            <Th k="lastUsedAt" align="right" sort={sort} onToggle={toggle}>
              Last used
            </Th>
          </div>

          {filtered.length === 0 ? (
            <Empty text="no endpoints match" />
          ) : (
            filtered.map((r) => (
              <div
                key={r.endpoint}
                style={{
                  display: "grid",
                  gridTemplateColumns: EP_GRID,
                  gap: 10,
                  padding: "12px 10px",
                  alignItems: "center",
                  borderBottom: "1px solid var(--border-soft)",
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div
                    style={{
                      fontSize: 13,
                      color: "var(--fg)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {r.endpoint}
                  </div>
                  <div
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      color: "var(--fg-dim)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {r.host}
                  </div>
                </div>
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 11,
                    color: "var(--fg-muted)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {r.provider}
                </span>
                <TypeTag type={r.type} />
                <span style={numCell}>{r.calls.toLocaleString()}</span>
                {/* Both money columns are USD like every other figure on the page --
                  a bare "6.110" reads as dollars but is ALGO (~6× off). The exact
                  on-chain ALGO amount stays available on hover for anyone
                  cross-checking settlements. LLM unit prices are estimates
                  (see footer) -- the * marks the price, not the total. */}
                <span
                  className={r.unitPrice != null ? "cell-tip" : undefined}
                  data-tip={
                    r.unitPrice != null
                      ? `$${trim(r.unitPrice)}/${r.unit}`
                      : undefined
                  }
                  style={{
                    ...numCell,
                    color: "var(--fg-muted)",
                    whiteSpace: "nowrap",
                  }}
                >
                  {r.unitPrice != null ? (
                    <>
                      ${usdPrice(r.unitPrice)}
                      {r.type === "llm" && "*"}
                      <span style={{ color: "var(--fg-dim)" }}>/{r.unit}</span>
                    </>
                  ) : (
                    "-"
                  )}
                </span>
                <span
                  className="cell-tip"
                  data-tip={`$${trim(r.totalAlgo)}`}
                  style={{ ...numCell, color: "var(--accent)" }}
                >
                  ${usd(r.totalAlgo)}
                </span>
                <span
                  style={{
                    ...numCell,
                    display: "flex",
                    alignItems: "center",
                    gap: 6,
                    justifyContent: "flex-end",
                  }}
                >
                  <span
                    style={{
                      width: 34,
                      height: 5,
                      background: "var(--accent-soft)",
                      borderRadius: 999,
                      overflow: "hidden",
                      flexShrink: 0,
                    }}
                  >
                    <span
                      style={{
                        display: "block",
                        height: "100%",
                        width: `${Math.min(100, r.pctOfSpend)}%`,
                        background: "var(--accent)",
                      }}
                    />
                  </span>
                  {/* Fixed-width value box so the bars line up in a column -- with the
                    text free-width, wider values pushed each row's bar to a different x. */}
                  <span style={{ minWidth: 40, textAlign: "right" }}>
                    {r.pctOfSpend}%
                  </span>
                </span>
                <span
                  style={{
                    ...numCell,
                    color:
                      r.successRate == null
                        ? "var(--fg-dim)"
                        : r.successRate < 90
                          ? "var(--danger)"
                          : r.successRate < 98
                            ? "var(--warm)"
                            : "var(--fg-muted)",
                  }}
                >
                  {r.successRate == null ? "-" : `${r.successRate}%`}
                </span>
                <span style={{ ...numCell, color: "var(--fg-muted)" }}>
                  {relTime(r.lastUsedAt)}
                </span>
              </div>
            ))
          )}
        </div>
      </HScroll>
    </Card>
  );
}

// Sortable column header -- declared at module scope (not inside EndpointTable's
// render) so it isn't recreated every render. Sort state + toggle come via props.
function Th({
  k,
  children,
  align = "left",
  sort,
  onToggle,
}: {
  k: SortKey;
  children: React.ReactNode;
  align?: "left" | "right";
  sort: { key: SortKey; dir: "asc" | "desc" };
  onToggle: (k: SortKey) => void;
}) {
  return (
    <button
      onClick={() => onToggle(k)}
      style={{
        background: "transparent",
        border: "none",
        cursor: "pointer",
        padding: 0,
        color: sort.key === k ? "var(--fg-muted)" : "var(--fg-dim)",
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        textTransform: "uppercase",
        letterSpacing: "0.08em",
        display: "inline-flex",
        // Centred, not the flex default of stretch: on a touch screen the
        // button is given a 44px floor, and without this the label would sit
        // at the top of it while the plain header cells beside it stay
        // centred.
        alignItems: "center",
        gap: 3,
        justifyContent: align === "right" ? "flex-end" : "flex-start",
      }}
    >
      {children}
      {sort.key === k ? (sort.dir === "asc" ? "▴" : "▾") : ""}
    </button>
  );
}

// ── Small building blocks ───────────────────────────────────────────────────
function RangeSelector({
  range,
  onChange,
}: {
  range: UsageRange;
  onChange: (r: UsageRange) => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        gap: 2,
        background: "var(--bg-elev-1)",
        padding: 3,
        borderRadius: "var(--r-2)",
        border: "1px solid var(--border)",
      }}
    >
      {RANGES.map((r) => (
        <button
          key={r}
          onClick={() => onChange(r)}
          style={{
            border: "none",
            background: range === r ? "var(--bg-elev-3)" : "transparent",
            color: range === r ? "var(--fg)" : "var(--fg-muted)",
            padding: "6px 14px",
            fontSize: 12,
            fontWeight: 500,
            borderRadius: 5,
            cursor: "pointer",
            fontFamily: "var(--font-mono)",
          }}
        >
          {r}
        </button>
      ))}
    </div>
  );
}

function TypeTag({ type }: { type: UsageCategory }) {
  const c = TYPE_PILL[type];
  return (
    <span
      style={{
        justifySelf: "start",
        display: "inline-flex",
        alignItems: "center",
        height: 20,
        padding: "0 8px",
        borderRadius: 999,
        border: `1px solid ${c}55`,
        background: `${c}1A`,
        color: c,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        letterSpacing: "0.04em",
      }}
    >
      {type === "llm" ? "LLM" : type}
    </span>
  );
}

function CardHead({
  title,
  right,
}: {
  title: string;
  right?: React.ReactNode;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        marginBottom: 6,
      }}
    >
      <div style={{ fontSize: 14, fontWeight: 500, color: "var(--fg)" }}>
        {title}
      </div>
      {right}
    </div>
  );
}

function Legend({ items }: { items: { c: string; label: string }[] }) {
  return (
    <div style={{ display: "flex", gap: 14 }}>
      {items.map((i) => (
        <span
          key={i.label}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--fg-muted)",
          }}
        >
          <span
            style={{ width: 8, height: 8, borderRadius: 999, background: i.c }}
          />
          {i.label}
        </span>
      ))}
    </div>
  );
}

// A sideways-scrolling table that says so.
//
// Both usage tables are wider than a phone and always were: the settlements
// card was measured as a two-up stack and came out worse (833px against 18
// rows that scrolled), and the endpoints table is nine sortable columns. The
// problem was never the scrolling, it was that nothing announced it -- at
// 375px the endpoints table shows 305 of its 984 pixels and reads as broken,
// with a type pill sliced in half at the edge.
//
// So this adds the one thing that was missing: a fade at the right edge while
// there is more to reach, gone once there is not. See .am-hscroll.
function HScroll({ children }: { children: React.ReactNode }) {
  const paneRef = useRef<HTMLDivElement>(null);
  // Starts true so nothing flashes a fade over a table that fits. The first
  // measurement arrives from the observer below, before paint in practice.
  const [atEnd, setAtEnd] = useState(true);

  const measure = useCallback(() => {
    const el = paneRef.current;
    if (!el) return;
    // 1px of slack: with fractional column widths scrollLeft never lands
    // exactly on the end, and an off-by-a-fraction leaves the fade up forever.
    setAtEnd(el.scrollLeft + el.clientWidth >= el.scrollWidth - 1);
  }, []);

  // A ResizeObserver rather than a resize listener, and deliberately: this has
  // to re-measure when the CONTENT changes width too -- filtering the endpoint
  // list or switching the range does that without the window moving at all.
  // Its callback is also asynchronous, which is what keeps the first
  // measurement out of the effect body.
  useEffect(() => {
    const el = paneRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => ro.disconnect();
  }, [measure]);

  return (
    <div className="am-hscroll" data-at-end={atEnd ? "" : undefined}>
      <div className="am-hscroll__pane" ref={paneRef} onScroll={measure}>
        {children}
      </div>
    </div>
  );
}

function Empty({ text }: { text: string }) {
  return (
    <div
      style={{
        padding: 28,
        textAlign: "center",
        color: "var(--fg-dim)",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
      }}
    >
      {text}
    </div>
  );
}

// ── formatting helpers ──────────────────────────────────────────────────────
// Per-unit prices are often sub-cent ($0.00034/quote), so unlike usd() this
// keeps up to 5 fraction digits instead of rounding everything to 2.
function usdPrice(algoAmount: number) {
  return (algoAmount * ALGO_USD).toLocaleString("en", {
    maximumFractionDigits: 5,
  });
}
function trim(n: number) {
  return n.toLocaleString("en", {
    minimumFractionDigits: 0,
    maximumFractionDigits: 4,
  });
}

const hcell: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 10,
  textTransform: "uppercase",
  letterSpacing: "0.08em",
  color: "var(--fg-dim)",
};
const numCell: React.CSSProperties = {
  fontFamily: "var(--font-mono)",
  fontSize: 12,
  color: "var(--fg)",
  textAlign: "right",
};
const spendRowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  width: "100%",
  padding: "9px 4px",
  background: "transparent",
  border: "none",
  borderBottom: "1px solid var(--border-soft)",
  cursor: "pointer",
};
