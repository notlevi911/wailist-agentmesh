"use client";
import { assetSymbol, formatPrice, type BazaarResource } from "@/lib/bazaar";

// Matches ResourceCard's tool402 accent, so a row still reads as "the same
// kind of thing" as the pinned Supported cards above it.
const MAGENTA = "#E879F9";
const MAGENTA_SOFT = "rgba(232, 121, 249, 0.14)";

// A single catalog entry, as one fixed-height row rather than a card. Every
// row is the same shape regardless of description length (one-line
// ellipsis, not a multi-line clamp) — the previous card grid put
// variable-height cards in CSS grid cells, which has no masonry behavior,
// so descriptions of different lengths produced a visibly broken, ragged
// layout. A uniform row sidesteps that entirely and reads as a real list,
// not a wall of mismatched tiles.
export function EndpointRow({
  resource,
  onAdd,
  indent = false,
}: {
  resource: BazaarResource;
  onAdd: (r: BazaarResource) => void;
  indent?: boolean;
}) {
  const params = resource.params ?? [];
  const paramCount = params.length;
  let displayPath = resource.url;
  try {
    displayPath = new URL(resource.url).pathname;
  } catch {
    // Malformed URL from an unexpected catalog entry — fall back to the raw
    // string rather than crashing this row (and every row after it).
  }

  return (
    <div
      className="bz-row"
      style={{ paddingLeft: indent ? 44 : 16 }}
    >
      <span
        aria-hidden
        className="bz-row__icon"
        style={{ background: MAGENTA_SOFT, color: MAGENTA }}
      >
        ✦
      </span>

      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span className="bz-row__name">{resource.provider ?? resource.host}</span>
          <span className="bz-row__path" title={resource.url}>
            {resource.method} {displayPath}
          </span>
        </div>
        <p className="bz-row__desc">
          {resource.description || "No description published."}
        </p>
      </div>

      <div className="bz-row__meta">
        <span className="bz-row__price">
          {formatPrice(resource.amountMicros)} {assetSymbol(resource.asset)}
        </span>
        <span className="bz-row__price-unit">/ call</span>
        {resource.testnet && <span className="bz-pill">testnet</span>}
        {resource.settleCount > 0 && (
          <span className="bz-row__stat">{resource.settleCount} settles</span>
        )}
        {paramCount > 0 && (
          <span className="bz-row__stat">
            {paramCount} field{paramCount === 1 ? "" : "s"}
          </span>
        )}
      </div>

      <button type="button" className="bz-row__add" onClick={() => onAdd(resource)}>
        Add
      </button>
    </div>
  );
}
