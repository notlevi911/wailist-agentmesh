"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { BASE } from "@/lib/api";
import {
  X402_PLATFORM_FEE_USD_MICROS,
  type BazaarResource,
} from "@/lib/bazaar";
import { tendril } from "@/lib/tendril";
import { prism } from "@/lib/prism";
import { can } from "@/lib/readonly";
import { useReadOnly } from "@/hooks/useReadOnly";
import { TENDRIL_DEMO_WORKFLOW, PRISM_DEMO_WORKFLOW } from "@/lib/data";
import { loadTemplateWorkflow } from "@/lib/templateWorkflow";
import type { Workflow } from "@/lib/types";

const MAGENTA = "#E879F9";
const MAGENTA_SOFT = "rgba(232, 121, 249, 0.14)";

// What each console is FOR, in the user's terms rather than the catalog's. A
// console entry's own description is written for an endpoint ("Run a Python
// script on rented compute…"), which is the wrong altitude for a card whose
// button opens a whole workspace.
const CONSOLE_COPY: Record<string, { verb: string; blurb: string }> = {
  tendril: {
    verb: "Rent a machine by the hour",
    blurb:
      "A real Linux box with a terminal, billed by the second. Give it back when you're done and you only pay for the time you used.",
  },
  prism: {
    verb: "Code review and resume screening",
    blurb:
      "Have a file reviewed for bugs and security holes, or score a resume against a role. Pick quick or thorough, and pay for that one run.",
  },
};

// TRY_WORKFLOW_TEMPLATES backs the card's "try a workflow" icon: a real,
// multi-node pipeline that actually calls this partner, as opposed to the
// console's single-endpoint form. Lives in lib/data.ts next to DEMO_WORKFLOW
// -- same shape, same reason (real endpoints and correct billing math, not
// an invented example).
const TRY_WORKFLOW_TEMPLATES: Record<string, Workflow> = {
  tendril: TENDRIL_DEMO_WORKFLOW,
  prism: PRISM_DEMO_WORKFLOW,
};

// Where each console lives. Resolved on click, never on render: both of these
// find-or-create a hidden workflow row server-side, and merely LOOKING at the
// Bazaar must not mint console rows for partners the user has never opened.
const CONSOLE_ROUTES: Record<
  string,
  { create: () => Promise<string>; find: () => Promise<string | null> }
> = {
  tendril: {
    create: () => tendril.console(),
    find: () => tendril.consoleWorkflowIdIfExists(),
  },
  prism: {
    create: () => prism.console(),
    find: () => prism.consoleWorkflowIdIfExists(),
  },
};

// TIER_SUFFIXES are quality tiers, not separate capabilities: "code-review-fast"
// and "code-review-accurate" are one thing you can do, offered at two depths.
// Listing both would make a console look twice as broad as it is.
const TIER_SUFFIXES = ["fast", "accurate", "quick", "thorough"];

// capabilityLabels turns endpoint URLs into plain-language things-you-can-do.
// Order-preserving and deduplicated, so a console lists each capability once in
// the order its endpoints appear.
export function capabilityLabels(urls: string[]): string[] {
  const out: string[] = [];
  for (const raw of urls) {
    let seg: string;
    try {
      const parts = new URL(raw).pathname.split("/").filter(Boolean);
      // A rootless URL (https://host/) has no path segment to name a
      // capability after; skip it rather than fall back to `raw` and render
      // the whole URL string as a chip.
      if (parts.length === 0) continue;
      seg = parts[parts.length - 1];
    } catch {
      // A malformed catalog URL should not take the card down with it.
      continue;
    }
    const words = seg.split(/[-_]/).filter(Boolean);
    if (words.length > 1 && TIER_SUFFIXES.includes(words[words.length - 1].toLowerCase())) {
      words.pop();
    }
    if (words.length === 0) continue;
    const label =
      words.join(" ").charAt(0).toUpperCase() + words.join(" ").slice(1).toLowerCase();
    if (!out.includes(label)) out.push(label);
  }
  return out;
}

// priceLabel collapses a console's endpoints into one figure, ALL-IN.
//
// Prism covers four endpoints at three different prices, so a single number
// would be wrong for three of them — hence a range. More importantly the range
// is the total the user pays, not the vendor's share: adding
// X402_PLATFORM_FEE_USD_MICROS is the difference between this card promising
// "0.1–0.25 USDC" and the console then charging $1.60–$1.75.
//
// A card that quotes the vendor price alone is exactly the failure
// prism.test.ts describes ("a user who reads 0.25 USDC and is billed $1.75 has
// been misled"), and this card is the main way into the console.
export function priceLabel(resources: BazaarResource[]): string {
  const totals = resources
    .map((r) => r.amountMicros + X402_PLATFORM_FEE_USD_MICROS)
    .sort((a, b) => a - b);
  const usd = (micros: number) => `$${(micros / 1e6).toFixed(2)}`;
  // A console card only mounts with a non-empty endpoint group today, but a
  // guard here keeps an empty list from rendering "$NaN a run" if that ever
  // stops holding.
  if (totals.length === 0) return "";
  const low = totals[0];
  const high = totals[totals.length - 1];
  if (low === high) return `${usd(low)} a run`;
  return `${usd(low)}-${usd(high)} a run`;
}

// One partner console: several endpoints behind a single purpose-built page.
//
// Deliberately not a ResourceCard. A catalog entry is a thing you ADD to a
// canvas; a console is a place you GO. Four Prism cards that all open the same
// page would be four buttons for one destination, and would imply four
// separately-configurable nodes that do not exist — Prism's request body can't
// be expressed as a canvas node at all, which is why it has a console.
export function ConsoleCard({
  consoleKey,
  resources,
}: {
  consoleKey: string;
  resources: BazaarResource[];
}) {
  const router = useRouter();
  const readOnly = useReadOnly();
  const [opening, setOpening] = useState(false);
  const [trying, setTrying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const provider = resources[0].provider ?? resources[0].host;
  const capabilities = capabilityLabels(resources.map((r) => r.url));
  const copy = CONSOLE_COPY[consoleKey];
  const route = CONSOLE_ROUTES[consoleKey];
  const template = TRY_WORKFLOW_TEMPLATES[consoleKey];
  const canTryWorkflow = Boolean(template) && can("workflow.create", readOnly);
  // Mock/demo mode (NEXT_PUBLIC_API_URL unset) has no backend to resolve a
  // console workflow id against. Say so on the button rather than routing to
  // a URL that cannot load -- the rest of the page still renders its fixtures
  // usefully, and a dead click would read as the page being broken.
  const available = Boolean(BASE) && Boolean(route);

  const open = async () => {
    if (opening || !available || !route) return;
    setOpening(true);
    setError(null);
    try {
      // A viewer takes the non-creating variant: find-or-create is authoring
      // even though the call is a GET, and a read-only session must not
      // author. If no console exists yet there is simply nowhere to go.
      const id = can("workflow.create", readOnly)
        ? await route.create()
        : await route.find();
      if (!id) {
        setError("Open this from the AgentMesh desktop app first.");
        setOpening(false);
        return;
      }
      router.push(`/workflows/${id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not open this. Try again.");
      setOpening(false);
    }
  };

  // Unlike `open`, this always creates a fresh workflow row (create() then
  // update(), the same two-call pattern the demo-workflow buttons use) --
  // there is no "the one shared try-it" row to find-or-create, since each
  // click is a new copy the user immediately owns and can edit or throw away.
  const tryWorkflow = async () => {
    if (trying || opening || !canTryWorkflow || !available || !template) return;
    setTrying(true);
    setError(null);
    try {
      const id = await loadTemplateWorkflow(template);
      router.push(`/workflows/${id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load this workflow. Try again.");
      setTrying(false);
    }
  };

  return (
    <div
      style={{
        border: "1px solid var(--accent-line)",
        background: "var(--accent-soft)",
        borderRadius: "var(--r-3)",
        padding: 18,
        display: "flex",
        flexDirection: "column",
        gap: 12,
        minWidth: 0,
        height: "100%",
        boxSizing: "border-box",
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 10 }}>
        <span
          aria-hidden
          style={{
            width: 28,
            height: 28,
            borderRadius: "var(--r-2)",
            background: MAGENTA_SOFT,
            color: MAGENTA,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 14,
            flexShrink: 0,
          }}
        >
          ✦
        </span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div
            style={{
              fontSize: 15,
              fontWeight: 600,
              color: "var(--fg)",
              letterSpacing: "-0.01em",
            }}
          >
            {provider}
          </div>
          <div style={{ fontSize: 12, color: "var(--fg-muted)", marginTop: 1 }}>
            {copy?.verb ?? "Open console"}
          </div>
        </div>
        {canTryWorkflow && (
          <button
            type="button"
            onClick={tryWorkflow}
            disabled={trying || opening || !available}
            title={
              available
                ? `Try a workflow with ${provider}`
                : "This preview has no backend connected."
            }
            aria-label={`Try a workflow with ${provider}`}
            style={{
              width: 26,
              height: 26,
              flexShrink: 0,
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              borderRadius: "var(--r-2)",
              border: "1px solid var(--border-strong)",
              background: "var(--bg)",
              color: trying || !available ? "var(--fg-dim)" : MAGENTA,
              cursor: trying || opening || !available ? "default" : "pointer",
              fontSize: 11,
              padding: 0,
            }}
          >
            {trying ? "…" : "▶"}
          </button>
        )}
      </div>

      <p
        style={{
          margin: 0,
          fontSize: 12.5,
          lineHeight: 1.6,
          color: "var(--fg-muted)",
          flex: 1,
          // Keeps the two cards' body text to a comfortable measure even when
          // the grid gives them a wide track.
          maxWidth: "60ch",
        }}
      >
        {copy?.blurb ?? resources[0].description}
      </p>

      {/* What you can actually do in there. Derived from the endpoints rather
          than hand-written, so it cannot drift out of date when a partner adds
          one — but humanised, because a raw path ("code-review-accurate") is a
          route name, not a capability. */}
      {capabilities.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
          {capabilities.map((c) => (
            <span
              key={c}
              style={{
                padding: "2px 8px",
                borderRadius: 999,
                border: "1px solid var(--border-strong)",
                background: "var(--bg)",
                fontSize: 11,
                color: "var(--fg-muted)",
              }}
            >
              {c}
            </span>
          ))}
        </div>
      )}

      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
          flexWrap: "wrap",
          paddingTop: 12,
          borderTop: "1px solid var(--border)",
        }}
      >
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 11.5,
            color: "var(--fg)",
            fontWeight: 600,
          }}
        >
          {priceLabel(resources)}
        </span>
        <button
          type="button"
          onClick={open}
          disabled={opening || !available}
          title={available ? undefined : "This preview has no backend connected."}
          style={{
            height: 32,
            padding: "0 16px",
            border: `1px solid ${opening || !available ? "var(--border-strong)" : "var(--accent)"}`,
            background: opening || !available ? "transparent" : "var(--accent)",
            color: opening || !available ? "var(--fg-dim)" : "var(--accent-fg)",
            borderRadius: "var(--r-2)",
            fontSize: 12,
            fontWeight: 500,
            cursor: opening || !available ? "default" : "pointer",
            fontFamily: "var(--font-sans)",
          }}
        >
          {!available ? "Unavailable in preview" : opening ? "Opening…" : "Open"}
        </button>
      </div>

      {error && (
        <div style={{ fontSize: 11.5, color: "var(--danger)", lineHeight: 1.5 }}>
          {error}
        </div>
      )}
    </div>
  );
}
