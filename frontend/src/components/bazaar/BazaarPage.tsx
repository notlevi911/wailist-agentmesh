"use client";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Topbar } from "@/components/Topbar";
import { bazaar, type BazaarResource } from "@/lib/bazaar";
import { ResourceCard } from "./ResourceCard";
import { ConsoleCard } from "./ConsoleCard";
import { EndpointRow } from "./EndpointRow";
import { ProviderGroupCard } from "./ProviderGroupCard";
import { AddToWorkflowDialog } from "./AddToWorkflowDialog";

const PAGE_SIZE = 30;

// The partner track is explicit, not auto-fill. There are two partners; an
// auto-fill grid stretches to four columns on a wide screen and leaves them
// adrift in it, which reads as "two things are missing" rather than "these are
// the two". auto-fit with a max keeps each card at a readable width and lets a
// third slot in cleanly when there is one.
const CONSOLE_GRID: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fit, minmax(320px, 460px))",
  justifyContent: "start",
  gap: 14,
};

// Any supported entry WITHOUT a console still renders as an ordinary card.
// Nothing produces one today (curated.go's TestEveryCuratedEntryIsConsoleBacked
// requires a console key), but a registry that grows a non-console partner
// should degrade to a card rather than vanish from the page.
const GRID: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))",
  gap: 12,
};

// The community list renders as one bordered row-list rather than a card
// grid: a card grid puts variable-height cards into CSS grid cells (no
// masonry), which produced a visibly broken, ragged layout once entries
// with different description lengths sat next to each other. A uniform-
// height row list has no such alignment problem, and reads as the same
// "real API directory" language as the rest of the page's dense data.
const BAZAAR_CSS = `
.bz-list {
  border: 1px solid var(--border);
  border-radius: var(--r-3);
  overflow: hidden;
  background: var(--bg-elev-1);
}
.bz-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 11px 16px;
  border: none;
  border-bottom: 1px solid var(--border);
  background: transparent;
  text-align: left;
  font-family: var(--font-sans);
  color: inherit;
  transition: background 0.15s var(--ease);
}
.bz-row--group {
  cursor: pointer;
}
.bz-row::before {
  content: "";
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 2px;
  background: var(--accent);
  transform: scaleY(0);
  transition: transform 0.15s var(--ease);
}
.bz-row--group:hover,
.bz-row--group:focus-visible {
  background: var(--bg-elev-2);
}
.bz-row--group:hover::before,
.bz-row--group:focus-visible::before {
  transform: scaleY(1);
}
.bz-row__icon {
  width: 26px;
  height: 26px;
  border-radius: var(--r-2);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  flex-shrink: 0;
}
.bz-row__chevron {
  transition: transform 0.2s var(--ease);
}
.bz-row__chevron[data-open="true"] {
  transform: rotate(90deg);
}
.bz-row__name {
  font-size: 13px;
  font-weight: 600;
  color: var(--fg);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.bz-row__path {
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--fg-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex-shrink: 1;
  min-width: 0;
}
.bz-row__desc {
  margin: 2px 0 0;
  font-size: 11.5px;
  color: var(--fg-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.bz-row__meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--fg-dim);
}
.bz-row__price {
  color: var(--fg);
  font-weight: 600;
}
.bz-row__price-unit {
  margin-right: 2px;
}
.bz-row__stat {
  white-space: nowrap;
}
.bz-pill {
  padding: 1px 6px;
  border-radius: var(--r-1);
  background: var(--bg-elev-3);
  border: 1px solid var(--border-strong);
  font-size: 10px;
}
.bz-row__add {
  flex-shrink: 0;
  height: 26px;
  padding: 0 12px;
  border: 1px solid var(--border-strong);
  background: transparent;
  color: var(--fg);
  border-radius: var(--r-2);
  font-size: 11.5px;
  font-weight: 500;
  font-family: var(--font-sans);
  cursor: pointer;
  transition:
    background 0.15s var(--ease),
    border-color 0.15s var(--ease),
    color 0.15s var(--ease);
}
.bz-row__add:hover {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--accent-fg);
}
.bz-group-body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.22s var(--ease);
}
.bz-group-body[data-open="true"] {
  grid-template-rows: 1fr;
}
.bz-group-body__inner {
  overflow: hidden;
  min-height: 0;
  background: var(--bg-elev-2);
}
.bz-supported-card {
  /* Fill the grid cell. Grid stretches this wrapper to the tallest card in
     the row, but the card inside is intrinsically sized, so a short
     description would otherwise leave a visible gap under it and the row
     would look ragged. */
  height: 100%;
}
.bz-supported-card > * {
  height: 100%;
}
@media (prefers-reduced-motion: reduce) {
  .bz-row,
  .bz-row::before,
  .bz-row__chevron,
  .bz-group-body {
    transition: none !important;
  }
}
`;

export function BazaarPage() {
  const [supported, setSupported] = useState<BazaarResource[]>([]);
  const [items, setItems] = useState<BazaarResource[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState<BazaarResource | null>(null);

  // Supported entries collapse by console key: Prism is four endpoints behind
  // one page, and four cards all opening that same page would be four buttons
  // for one destination. Entries without a console key keep one card each.
  // Map preserves first-seen order, so a console surfaces where its first
  // endpoint would have.
  const { consoles, plainSupported } = useMemo(() => {
    const byConsole = new Map<string, BazaarResource[]>();
    const plain: BazaarResource[] = [];
    for (const r of supported) {
      if (!r.console) {
        plain.push(r);
        continue;
      }
      const list = byConsole.get(r.console);
      if (list) list.push(r);
      else byConsole.set(r.console, [r]);
    }
    return { consoles: Array.from(byConsole.entries()), plainSupported: plain };
  }, [supported]);

  // Endpoints sharing a host collapse into one ProviderGroupCard so a single
  // heavy publisher doesn't bury everything else (one host is over 70% of the
  // raw catalog). Grouping is client-side over whatever's loaded so far —
  // this stays correct with the existing paged fetch, no backend change
  // needed, and a group simply grows as more of its host's entries scroll
  // in. Map preserves first-seen order, which is that host's highest
  // settle-count entry's position (items already arrive settle-count
  // sorted) — so a provider surfaces where its best single entry would have.
  // Rebuilds the whole grouping from all of `items` on every loadMore page,
  // not just the newly-appended page -- O(n) per page, O(n²/pageSize) over a
  // full scroll session instead of O(n). Not worth an incremental fold at
  // the current ~780-entry catalog (worst case is a few tens of thousands of
  // trivial operations, well under one frame); revisit if the catalog grows
  // by an order of magnitude or more.
  const groupedItems = useMemo(() => {
    const byHost = new Map<string, BazaarResource[]>();
    for (const r of items) {
      const list = byHost.get(r.host);
      if (list) list.push(r);
      else byHost.set(r.host, [r]);
    }
    return Array.from(byHost.entries());
  }, [items]);
  const [expandedHosts, setExpandedHosts] = useState<Set<string>>(new Set());
  const toggleHost = useCallback((host: string) => {
    setExpandedHosts((cur) => {
      const next = new Set(cur);
      if (next.has(host)) next.delete(host);
      else next.add(host);
      return next;
    });
  }, []);

  // The active search, debounced — separate from `query` so typing does not
  // fire a request per keystroke.
  const [activeQuery, setActiveQuery] = useState("");
  useEffect(() => {
    const t = window.setTimeout(() => setActiveQuery(query.trim()), 250);
    return () => window.clearTimeout(t);
  }, [query]);

  // Supported entries are pinned above the scrolling list, so they are fetched
  // once and never paged. Searching does not filter them — the point of the
  // section is that it is always visible.
  useEffect(() => {
    let cancelled = false;
    // supported: true asks the backend to filter over the FULL merged catalog,
    // not just the first 100-by-settle-count entries — a curated entry with
    // zero catalog matches (e.g. Tendril) is appended past that cutoff, so a
    // client-side .filter() over one page could silently miss it.
    bazaar
      .list({ offset: 0, limit: 100, supported: true })
      .then((page) => {
        if (!cancelled) setSupported(page.items);
      })
      .catch(() => {
        /* the main list surfaces the error; a missing pinned row is not fatal */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Reset the paged list whenever the search changes. Done during render
  // (React's "adjusting state when a prop changes" pattern) rather than in a
  // useEffect, so the reset lands in the same commit as the query change
  // instead of firing a second, cascading render.
  const [resetForQuery, setResetForQuery] = useState(activeQuery);
  const [noMore, setNoMore] = useState(false);
  if (resetForQuery !== activeQuery) {
    setResetForQuery(activeQuery);
    setItems([]);
    setTotal(0);
    setNoMore(false);
    // Without this, a query change right after a failed load leaves the
    // sentinel effect's `if (noMore || loading || error) return;` guard
    // permanently tripped for the NEW query: the stale error message stays
    // on screen with only a manual Retry button instead of the new search
    // auto-loading.
    setError(null);
  }

  // Bumped every time a search reset commits, so an in-flight loadMore
  // response from a stale (pre-reset) request can tell it's stale and skip
  // applying setTotal — items already self-guards via its own offset check,
  // but total has no equivalent natural staleness signal to compare against.
  // This must be useLayoutEffect, not useEffect: a plain useEffect is
  // deferred past paint via a macrotask, leaving a window (until that
  // macrotask runs) during which a stale request's already-queued
  // microtask continuation (the code right after `await bazaar.list(...)`)
  // could read the pre-bump value and slip through. useLayoutEffect runs
  // synchronously in the commit phase, closing that window entirely.
  const requestGeneration = useRef(0);
  useLayoutEffect(() => {
    requestGeneration.current += 1;
  }, [activeQuery]);

  const loadMore = useCallback(async () => {
    if (loading) return;
    setLoading(true);
    setError(null);
    const myGeneration = requestGeneration.current;
    try {
      const offset = items.length;
      const page = await bazaar.list({
        offset,
        limit: PAGE_SIZE,
        q: activeQuery || undefined,
        // The grid only shows unsupported entries — a supported one already
        // renders in the pinned section above, and showing it twice under
        // contradictory copy ("Community listings — you configure the fields
        // yourself") is actively wrong for a supported card.
        supported: false,
      });
      // A reset that happened while this request was in flight bumped the
      // generation counter — this response no longer describes the current
      // query, so skip it entirely. Gating setItems on this too (not just
      // total/noMore) matters: right after a reset, items is [] and offset
      // was captured as 0, so the offset-based guard below is trivially true
      // and would otherwise let a stale first page slip into the new query's
      // state instead of being rejected.
      if (requestGeneration.current === myGeneration) {
        setTotal(page.total);
        // A short page (fewer items than asked for) means there is nothing
        // more to fetch, regardless of what `total` says — `total` counts
        // the whole filtered set and can disagree with pagination in edge
        // cases (e.g. a concurrent catalog refresh), but a short page from
        // the backend is authoritative on its own.
        if (page.items.length < PAGE_SIZE) setNoMore(true);
        // Guard against a concurrent reset landing between the request and
        // its response, which would otherwise append a stale page.
        setItems((cur) => (cur.length === offset ? [...cur, ...page.items] : cur));
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "could not load the catalog");
    } finally {
      setLoading(false);
    }
  }, [items.length, loading, activeQuery]);

  // Sentinel-driven infinite scroll. Re-observes after every load so the
  // callback always closes over the current item count.
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    // noMore (a short page) is the authoritative "stop fetching" signal — see
    // loadMore. Also gate on !error: a failed request leaves `loading` back
    // at false without ever marking noMore, and without this guard the
    // observer effect (re-created because loadMore's identity changed) would
    // immediately re-fire the same failing request forever. Auto-retry was
    // never the intent — the manual "Retry" button below is.
    if (noMore || loading || error) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) loadMore();
      },
      { rootMargin: "300px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [loadMore, noMore, loading, error]);

  // Same signal the observer uses to stop fetching, so the UI's "done" state
  // and the fetch-stopping state can never disagree.
  const exhausted = noMore;

  return (
    <div style={{ minHeight: "100vh", display: "flex", flexDirection: "column" }}>
      <style>{BAZAAR_CSS}</style>
      <Topbar />
      <div style={{ padding: "24px 24px 64px", maxWidth: 1180, width: "100%", margin: "0 auto" }}>
        <h1
          style={{
            margin: 0,
            fontSize: 26,
            fontWeight: 600,
            letterSpacing: "-0.02em",
            color: "var(--fg)",
          }}
        >
          x402 Bazaar
        </h1>
        <p
          style={{
            margin: "8px 0 0",
            fontSize: 13.5,
            color: "var(--fg-muted)",
            lineHeight: 1.65,
            maxWidth: "68ch",
          }}
        >
          Services your agents can pay for by the call, no accounts or API keys
          needed. Our partners come with a ready-made page. Everything else you
          can drop straight onto a canvas.
        </p>

        {supported.length > 0 && (
          <section style={{ marginTop: 28 }}>
            <SectionHeading
              title="Partners"
              note="Set up and tested by us. Open one and start using it right away."
            />
            {consoles.length > 0 && (
              <div style={CONSOLE_GRID}>
                {consoles.map(([key, resources]) => (
                  <ConsoleCard key={key} consoleKey={key} resources={resources} />
                ))}
              </div>
            )}
            {plainSupported.length > 0 && (
              <div style={{ ...GRID, marginTop: consoles.length > 0 ? 12 : 0 }}>
                {plainSupported.map((r) => (
                  <div key={r.id} className="bz-supported-card">
                    <ResourceCard resource={r} onAdd={setAdding} />
                  </div>
                ))}
              </div>
            )}
          </section>
        )}

        <section style={{ marginTop: 28 }}>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              justifyContent: "space-between",
              gap: 12,
              flexWrap: "wrap",
              marginBottom: 12,
            }}
          >
            <SectionHeading
              title="Everything else"
              note="Public listings. Add one to a canvas and fill in its details yourself."
            />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search…"
              aria-label="Search endpoints"
              style={{
                height: 32,
                padding: "0 10px",
                minWidth: 200,
                border: "1px solid var(--border)",
                background: "var(--bg)",
                borderRadius: "var(--r-2)",
                color: "var(--fg)",
                fontSize: 12.5,
                fontFamily: "var(--font-sans)",
              }}
            />
          </div>

          <div className="bz-list">
            {groupedItems.map(([host, resources]) =>
              resources.length === 1 ? (
                <EndpointRow key={resources[0].id} resource={resources[0]} onAdd={setAdding} />
              ) : (
                <ProviderGroupCard
                  key={host}
                  host={host}
                  resources={resources}
                  expanded={expandedHosts.has(host)}
                  onToggle={() => toggleHost(host)}
                  onAdd={setAdding}
                  partial={!noMore}
                />
              ),
            )}
          </div>

          {error && (
            <p style={{ marginTop: 16, fontSize: 12.5, color: "var(--danger)" }}>
              {error}{" "}
              <button
                type="button"
                onClick={loadMore}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--accent)",
                  cursor: "pointer",
                  fontSize: 12.5,
                  textDecoration: "underline",
                  fontFamily: "var(--font-sans)",
                }}
              >
                Retry
              </button>
            </p>
          )}

          {loading && (
            <p style={{ marginTop: 16, fontSize: 12.5, color: "var(--fg-dim)" }}>
              Loading…
            </p>
          )}

          {!loading && !error && items.length === 0 && activeQuery && (
            <p style={{ marginTop: 16, fontSize: 12.5, color: "var(--fg-dim)" }}>
              Nothing matches “{activeQuery}”.
            </p>
          )}

          {exhausted && items.length > 0 && (
            <p style={{ marginTop: 16, fontSize: 12, color: "var(--fg-dim)" }}>
              That&apos;s all {total}.
            </p>
          )}

          {!exhausted && !loading && !error && items.length > 0 && (
            <button
              type="button"
              onClick={loadMore}
              style={{
                marginTop: 16,
                width: "100%",
                height: 36,
                background: "var(--bg-elev-1)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-2)",
                color: "var(--fg-muted)",
                fontFamily: "var(--font-sans)",
                fontSize: 12.5,
                cursor: "pointer",
              }}
            >
              Load more{total > 0 ? ` · ${items.length} of ${total}` : ""}
            </button>
          )}

          <div ref={sentinelRef} style={{ height: 1 }} />
        </section>
      </div>

      {adding && (
        <AddToWorkflowDialog resource={adding} onClose={() => setAdding(null)} />
      )}
    </div>
  );
}

function SectionHeading({ title, note }: { title: string; note: string }) {
  return (
    <div style={{ marginBottom: 10 }}>
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          color: "var(--fg-dim)",
        }}
      >
        {title}
      </div>
      <div style={{ fontSize: 12, color: "var(--fg-muted)", marginTop: 3 }}>
        {note}
      </div>
    </div>
  );
}
