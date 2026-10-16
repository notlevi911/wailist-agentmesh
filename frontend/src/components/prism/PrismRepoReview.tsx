"use client";
import { useCallback, useMemo, useState } from "react";
import {
  formatUsd,
  prism as prismApi,
  repoRunCost,
  PrismRunError,
  type PrismEndpoint,
  type PrismRepoListing,
  type PrismRepoReviewResult,
} from "@/lib/prism";
import { PrismResult } from "./PrismResult";

// Reviewing a whole repository.
//
// One paid call per file, so the flow is deliberately two-step: list first
// (free), then review only what the user ticked. They see the file list and the
// exact total before any money moves — pasting a link must never be the same
// gesture as agreeing to a bill.

const MAGENTA = "var(--type-x402)";
const MAGENTA_DIM = "var(--type-x402-soft)";
const GREEN = "var(--success)";
const AMBER = "var(--warning)";

function Label({ children }: { children: React.ReactNode }) {
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

const inputStyle: React.CSSProperties = {
  flex: 1,
  minWidth: 0,
  height: 34,
  padding: "0 11px",
  background: "var(--bg)",
  border: "1px solid var(--border-strong)",
  borderRadius: "var(--r-1)",
  color: "var(--fg)",
  fontSize: "var(--t-3)",
  fontFamily: "var(--font-mono)",
  outline: "none",
};

function actionButton(disabled: boolean): React.CSSProperties {
  return {
    height: 34,
    padding: "0 16px",
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

function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// A large repository skips tens of thousands of paths, and mounting a DOM node
// per path freezes the tab when the disclosure is opened. The list is
// reassurance that nothing interesting was dropped, not a manifest, so a
// readable sample plus a count does the same job.
const MAX_SKIPPED_SHOWN = 200;

export function PrismRepoReview({
  endpoint,
  platformFee,
}: {
  endpoint: PrismEndpoint;
  platformFee: number;
}) {
  const [repo, setRepo] = useState("");
  const [listing, setListing] = useState<PrismRepoListing | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [listing_busy, setListingBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [billingBlocked, setBillingBlocked] = useState(false);

  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<PrismRepoReviewResult | null>(null);
  const [openFile, setOpenFile] = useState<string | null>(null);

  const reviewable = useMemo(
    () => (listing?.files ?? []).filter((f) => !f.skip),
    [listing],
  );
  const skipped = useMemo(
    () => (listing?.files ?? []).filter((f) => f.skip),
    [listing],
  );

  const total = repoRunCost(selected.size, endpoint.amountMicros, platformFee);
  const overCap = listing ? selected.size > listing.maxFiles : false;

  const loadFiles = useCallback(async () => {
    if (listing_busy) return;
    setListingBusy(true);
    setError(null);
    setBillingBlocked(false);
    setListing(null);
    setResult(null);
    try {
      const l = await prismApi.repoFiles(repo);
      setListing(l);
      // Everything reviewable starts ticked — but capped, so a huge repo does
      // not arrive with a four-figure total already selected.
      const initial = l.files.filter((f) => !f.skip).slice(0, l.maxFiles);
      setSelected(new Set(initial.map((f) => f.path)));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not read that repository.");
    } finally {
      setListingBusy(false);
    }
  }, [repo, listing_busy]);

  const toggle = (path: string) =>
    setSelected((cur) => {
      const next = new Set(cur);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const runReview = async () => {
    if (!listing || running || selected.size === 0 || overCap) return;
    setRunning(true);
    setError(null);
    setBillingBlocked(false);
    setResult(null);
    try {
      setResult(
        await prismApi.repoReview(
          `${listing.owner}/${listing.name}`,
          listing.ref,
          endpoint.tier,
          Array.from(selected),
        ),
      );
    } catch (e) {
      // A 402 is refused before anything is paid, so it gets a top-up button
      // rather than a bare error — the same treatment the single-file panel
      // gives the identical condition.
      setBillingBlocked(e instanceof PrismRunError && e.status === 402);
      setError(e instanceof Error ? e.message : "The review failed.");
    } finally {
      setRunning(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--s-4)" }}>
      <div>
        <Label>Repository</Label>
        <div style={{ display: "flex", gap: "var(--s-3)", marginTop: 8, flexWrap: "wrap" }}>
          <input
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") loadFiles();
            }}
            placeholder="github.com/owner/repo"
            style={inputStyle}
            disabled={running}
          />
          <button
            type="button"
            onClick={loadFiles}
            disabled={listing_busy || running || !repo.trim()}
            style={actionButton(listing_busy || running || !repo.trim())}
          >
            {listing_busy ? "Reading…" : "Find files"}
          </button>
        </div>
        <div
          style={{
            fontSize: "var(--t-1)",
            color: "var(--fg-muted)",
            marginTop: 6,
            lineHeight: 1.5,
          }}
        >
          Public GitHub repositories only: Prism opens each file itself, so it
          can&rsquo;t sign in. Listing is free; you pick what to review before
          anything is charged.
        </div>
      </div>

      {error && (
        <div>
          <div style={{ fontSize: "var(--t-2)", color: "var(--danger)", lineHeight: 1.55 }}>
            {error}
          </div>
          {/* A 402 is refused before any file is reviewed, so the honest note
              is "nothing moved" plus the way to fix it — the same treatment
              the single-file panel gives the identical condition. */}
          {billingBlocked && (
            <div style={{ marginTop: 8 }}>
              <div
                style={{
                  fontSize: "var(--t-1)",
                  color: "var(--fg-muted)",
                  lineHeight: 1.55,
                }}
              >
                You were not charged. Add credit and run it again.
              </div>
              <a
                href="/billing"
                style={{
                  display: "inline-block",
                  marginTop: 8,
                  fontSize: "var(--t-1)",
                  color: "var(--accent)",
                  textDecoration: "underline",
                }}
              >
                Add credits
              </a>
            </div>
          )}
        </div>
      )}

      {listing && (
        <>
          <div>
            <div
              style={{
                display: "flex",
                alignItems: "baseline",
                justifyContent: "space-between",
                gap: "var(--s-3)",
                flexWrap: "wrap",
              }}
            >
              <Label>
                {listing.owner}/{listing.name} · {listing.ref}
              </Label>
              <div style={{ fontSize: "var(--t-1)", color: "var(--fg-dim)" }}>
                {reviewable.length} of {listing.files.length} files can be reviewed
              </div>
            </div>

            {reviewable.length === 0 && (
              <div
                style={{
                  marginTop: 10,
                  fontSize: "var(--t-2)",
                  color: "var(--fg-muted)",
                  lineHeight: 1.55,
                }}
              >
                No supported source files found. Try a repository containing
                source code.
              </div>
            )}

            {reviewable.length > 0 && (
              <div
                style={{
                  marginTop: 10,
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-2)",
                  background: "var(--bg)",
                  maxHeight: 320,
                  overflow: "auto",
                }}
              >
                {reviewable.map((f) => {
                  const on = selected.has(f.path);
                  return (
                    <label
                      key={f.path}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: "var(--s-3)",
                        padding: "7px 12px",
                        borderBottom: "1px solid var(--border)",
                        cursor: running ? "default" : "pointer",
                        background: on ? MAGENTA_DIM : "transparent",
                      }}
                    >
                      <input
                        type="checkbox"
                        checked={on}
                        disabled={running}
                        onChange={() => toggle(f.path)}
                        style={{ accentColor: MAGENTA }}
                      />
                      <span
                        style={{
                          flex: 1,
                          minWidth: 0,
                          fontFamily: "var(--font-mono)",
                          fontSize: "var(--t-1)",
                          color: on ? "var(--fg)" : "var(--fg-muted)",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {f.path}
                      </span>
                      <span
                        style={{
                          fontFamily: "var(--font-mono)",
                          fontSize: "var(--t-0)",
                          color: "var(--fg-dim)",
                          flexShrink: 0,
                        }}
                      >
                        {fileSize(f.size)}
                      </span>
                    </label>
                  );
                })}
              </div>
            )}

            {/* Skipped files are listed, not hidden: otherwise a user wonders
                why their file is missing and has no way to find out. */}
            {skipped.length > 0 && (
              <details style={{ marginTop: 8 }}>
                <summary
                  style={{
                    fontSize: "var(--t-1)",
                    color: "var(--fg-dim)",
                    cursor: "pointer",
                  }}
                >
                  {skipped.length} left out
                </summary>
                <div
                  style={{
                    marginTop: 6,
                    maxHeight: 160,
                    overflow: "auto",
                    fontSize: "var(--t-1)",
                    fontFamily: "var(--font-mono)",
                    color: "var(--fg-dim)",
                    lineHeight: 1.7,
                  }}
                >
                  {skipped.slice(0, MAX_SKIPPED_SHOWN).map((f) => (
                    <div key={f.path}>
                      {f.path} - {f.skip}
                    </div>
                  ))}
                  {skipped.length > MAX_SKIPPED_SHOWN && (
                    <div style={{ marginTop: 6, color: "var(--fg-muted)" }}>
                      …and {skipped.length - MAX_SKIPPED_SHOWN} more. These are
                      lockfiles, build output, and binaries. Skipped files are not
                      charged.
                    </div>
                  )}
                </div>
              </details>
            )}
          </div>

          {reviewable.length > 0 && (
            <div
              style={{
                paddingTop: 14,
                borderTop: "1px solid var(--border)",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "var(--s-4)",
                flexWrap: "wrap",
              }}
            >
              <div
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--t-1)",
                  color: "var(--fg-dim)",
                  lineHeight: 1.7,
                }}
              >
                <div style={{ color: "var(--fg)", fontWeight: 600 }}>
                  {formatUsd(total)} for {selected.size} file
                  {selected.size === 1 ? "" : "s"}
                </div>
                <div>
                  {selected.size} × {formatUsd(endpoint.amountMicros)} to Prism ·{" "}
                  {formatUsd(platformFee)} AgentMesh fee, once
                </div>
              </div>
              <button
                type="button"
                onClick={runReview}
                disabled={running || selected.size === 0 || overCap}
                style={actionButton(running || selected.size === 0 || overCap)}
              >
                {running ? "Reviewing…" : `Review · ${formatUsd(total)}`}
              </button>
            </div>
          )}

          {overCap && (
            <div style={{ fontSize: "var(--t-1)", color: AMBER, textAlign: "right" }}>
              That&rsquo;s more than {listing.maxFiles} files. Untick some to
              carry on.
            </div>
          )}

          {running && (
            <div
              style={{
                fontSize: "var(--t-2)",
                color: "var(--fg-muted)",
                lineHeight: 1.55,
              }}
            >
              Reviewing {selected.size} files, one at a time. Leave the page open
              until the review finishes.
            </div>
          )}
        </>
      )}

      {result && (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--s-3)" }}>
          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              justifyContent: "space-between",
              gap: "var(--s-3)",
              flexWrap: "wrap",
              paddingTop: 12,
              borderTop: "1px solid var(--border)",
            }}
          >
            <Label>
              {result.repo} · {result.results.length} files
            </Label>
            <span
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: "var(--t-1)",
                color: "var(--fg-muted)",
              }}
            >
              {formatUsd(result.totalUsdMicros)} charged
            </span>
          </div>

          {result.results.map((f) => {
            const open = openFile === f.path;
            return (
              <div
                key={f.path}
                style={{
                  border: "1px solid var(--border)",
                  borderRadius: "var(--r-2)",
                  background: "var(--bg)",
                  overflow: "hidden",
                }}
              >
                <button
                  type="button"
                  onClick={() => setOpenFile(open ? null : f.path)}
                  style={{
                    width: "100%",
                    display: "flex",
                    alignItems: "center",
                    gap: "var(--s-3)",
                    padding: "10px 12px",
                    background: "transparent",
                    border: "none",
                    cursor: "pointer",
                    textAlign: "left",
                    fontFamily: "var(--font-sans)",
                  }}
                >
                  <span
                    aria-hidden
                    style={{ color: f.error ? AMBER : GREEN, fontSize: "var(--t-1)" }}
                  >
                    {f.error ? "!" : "✓"}
                  </span>
                  <span
                    style={{
                      flex: 1,
                      minWidth: 0,
                      fontFamily: "var(--font-mono)",
                      fontSize: "var(--t-1)",
                      color: "var(--fg)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {f.path}
                  </span>
                  <span style={{ fontSize: "var(--t-0)", color: "var(--fg-dim)" }}>
                    {open ? "hide" : "view"}
                  </span>
                </button>
                {open && (
                  <div style={{ padding: "0 12px 12px" }}>
                    {f.error ? (
                      <div
                        style={{
                          fontSize: "var(--t-2)",
                          color: "var(--danger)",
                          lineHeight: 1.55,
                        }}
                      >
                        {f.error}
                      </div>
                    ) : (
                      <PrismResult response={f.response} />
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
