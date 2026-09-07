"use client";
import { Pill } from "@/components/ui";
import { assetSymbol, formatPrice, type BazaarResource } from "@/lib/bazaar";

// The tool402 accent, matching the canvas node and Inspector so an endpoint
// looks like the same thing everywhere it appears (Inspector.tsx:276-280).
const MAGENTA = "#E879F9";
const MAGENTA_SOFT = "rgba(232, 121, 249, 0.14)";

// One catalog entry. A supported entry is visually distinct because the badge
// is the whole promise of the page: it means we have hand-authored this
// endpoint's fields and verified a real settlement through it.
export function ResourceCard({
  resource,
  onAdd,
}: {
  resource: BazaarResource;
  onAdd: (r: BazaarResource) => void;
}) {
  // (resource.params ?? []): defense in depth. The backend guarantees a
  // non-nil array, but a mirror of an external catalog should never trust
  // its own past output blindly.
  const params = resource.params ?? [];
  const paramCount = params.length;
  let displayPath = resource.url;
  try {
    displayPath = new URL(resource.url).pathname;
  } catch {
    // Malformed URL from an unexpected catalog entry — fall back to showing
    // the raw string rather than crashing this card (and every card
    // rendered alongside it in the same list).
  }
  return (
    <div
      style={{
        border: `1px solid ${resource.supported ? "var(--accent-line)" : "var(--border)"}`,
        background: resource.supported ? "var(--accent-soft)" : "var(--bg-elev-1)",
        borderRadius: "var(--r-2)",
        padding: 14,
        display: "flex",
        flexDirection: "column",
        gap: 10,
        minWidth: 0,
        // Fills the grid cell the wrapper stretches to, so cards in a row
        // end at the same baseline instead of each stopping at its own
        // content height.
        height: "100%",
        boxSizing: "border-box",
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
        <span
          aria-hidden
          style={{
            width: 26,
            height: 26,
            borderRadius: "var(--r-2)",
            background: MAGENTA_SOFT,
            color: MAGENTA,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 13,
            flexShrink: 0,
          }}
        >
          ✦
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div
            style={{
              fontSize: 13,
              fontWeight: 600,
              color: "var(--fg)",
              // Wraps instead of truncating. overflowWrap handles the long
              // unbroken host names that have no space to break at.
              overflowWrap: "anywhere",
            }}
          >
            {resource.provider ?? resource.host}
          </div>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10.5,
              color: "var(--fg-dim)",
              overflowWrap: "anywhere",
            }}
            title={resource.url}
          >
            {resource.method} {displayPath}
          </div>
        </div>
        {resource.supported && <Pill tone="warm">Supported</Pill>}
      </div>

      <p
        style={{
          margin: 0,
          fontSize: 12,
          lineHeight: 1.55,
          color: "var(--fg-muted)",
          // Grows into whatever height the tallest card in the row sets, so
          // the price/meta footer and Add button line up across the row.
          flex: 1,
          overflowWrap: "anywhere",
        }}
      >
        {resource.description || "No description published."}
      </p>

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
          fontSize: 11,
          color: "var(--fg-dim)",
          fontFamily: "var(--font-mono)",
        }}
      >
        <span style={{ color: "var(--fg)", fontWeight: 600 }}>
          {formatPrice(resource.amountMicros)} {assetSymbol(resource.asset)}
        </span>
        <span>/ call</span>
        {resource.testnet && <Pill>testnet</Pill>}
        {resource.settleCount > 0 && <span>· {resource.settleCount} settles</span>}
        {paramCount > 0 && (
          <span>
            · {paramCount} field{paramCount === 1 ? "" : "s"}
          </span>
        )}
      </div>

      {/* An unsupported endpoint is usable, but nothing about its input shape
          is guaranteed — say so rather than letting a blank node imply it is
          ready to run. */}
      {!resource.supported && (
        <div style={{ fontSize: 11, color: "var(--fg-dim)", lineHeight: 1.5 }}>
          Community listing — you&apos;ll configure its fields yourself after adding.
        </div>
      )}

      {/* A supported entry without curated params must not imply this card is
          pre-configured — only endorsement is guaranteed, not a filled-in
          form. Nothing renders here today: every curated entry now has a
          console and is drawn by ConsoleCard instead. This is the fallback for
          a future partner that has neither params nor a console. */}
      {resource.supported && paramCount === 0 && (
        <div style={{ fontSize: 11, color: "var(--fg-dim)", lineHeight: 1.5 }}>
          Officially supported — this endpoint takes no input, or its fields
          aren&apos;t set up yet. Configure via Discover after adding.
        </div>
      )}

      <button
        type="button"
        onClick={() => onAdd(resource)}
        style={{
          height: 32,
          border: `1px solid ${resource.supported ? "var(--accent)" : "var(--border-strong)"}`,
          background: resource.supported ? "var(--accent)" : "transparent",
          color: resource.supported ? "var(--accent-fg)" : "var(--fg)",
          borderRadius: "var(--r-2)",
          fontSize: 12,
          fontWeight: 500,
          cursor: "pointer",
          fontFamily: "var(--font-sans)",
        }}
      >
        Add to workflow
      </button>
    </div>
  );
}
