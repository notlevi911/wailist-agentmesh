"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Topbar } from "@/components/Topbar";
import { Tag, ghostBtnSm } from "@/components/ui";
import { ExternalLink } from "@/components/ExternalLink";
import {
  prism as prismApi,
  formatUsd,
  totalCostMicros,
  PrismRunError,
  type PrismEndpoint,
  type PrismField,
  type PrismRunField,
  type PrismRunResult,
  type PrismSpec,
} from "@/lib/prism";
import { formatFileSize, readFileAsBase64 } from "@/lib/fileEncoding";
import { PrismResult } from "./PrismResult";
import { PrismRepoReview } from "./PrismRepoReview";
import { PAYMENT_RECOVERY, type PaymentRefusalCode } from "@/lib/paymentRefusal";

// A stable identity for "this endpoint has no values yet". Without it the
// `?? {}` fallback allocates a fresh object every render and the memo that
// derives the missing-field list recomputes on every keystroke.
const EMPTY_FIELDS: Record<string, PrismRunField> = {};

// Prism shares the x402 magenta the canvas tool node, the Inspector and the
// Tendril console all use, so a paid endpoint reads as the same kind of thing
// wherever it appears in the app.
const MAGENTA = "var(--type-x402)";
const MAGENTA_DIM = "var(--type-x402-soft)";
const AMBER = "var(--warning)";

// ── Shared chrome, matching TendrilConsolePage's idiom ──────────────────────
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

const textInput: React.CSSProperties = {
  width: "100%",
  height: 34,
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

// A segmented control, used for both the task picker and the tier toggle.
function Segmented<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: Array<{ key: T; label: string; note?: string }>;
  value: T;
  onChange: (key: T) => void;
  ariaLabel: string;
}) {
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      style={{
        display: "flex",
        gap: "var(--s-2)",
        flexWrap: "wrap",
      }}
    >
      {options.map((o) => {
        const active = o.key === value;
        return (
          <button
            key={o.key}
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.key)}
            style={{
              flex: "1 1 180px",
              minWidth: 160,
              textAlign: "left",
              padding: "10px 12px",
              borderRadius: "var(--r-2)",
              border: `1px solid ${active ? MAGENTA : "var(--border-strong)"}`,
              background: active ? MAGENTA_DIM : "transparent",
              color: "var(--fg)",
              cursor: "pointer",
              fontFamily: "var(--font-sans)",
              transition:
                "border-color 0.15s var(--ease), background 0.15s var(--ease)",
            }}
          >
            <div style={{ fontSize: "var(--t-3)", fontWeight: 600 }}>{o.label}</div>
            {o.note && (
              <div
                style={{
                  fontSize: "var(--t-1)",
                  color: active ? "var(--fg-muted)" : "var(--fg-dim)",
                  marginTop: 2,
                  fontFamily: "var(--font-mono)",
                }}
              >
                {o.note}
              </div>
            )}
          </button>
        );
      })}
    </div>
  );
}

// One form control. A file field owns its own picker state so the chosen
// file's name and size stay visible after it is read — a bare <input
// type="file"> loses that the moment the component re-renders.
function FieldControl({
  field,
  value,
  onChange,
  disabled,
}: {
  field: PrismField;
  value: PrismRunField | undefined;
  onChange: (next: PrismRunField | undefined) => void;
  disabled: boolean;
}) {
  const [fileError, setFileError] = useState<string | null>(null);
  const [fileSize, setFileSize] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const pick = async (file: File) => {
    setFileError(null);
    try {
      const encoded = await readFileAsBase64(file);
      setFileSize(encoded.size);
      onChange({
        kind: "file",
        value: encoded.value,
        fileName: encoded.fileName,
        mimeType: encoded.mimeType,
      });
    } catch (e) {
      // Clear any previously-picked file: leaving the old one selected after
      // a failed replacement would send a file the user believes they
      // swapped out, and they would pay for that mistake.
      setFileSize(null);
      onChange(undefined);
      setFileError(
        e instanceof Error ? e.message : `Could not read ${file.name}.`,
      );
    }
  };

  const label = (
    <div style={{ display: "flex", alignItems: "baseline", gap: "var(--s-2)" }}>
      <span style={{ fontSize: "var(--t-2)", fontWeight: 600, color: "var(--fg)" }}>
        {field.label}
      </span>
      {!field.required && (
        <span style={{ fontSize: "var(--t-0)", color: "var(--fg-dim)" }}>optional</span>
      )}
    </div>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--s-2)" }}>
      {label}
      {field.description && (
        <div
          style={{ fontSize: "var(--t-1)", color: "var(--fg-muted)", lineHeight: 1.5 }}
        >
          {field.description}
        </div>
      )}

      {field.kind === "file" ? (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--s-2)" }}>
          <input
            ref={inputRef}
            type="file"
            accept={field.accept}
            disabled={disabled}
            style={{ display: "none" }}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) pick(f);
              // Reset so picking the same filename twice still fires onChange.
              e.target.value = "";
            }}
          />
          {value?.fileName ? (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "var(--s-3)",
                padding: "9px 11px",
                border: `1px solid ${MAGENTA}`,
                background: MAGENTA_DIM,
                borderRadius: "var(--r-1)",
              }}
            >
              <span aria-hidden style={{ color: MAGENTA }}>
                ✦
              </span>
              <span
                style={{
                  flex: 1,
                  minWidth: 0,
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--t-2)",
                  color: "var(--fg)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {value.fileName}
              </span>
              {fileSize !== null && (
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: "var(--t-1)",
                    color: "var(--fg-dim)",
                  }}
                >
                  {formatFileSize(fileSize)}
                </span>
              )}
              <button
                type="button"
                disabled={disabled}
                aria-label={`Remove ${value.fileName}`}
                onClick={() => {
                  setFileSize(null);
                  setFileError(null);
                  onChange(undefined);
                }}
                style={{
                  background: "none",
                  border: "none",
                  color: "var(--fg-dim)",
                  cursor: disabled ? "default" : "pointer",
                  fontSize: "var(--t-3)",
                  padding: 0,
                }}
              >
                ✕
              </button>
            </div>
          ) : (
            <button
              type="button"
              disabled={disabled}
              onClick={() => inputRef.current?.click()}
              style={{
                padding: "14px 12px",
                border: "1px dashed var(--border-strong)",
                borderRadius: "var(--r-1)",
                background: "var(--bg)",
                color: "var(--fg-muted)",
                fontSize: "var(--t-2)",
                fontFamily: "var(--font-sans)",
                cursor: disabled ? "default" : "pointer",
              }}
            >
              {/* The accepted extensions are already stated in the field's
                  own description, in prose. Repeating the raw accept string
                  here reads as machine output. */}
              Choose a file
            </button>
          )}
          {fileError && (
            <div style={{ fontSize: "var(--t-1)", color: "var(--danger)" }}>
              {fileError}
            </div>
          )}
        </div>
      ) : field.kind === "textarea" ? (
        <textarea
          disabled={disabled}
          value={value?.value ?? ""}
          placeholder={field.placeholder}
          onChange={(e) => onChange({ kind: "text", value: e.target.value })}
          rows={4}
          style={{
            ...textInput,
            height: "auto",
            padding: "9px 11px",
            resize: "vertical",
            lineHeight: 1.5,
          }}
        />
      ) : (
        <input
          disabled={disabled}
          value={value?.value ?? ""}
          placeholder={field.placeholder}
          onChange={(e) => onChange({ kind: "text", value: e.target.value })}
          style={textInput}
        />
      )}
    </div>
  );
}

// A dedicated control panel for Prism, opened from the workflow list or the
// Bazaar like any other workflow row — but once inside there is no graph:
// picking a task, filling its form, and paying for one call are a single
// press with no chaining.
//
// Prism has a console rather than a canvas node because its real request body
// (a nested files array carrying base64 file content) cannot be expressed by
// the flat, plain-text discoveredParams a tool402 node renders. See
// backend/internal/bazaar/curated.go.
export function PrismConsolePage() {
  const router = useRouter();
  const [spec, setSpec] = useState<PrismSpec | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [taskKey, setTaskKey] = useState<string | null>(null);
  const [tier, setTier] = useState<string>("accurate");
  // Field values are keyed by TASK, not by endpoint id. The tier toggle
  // presents itself as a depth control over one task, and both tiers of a task
  // take byte-identical fields — so keying by endpoint meant filling in a
  // resume under "Quick", switching to "Thorough", and finding the form blank
  // and the Run button disabled.
  const [values, setValues] = useState<
    Record<string, Record<string, PrismRunField>>
  >({});

  // Code review comes in two shapes: one file, or a whole repository (one
  // paid call per file). Only the code-review task has a repo mode — there is
  // no such thing as screening a repository of resumes.
  const [scope, setScope] = useState<"file" | "repo">("file");

  const [running, setRunning] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const [refusalCode, setRefusalCode] = useState<PaymentRefusalCode | null>(null);
  const recovery = refusalCode ? PAYMENT_RECOVERY[refusalCode] : null;
  const [result, setResult] = useState<PrismRunResult | null>(null);

  useEffect(() => {
    let stale = false;
    prismApi
      .spec()
      .then((s) => {
        if (stale) return;
        setSpec(s);
        setTaskKey((cur) => cur ?? s.tasks[0]?.key ?? null);
      })
      .catch((e: unknown) => {
        if (!stale) {
          setLoadError(
            e instanceof Error
              ? e.message
              : "Could not load Prism's endpoints.",
          );
        }
      });
    return () => {
      stale = true;
    };
  }, []);

  const endpoint: PrismEndpoint | null = useMemo(() => {
    if (!spec || !taskKey) return null;
    return (
      spec.endpoints.find((e) => e.task === taskKey && e.tier === tier) ??
      spec.endpoints.find((e) => e.task === taskKey) ??
      null
    );
  }, [spec, taskKey, tier]);

  // Memoised so the `missing` list below recomputes only when this
  // endpoint's own values change -- see EMPTY_FIELDS.
  const fieldValues = useMemo(
    () => (endpoint ? (values[endpoint.task] ?? EMPTY_FIELDS) : EMPTY_FIELDS),
    [endpoint, values],
  );

  const setField = useCallback(
    (taskKey: string, name: string, next: PrismRunField | undefined) => {
      setValues((cur) => {
        const forTask = { ...(cur[taskKey] ?? {}) };
        if (next === undefined) delete forTask[name];
        else forTask[name] = next;
        return { ...cur, [taskKey]: forTask };
      });
    },
    [],
  );

  // Mirrors the backend's own validation so the button is disabled rather
  // than the user discovering a missing field via a 400. The backend check is
  // the real one — this only saves a round trip.
  const missing = useMemo(() => {
    if (!endpoint) return [];
    return endpoint.fields
      .filter((f) => f.required && !(fieldValues[f.name]?.value ?? "").trim())
      .map((f) => f.label);
  }, [endpoint, fieldValues]);

  const handleRun = async () => {
    if (!endpoint || missing.length > 0 || running) return;
    setRunning(true);
    setRunError(null);
    setRefusalCode(null);
    setResult(null);
    try {
      setResult(await prismApi.run(endpoint.id, fieldValues));
    } catch (e) {
      setRefusalCode(e instanceof PrismRunError ? e.code : null);
      setRunError(
        e instanceof Error ? e.message : "Something went wrong. Try again.",
      );
    } finally {
      setRunning(false);
    }
  };

  const fee = spec?.platformFeeUsdMicros ?? 0;
  const total = endpoint ? totalCostMicros(endpoint, fee) : 0;

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
          {/* ghostBtnSm is inline-flex (so its own icon+label stay aligned),
              and Tag is inline-flex too -- with no block-level element
              between them, the button's marginBottom did nothing and the
              two sat on one crowded line, the Tag's own bullet dot reading
              like a separator glued to "Workflows". A wrapping block gives
              the margin somewhere real to apply. */}
          <div style={{ marginBottom: 18 }}>
            <button
              onClick={() => router.push("/workflows")}
              style={ghostBtnSm}
            >
              ← Workflows
            </button>
          </div>

          <Tag>prism · ai routing</Tag>
          <h1 className="am-console-title">Run an AI task</h1>
          <p
            style={{
              margin: "0 0 14px",
              color: "var(--fg-muted)",
              fontSize: "var(--t-4)",
              maxWidth: 540,
            }}
          >
            Pick a task, fill it in, and pay for that one run. Nothing to set up
            and no subscription.
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
              {/* ── Task ─────────────────────────────────────────────── */}
              <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                <PanelLabel>Task</PanelLabel>
                <div style={{ marginTop: 10 }}>
                  <Segmented
                    ariaLabel="Task"
                    value={taskKey ?? ""}
                    onChange={(k) => {
                      setTaskKey(k);
                      if (k !== "code-review") setScope("file");
                      setResult(null);
                      setRunError(null);
                    }}
                    options={spec.tasks.map((t) => ({
                      key: t.key,
                      label: t.title,
                      note: t.description,
                    }))}
                  />
                </div>
              </Panel>

              {/* ── Tier ─────────────────────────────────────────────── */}
              <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                <PanelLabel>Depth</PanelLabel>
                <div style={{ marginTop: 10 }}>
                  <Segmented
                    ariaLabel="Quality tier"
                    value={tier}
                    onChange={(t) => {
                      setTier(t);
                      setResult(null);
                      setRunError(null);
                    }}
                    options={spec.endpoints
                      .filter((e) => e.task === taskKey)
                      .map((e) => ({
                        key: e.tier,
                        label: e.tier === "fast" ? "Quick" : "Thorough",
                        // In repo mode the run covers N files with ONE fee, so
                        // a single-run total here would contradict the real
                        // total the repo panel works out. Quote per file
                        // instead; the panel below owns the run total.
                        //
                        // For a single file it is the TOTAL, not Prism's share:
                        // comparing tiers on the vendor price alone understates
                        // both by $1.50 and makes the cheaper one look twice as
                        // good as it is.
                        note:
                          scope === "repo"
                            ? `${formatUsd(e.amountMicros)} a file`
                            : `${formatUsd(totalCostMicros(e, fee))} a run`,
                      }))}
                  />
                </div>
                {endpoint && (
                  <p
                    style={{
                      margin: "12px 0 0",
                      fontSize: "var(--t-2)",
                      color: "var(--fg-muted)",
                      lineHeight: 1.55,
                    }}
                  >
                    {endpoint.description}
                  </p>
                )}
                {endpoint?.verified === "sibling" && (
                  <div
                    style={{
                      display: "flex",
                      gap: "var(--s-2)",
                      alignItems: "baseline",
                      marginTop: 10,
                      fontSize: "var(--t-1)",
                      color: "var(--fg-dim)",
                      lineHeight: 1.55,
                    }}
                  >
                    <span aria-hidden style={{ flexShrink: 0 }}>
                      ⓘ
                    </span>
                    <span>
                      Newer than the thorough version and less used so far. If a
                      run fails, you are still charged for it.
                    </span>
                  </div>
                )}
              </Panel>

              {/* ── What to review ───────────────────────────────────── */}
              {taskKey === "code-review" && (
                <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                  <PanelLabel>What to review</PanelLabel>
                  <div style={{ marginTop: 10 }}>
                    <Segmented
                      ariaLabel="What to review"
                      value={scope}
                      onChange={(v) => {
                        setScope(v);
                        setResult(null);
                        setRunError(null);
                      }}
                      options={[
                        {
                          key: "file" as const,
                          label: "One file",
                          note: "paste a link to a single file",
                        },
                        {
                          key: "repo" as const,
                          label: "Whole repo",
                          note: "every source file, one pass each",
                        },
                      ]}
                    />
                  </div>
                </Panel>
              )}

              {taskKey === "code-review" && scope === "repo" && endpoint && (
                <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                  <PrismRepoReview endpoint={endpoint} platformFee={fee} />
                </Panel>
              )}

              {/* ── Form ─────────────────────────────────────────────── */}
              {endpoint && scope === "file" && (
                <Panel style={{ padding: "18px 20px", marginBottom: 16 }}>
                  <PanelLabel>Input</PanelLabel>
                  <div
                    style={{
                      marginTop: 14,
                      display: "flex",
                      flexDirection: "column",
                      gap: "var(--s-5)",
                    }}
                  >
                    {endpoint.fields.map((f) => (
                      <FieldControl
                        key={`${endpoint.task}:${f.name}`}
                        field={f}
                        value={fieldValues[f.name]}
                        disabled={running}
                        onChange={(next) =>
                          setField(endpoint.task, f.name, next)
                        }
                      />
                    ))}
                  </div>

                  {/* One flat row instead of a button with a status line
                      stacked under it: the hint (if any) sits to the left, a
                      rule fills whatever space is left, then the button. */}
                  <div
                    style={{
                      marginTop: 20,
                      paddingTop: 16,
                      borderTop: "1px solid var(--border)",
                      display: "flex",
                      alignItems: "center",
                      gap: "var(--s-4)",
                      // Same escape hatch the pre-flatten layout had: on a
                      // narrow viewport the hint text can't shrink below its
                      // own width, so without wrap it would overflow the
                      // panel instead of dropping to its own line.
                      flexWrap: "wrap",
                    }}
                  >
                    {missing.length > 0 && (
                      <div
                        style={{
                          fontSize: "var(--t-1)",
                          color: "var(--fg-dim)",
                        }}
                      >
                        Add {missing.map((m) => m.toLowerCase()).join(" and ")}{" "}
                        to run this.
                      </div>
                    )}
                    <div
                      aria-hidden
                      style={{
                        flex: 1,
                        minWidth: 24,
                        height: 1,
                        background: "var(--border)",
                      }}
                    />
                    <button
                      type="button"
                      onClick={handleRun}
                      disabled={running || missing.length > 0}
                      style={nameplateButton(running || missing.length > 0)}
                    >
                      {running ? "Working…" : `Run · ${formatUsd(total)}`}
                    </button>
                  </div>
                </Panel>
              )}

              {runError && scope === "file" && (
                <Panel
                  style={{
                    padding: "14px 16px",
                    marginBottom: 16,
                    borderColor: "var(--danger)",
                  }}
                >
                  <PanelLabel>Didn&rsquo;t run</PanelLabel>
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
                    // Whether the money moved is the first thing anyone wants
                    // to know after a failure, and the honest answer here is
                    // "it depends where it broke". Say that rather than imply
                    // either outcome.
                    <div
                      style={{
                        marginTop: 8,
                        fontSize: "var(--t-1)",
                        color: "var(--fg-muted)",
                        lineHeight: 1.55,
                      }}
                    >
                      If you were charged, it shows up under Credits. Worth a
                      look before you run it again.
                    </div>
                  )}
                </Panel>
              )}

              {result && scope === "file" && (
                <Panel style={{ padding: "18px 20px" }}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "baseline",
                      justifyContent: "space-between",
                      gap: "var(--s-4)",
                      flexWrap: "wrap",
                    }}
                  >
                    <PanelLabel>Result</PanelLabel>
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

                  {/* An unsettled call is not a free win — it means the
                      endpoint never asked for payment, so this response did
                      not come from a paid x402 call at all. Saying so beats
                      letting it read as a successful paid result. */}
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
                      Prism answered without asking for payment, so this run was
                      free. That is unusual, so double-check the result before
                      you rely on it.
                    </div>
                  )}

                  <div style={{ marginTop: 12 }}>
                    <PrismResult response={result.response} />
                  </div>

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
                        <div style={{ fontSize: "var(--t-1)", color: "var(--fg-dim)" }}>
                          Paid to Prism{" "}
                          <ExternalLink
                            href={result.explorerURL}
                            style={txLinkStyle}
                          >
                            {result.txId}
                          </ExternalLink>
                        </div>
                      )}
                      {result.platformFeeTxId && (
                        <div style={{ fontSize: "var(--t-1)", color: "var(--fg-dim)" }}>
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
            </>
          )}
        </div>
      </div>
    </div>
  );
}
