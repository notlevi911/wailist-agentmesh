"use client";
import { assetSymbol, formatPrice, type BazaarResource } from "@/lib/bazaar";
import { EndpointRow } from "./EndpointRow";

const MAGENTA = "#E879F9";
const MAGENTA_SOFT = "rgba(232, 121, 249, 0.14)";

// Endpoints sharing a host collapse into one of these — one publisher can be
// 70%+ of the raw catalog, and a flat list of all of it at once buries
// everything else. Expands into a nested list of EndpointRows with a smooth
// height animation (CSS grid-template-rows 0fr->1fr, no JS measurement
// needed) rather than an instant show/hide, since the reveal is the one
// interaction this component exists to make pleasant.
export function ProviderGroupCard({
  host,
  resources,
  expanded,
  onToggle,
  onAdd,
  partial = false,
}: {
  host: string;
  resources: BazaarResource[];
  expanded: boolean;
  onToggle: () => void;
  onAdd: (r: BazaarResource) => void;
  // True while the paged feed backing `resources` hasn't finished loading —
  // this host's entries can be spread across many pages (one host can be
  // over 70% of the raw catalog), so the count and cheapest price below are
  // provisional and may grow/shrink as more pages scroll in.
  partial?: boolean;
}) {
  // BazaarPage only ever mounts this for a host with 2+ entries today, but
  // nothing enforces that invariant here -- reduce with an empty array and
  // an initial value of `resources[0]` (undefined) skips the callback
  // entirely and returns undefined, which would crash on `cheapest.amountMicros`
  // below instead of degrading gracefully.
  if (resources.length === 0) return null;

  const label = resources[0].provider ?? host;
  // The min amountMicros alone isn't safe to label "$X": a host can mix
  // ALGO-, USDC-, and ASA-priced endpoints, and comparing raw amounts across
  // assets with no price feed is meaningless anyway. Tracking which
  // resource actually achieved the minimum, and labeling it with THAT
  // resource's own asset, at least never shows a currency the price doesn't
  // apply to.
  const cheapest = resources.reduce((min, r) =>
    r.amountMicros < min.amountMicros ? r : min,
  resources[0]);

  return (
    <div>
      <button
        type="button"
        className="bz-row bz-row--group"
        aria-expanded={expanded}
        onClick={onToggle}
        style={{ width: "100%" }}
      >
        <span
          aria-hidden
          className="bz-row__icon bz-row__chevron"
          data-open={expanded}
          style={{ background: MAGENTA_SOFT, color: MAGENTA }}
        >
          ▸
        </span>
        <span style={{ minWidth: 0, flex: 1, display: "block" }}>
          <span className="bz-row__name">{label}</span>
        </span>
        <span
          className="bz-row__meta"
          title={
            partial
              ? "Based on results loaded so far — may change as you scroll"
              : undefined
          }
        >
          <span className="bz-row__stat">
            {resources.length}
            {partial ? "+" : ""} endpoint{resources.length === 1 ? "" : "s"}
          </span>
          <span className="bz-row__stat">
            from {formatPrice(cheapest.amountMicros)} {assetSymbol(cheapest.asset)}
            {partial ? " so far" : ""}
          </span>
        </span>
      </button>

      <div className="bz-group-body" data-open={expanded} inert={!expanded}>
        <div className="bz-group-body__inner">
          {resources.map((r) => (
            <EndpointRow key={r.id} resource={r} onAdd={onAdd} indent />
          ))}
        </div>
      </div>
    </div>
  );
}
