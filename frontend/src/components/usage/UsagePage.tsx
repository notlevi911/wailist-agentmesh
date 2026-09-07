"use client";
import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { useCredits } from "@/lib/credits/store";
import { LowBalanceBanner } from "@/components/billing/LowBalanceBanner";
import { IconSearch, Card, ghostBtnSm } from "@/components/ui";
import { Topbar } from "@/components/Topbar";
import { usage as usageApi } from "@/lib/api";
import {
  UsageRange,
  UsagePayload,
  EndpointUsage,
  UsageCategory,
  Settlement,
} from "@/lib/types";
import { AreaChart } from "./AreaChart";
import { Donut, DonutSegment } from "./Donut";

const RANGES: UsageRange[] = ["24h", "7d", "30d"];

// x402 = accent, LLM = info, action = the orange already used in LogDrawer.
const CAT_COLOR: Record<UsageCategory, string> = {
  x402: "var(--accent)",
  llm: "var(--info)",
  action: "#FB923C",
};
// Endpoint type pill keeps the x402 magenta used elsewhere (tx links / tool402).
const TYPE_PILL: Record<UsageCategory, string> = {
  x402: "#E879F9",
  llm: "#6EA8FF", // hex (matches --info) so the `${c}55`/`${c}1A` alpha suffixes stay valid
  action: "#FB923C",
};
const CAT_LABEL: Record<UsageCategory, string> = {
  x402: "x402",
  llm: "LLM",
  action: "Actions",
};

export function UsagePage() {
  const router = useRouter();

  const [range, setRange] = useState<UsageRange>("30d");
  const [data, setData] = useState<UsagePayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<Error | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);
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
    Promise.all([
      usageApi.summary(range),
      usageApi.timeseries(range),
      usageApi.byWorkflow(range),
      usageApi.byEndpoint(range),
      usageApi.settlements(18),
    ])
      .then(([summary, timeseries, byWorkflow, byEndpoint, settlements]) => {
        if (cancelled) return;
        setData({ summary, timeseries, byWorkflow, byEndpoint, settlements });
      })
      .catch((e) => {
        if (cancelled) return;
        // Surface the failure but keep the last good payload -- a transient error
        // on a range switch shouldn't blank a page that was already working.
        console.error("usage load failed", e);
        setLoadError(e instanceof Error ? e : new Error(String(e)));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
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
  const retry = () => {
    usageApi.invalidate();
    setLoading(true);
    setLoadError(null);
    setReloadNonce((n) => n + 1);
  };

  // Clearing the scope must also drop ?workflow= from the URL, otherwise a
  // refresh or back-navigation silently reapplies the filter the user just cleared.
  const clearScope = () => {
    setScopedWf(null);
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    url.searchParams.delete("workflow");
    window.history.replaceState(null, "", url.pathname + url.search + url.hash);
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
              Workflows by spend · filtered to {scopedWf}
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
              onOpenWorkflow={(id) => router.push(`/workflows/${id}`)}
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
          paddingTop: 60,
          marginBottom: 12,
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "flex-end",
            gap: 14,
            flexWrap: "wrap",
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
                  flex: "0 0 auto",
                  minWidth: 300,
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
                      fontSize: 40,
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
        <div className="am-usage-table">
          <div
            style={{
              minWidth: 720,
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
          <div style={{ minWidth: 720, padding: "2px 0" }}>
            {settlements.map((s, i) => (
              <div
                key={s.txId}
                style={{
                  display: "grid",
                  gridTemplateColumns: SETTLE_GRID,
                  gap: 14,
                  alignItems: "center",
                  padding: "11px 10px",
                  borderBottom:
                    i < settlements.length - 1
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
                  {s.workflowId || "—"}
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
        </div>
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
        settled on Algorand testnet · figures update ~every run · LLM prices (*)
        estimated from provider list rates at ~3:1 in:out tokens
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
const SETTLE_GRID = "minmax(0,1.9fr) minmax(0,1.15fr) 140px 114px 108px"; // Endpoint · Hash · Workflow · Amount · Time

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

      <div style={{ overflowX: "auto" }}>
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
                      fontSize: 9,
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
      </div>
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
// Spend figures arrive from /usage/* already denominated in USD — they come
// from debit_ledger.amount_usd_micros, the same units credits are sold and
// billed in. The multiplier is retained (as 1) rather than deleted so the call
// sites stay honest about doing no conversion; applying the old 0.17 ALGO rate
// to USD figures would under-report every number on this page by ~6x.
const ALGO_USD = 1;
function usd(algoAmount: number, dp = 2) {
  return (algoAmount * ALGO_USD).toLocaleString("en", {
    minimumFractionDigits: dp,
    maximumFractionDigits: dp,
  });
}
// Compact USD for the credit balance -- keeps large figures small (100K, 50, 2.3M).
function compactUsd(algoAmount: number) {
  return Intl.NumberFormat("en", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(algoAmount * ALGO_USD);
}
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
function relTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
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
