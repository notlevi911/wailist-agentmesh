"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { Skeleton } from "@/components/ui/Skeleton";
import { PullToRefresh } from "@/components/PullToRefresh";
import { ExternalLink } from "@/components/ExternalLink";
import { workflowHref } from "@/lib/routes";
import { scopedWorkflowLabel } from "@/lib/usageScope";
import { useCredits } from "@/lib/credits/store";
import type { UsageCategory, UsagePayload, UsageRange } from "@/lib/types";
import { AreaChart } from "../AreaChart";
import { Donut } from "../Donut";
import {
  ALGO_USD,
  CAT_COLOR,
  CAT_LABEL,
  TYPE_PILL,
  centreFigure,
  relTime,
  usd,
} from "../format";

// Usage on a phone.
//
// The desktop page is built around two wide tables -- endpoints at 984px and
// settlements at 720px -- which on a phone scroll sideways inside a card a
// third that wide. Here each table becomes a short list of rows with the one
// figure that matters on the right, and "See all" where there is more.
//
// Same data and the same single fetch as the desktop page: UsagePage owns the
// state and hands it down, so a range switch or a retry behaves identically.

const RANGES: UsageRange[] = ["24h", "7d", "30d"];
const CATS: UsageCategory[] = ["x402", "llm", "action"];
// How many rows a list shows before "See all".
const TOP = 5;

const count = new Intl.NumberFormat("en");

export interface UsagePhoneProps {
  range: UsageRange;
  onRange: (r: UsageRange) => void;
  data: UsagePayload | null;
  loading: boolean;
  error: Error | null;
  // Reloads; settles when the new figures have landed.
  onRetry: () => void | Promise<void>;
  scopedWorkflowId: string | null;
  onClearScope: () => void;
}

export function UsagePhonePage(p: UsagePhoneProps) {
  const [allEndpoints, setAllEndpoints] = useState(false);
  // What is left sits beside what was spent. The store keeps a copy across
  // routes that goes stale as runs spend, so it is re-read on arrival.
  const { balanceUSD, balanceKnown, refreshBalance, lowBalanceThresholdUSD } = useCredits();
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);
  const creditState = !balanceKnown
    ? "unknown"
    : balanceUSD < lowBalanceThresholdUSD
      ? "low"
      : "ok";

  const body = !p.data ? (
    p.loading ? (
      <UsageSkeleton />
    ) : (
      <p className="bilp-note" data-tone="error" role="alert">
        Couldn&rsquo;t load usage.{" "}
        <button
          type="button"
          className="bilp-link"
          onClick={() => void p.onRetry()}
        >
          Retry
        </button>
      </p>
    )
  ) : (
    <UsageBody
      data={p.data}
      range={p.range}
      allEndpoints={allEndpoints}
      onToggleEndpoints={() => setAllEndpoints((v) => !v)}
      scopedWorkflowId={p.scopedWorkflowId}
    />
  );

  const scopeLabel = p.scopedWorkflowId
    ? scopedWorkflowLabel(p.scopedWorkflowId, p.data?.byWorkflow)
    : null;

  return (
    <PullToRefresh
      onRefresh={async () => {
        await p.onRetry();
      }}
      style={{ flex: 1, minHeight: 0, background: "var(--bg)" }}
    >
      <main className="bilp-page usgp-page" data-loading={p.loading}>
        <h1 className="bilp-title">Usage</h1>
        <p className="bilp-sub">What your agents spent, and on what.</p>

        {scopeLabel && (
          <div className="usgp-scope" role="status">
            <span>Workflows by spend · filtered to {scopeLabel}</span>
            <button
              type="button"
              className="bilp-link"
              onClick={p.onClearScope}
            >
              Clear
            </button>
          </div>
        )}

        {/* Also the way to top up from here, now that Credits is not a tab. */}
        <Link
          href="/billing"
          className="usgp-row usgp-credit"
          data-state={creditState}
        >
          <span className="usgp-credit__label">Credits left</span>
          <span className="usgp-credit__value">
            {balanceKnown ? `$${usd(balanceUSD)}` : "-"}
            {creditState === "low" && (
              <span className="bilp-pill">
                <span className="bilp-pill__dot" aria-hidden />
                Low
              </span>
            )}
            <span className="usgp-credit__chevron" aria-hidden>
              ›
            </span>
          </span>
        </Link>

        <div className="bilp-seg usgp-seg" role="radiogroup" aria-label="Range">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              role="radio"
              aria-checked={p.range === r}
              className="bilp-seg__item"
              onClick={() => p.range !== r && p.onRange(r)}
            >
              {r}
            </button>
          ))}
        </div>

        {/* A failed refresh keeps the last good figures on screen. */}
        {p.data && p.error && (
          <p className="bilp-note" data-tone="error" role="alert">
            Couldn&rsquo;t refresh; showing the last loaded figures.{" "}
            <button
              type="button"
              className="bilp-link"
              onClick={() => void p.onRetry()}
            >
              Retry
            </button>
          </p>
        )}

        {body}
      </main>
    </PullToRefresh>
  );
}

function UsageBody({
  data,
  range,
  allEndpoints,
  onToggleEndpoints,
  scopedWorkflowId,
}: {
  data: UsagePayload;
  range: UsageRange;
  allEndpoints: boolean;
  onToggleEndpoints: () => void;
  scopedWorkflowId: string | null;
}) {
  const endpointsHeading = useRef<HTMLHeadingElement>(null);
  const settlementsHeading = useRef<HTMLHeadingElement>(null);
  // Five by default, like the endpoints; the rest on request.
  const [allSettlements, setAllSettlements] = useState(false);
  const { summary, timeseries, byWorkflow, byEndpoint, settlements } = data;

  // The same category split the desktop donut draws, from endpoint totals.
  const cats = useMemo(() => {
    const t: Record<UsageCategory, number> = { x402: 0, llm: 0, action: 0 };
    for (const e of byEndpoint) t[e.type] += e.totalAlgo;
    return t;
  }, [byEndpoint]);
  const spent = cats.x402 + cats.llm + cats.action;
  const calls = byEndpoint.reduce((n, e) => n + e.calls, 0);
  const delta = summary.deltas.totalAlgoPct;

  const names = useMemo(
    () => new Map(byWorkflow.map((w) => [w.workflowId, w.name])),
    [byWorkflow],
  );
  const workflows = (
    scopedWorkflowId
      ? byWorkflow.filter((w) => w.workflowId === scopedWorkflowId)
      : [...byWorkflow]
  ).sort((a, b) => b.algo - a.algo);
  const endpoints = [...byEndpoint].sort((a, b) => b.totalAlgo - a.totalAlgo);
  const shownEndpoints = allEndpoints ? endpoints : endpoints.slice(0, TOP);

  if (spent === 0 && calls === 0 && settlements.length === 0) {
    return (
      <p className="bilp-note usgp-empty">
        No usage in the last {range}. Spend shows here once a workflow runs.
      </p>
    );
  }

  return (
    <>
      <section className="bilp-stats">
        <div className="bilp-stat">
          <span className="bilp-stat__label">Spent · {range}</span>
          <span className="bilp-stat__row">
            <span className="bilp-stat__value">${usd(spent)}</span>
            {delta !== 0 && (
              <span className="usgp-delta" data-up={delta > 0}>
                {delta > 0 ? "▲" : "▼"} {Math.abs(delta)}%
              </span>
            )}
          </span>
        </div>
        <div className="bilp-stat">
          <span className="bilp-stat__label">Calls · {range}</span>
          <span className="bilp-stat__value">{count.format(calls)}</span>
        </div>
      </section>

      <section className="bilp-section">
        <h2 className="bilp-heading">Spend over time</h2>
        <div className="usgp-legend">
          <span>
            <i style={{ background: "var(--accent)" }} /> Spend (USD)
          </span>
          <span>
            <i style={{ background: "var(--warm)" }} /> Calls
          </span>
        </div>
        <div className="usgp-chart">
          <AreaChart data={timeseries} algoUsd={ALGO_USD} maxLabels={5} />
        </div>
      </section>

      <section className="bilp-section">
        <h2 className="bilp-heading">By category</h2>
        <div className="usgp-cats">
          <div className="usgp-donut">
            <Donut
              size={120}
              thickness={16}
              segments={CATS.map((k) => ({
                label: CAT_LABEL[k],
                value: cats[k],
                color: CAT_COLOR[k],
              }))}
              centerLabel={centreFigure(spent)}
              ariaLabel={`$${usd(spent)} spent in the last ${range}`}
              centerSub={range}
            />
          </div>
          <ul className="usgp-list">
            {CATS.map((k) => (
              <li key={k} className="usgp-row">
                <span className="usgp-row__main usgp-row__main--inline">
                  <i
                    className="usgp-dot"
                    style={{ background: CAT_COLOR[k] }}
                  />
                  {CAT_LABEL[k]}
                  {k === "llm" && "*"}
                </span>
                <span className="usgp-row__fig">
                  ${usd(cats[k])}
                  <small>
                    {spent > 0 ? Math.round((cats[k] / spent) * 100) : 0}%
                  </small>
                </span>
              </li>
            ))}
          </ul>
        </div>
      </section>

      {workflows.length > 0 && (
        <section className="bilp-section">
          <h2 className="bilp-heading">Workflows by spend</h2>
          <ul className="usgp-list">
            {workflows.slice(0, TOP).map((w) => (
              <li key={w.workflowId}>
                <Link href={workflowHref(w.workflowId)} className="usgp-row">
                  <span className="usgp-row__main">
                    <span className="usgp-row__name">{w.name}</span>
                    <span className="usgp-row__sub">
                      {count.format(w.calls)} calls
                    </span>
                  </span>
                  <span className="usgp-row__fig">${usd(w.algo)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      {endpoints.length > 0 && (
        <section className="bilp-section">
          <h2 className="bilp-heading" ref={endpointsHeading}>
            Endpoints
          </h2>
          <ul className="usgp-list">
            {shownEndpoints.map((e) => (
              <li key={`${e.host}${e.endpoint}`} className="usgp-row">
                <span className="usgp-row__main">
                  <span className="usgp-row__name">{e.endpoint}</span>
                  <span className="usgp-row__sub">
                    <span
                      className="usgp-pill"
                      style={{ color: TYPE_PILL[e.type] }}
                    >
                      {CAT_LABEL[e.type]}
                    </span>
                    {e.provider} · {count.format(e.calls)} calls
                  </span>
                </span>
                <span className="usgp-row__fig">${usd(e.totalAlgo)}</span>
              </li>
            ))}
          </ul>
          {endpoints.length > TOP && (
            <button
              type="button"
              className="bilp-link bilp-link--block"
              aria-expanded={allEndpoints}
              onClick={() => {
                // Shrinking a long list from its bottom would leave the reader
                // looking at the next section with no idea where they were.
                if (allEndpoints) {
                  endpointsHeading.current?.scrollIntoView?.({
                    block: "start",
                  });
                }
                onToggleEndpoints();
              }}
            >
              {allEndpoints
                ? "Show fewer"
                : `See all endpoints (${endpoints.length})`}
            </button>
          )}
        </section>
      )}

      {settlements.length > 0 && (
        <section className="bilp-section">
          <h2 className="bilp-heading" ref={settlementsHeading}>
            Recent settlements
          </h2>
          <ul className="usgp-list">
            {(allSettlements ? settlements : settlements.slice(0, TOP)).map(
              (s) => (
                <li key={s.txId} className="usgp-row">
                  <span className="usgp-row__main">
                    <span className="usgp-row__name">{s.endpoint}</span>
                    <span className="usgp-row__sub">
                      {/* Part of the hash, as the website shows it -- first, so
                        it is never the piece that wraps away. */}
                      {/* The separator rides with the hash, so a long workflow
                        name wraps onto a line that starts with the name
                        rather than with a stray dot. */}
                      <span>
                        <ExternalLink
                          href={s.explorerURL}
                          className="usgp-hash"
                          style={{ color: TYPE_PILL.x402 }}
                          aria-label={`Transaction ${s.txId}`}
                        >
                          {s.txId.slice(0, 10)}…
                        </ExternalLink>{" "}
                        ·
                      </span>
                      <span>
                        {names.get(s.workflowId) ?? "-"} · {relTime(s.ts)}
                      </span>
                    </span>
                  </span>
                  <span className="usgp-row__fig">${usd(s.amountAlgo, 4)}</span>
                </li>
              ),
            )}
          </ul>
          {settlements.length > TOP && (
            <button
              type="button"
              className="bilp-link bilp-link--block"
              aria-expanded={allSettlements}
              onClick={() => {
                // As with endpoints: collapsing from the bottom of a long
                // list would leave the reader in the middle of nowhere.
                if (allSettlements) {
                  settlementsHeading.current?.scrollIntoView?.({
                    block: "start",
                  });
                }
                setAllSettlements((v) => !v);
              }}
            >
              {allSettlements
                ? "Show fewer"
                : `See all settlements (${settlements.length})`}
            </button>
          )}
        </section>
      )}

      <p className="bilp-note usgp-foot">
        LLM prices (*) are estimated from provider list rates.
      </p>
    </>
  );
}

function UsageSkeleton() {
  return (
    <div className="usgp-skeleton" aria-busy="true" aria-label="Loading usage">
      <Skeleton height={56} />
      <Skeleton height={150} />
      <Skeleton height={120} />
    </div>
  );
}
