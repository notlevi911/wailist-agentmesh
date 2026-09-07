"use client";
import { useMemo, useState } from "react";

// Rendering Prism's answer.
//
// Prism does not document a response schema, and the four endpoints do not
// share one — resume-screen returns a `candidates` array, code-review returns
// something else we have not seen yet. So this deliberately does NOT assume a
// shape: it recognises the shapes we have actually observed, and falls back to
// a readable structured view for everything else.
//
// The fallback is the important half. A shape we have never seen must still
// render usefully, because the user has already paid for it by the time it
// arrives — dumping raw JSON at them is the thing this component exists to
// stop, and swapping one unreadable dump for a prettier unreadable dump would
// miss the point.

const MAGENTA = "#E879F9";
const GREEN = "#34D399";
const AMBER = "#FFB547";

// Fields the engine merges into the response for its own bookkeeping, plus
// Prism's echo of what it charged. All of them are already shown in the
// settlement row above the result, so repeating them inside the body is noise.
const PAYMENT_NOISE = new Set([
  "amount",
  "txId",
  "txid",
  "explorerURL",
  "explorerUrl",
  "platformFeeTxId",
  "platformFeeExplorerURL",
  "paymentResponse",
]);

function isRecord(v: unknown): v is Record<string, unknown> {
  return !!v && typeof v === "object" && !Array.isArray(v);
}

// Turns "match_score" / "primaryDomain" into "Match score" / "Primary domain".
// Prism mixes snake_case and camelCase across fields, and raw keys are the
// single biggest reason a JSON dump reads as machine output.
export function humanizeKey(key: string): string {
  const spaced = key
    .replace(/[_-]+/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .trim();
  if (!spaced) return key;
  return spaced.charAt(0).toUpperCase() + spaced.slice(1).toLowerCase();
}

// A 0–1 score renders as a percentage; anything else is left alone. Prism
// returns match_score as 0.88, which reads as a rounding artefact rather than
// a rating unless it is converted.
export function formatScore(n: number): string {
  if (n >= 0 && n <= 1) return `${Math.round(n * 100)}%`;
  return String(n);
}

function scoreColor(pct: number): string {
  if (pct >= 0.75) return GREEN;
  if (pct >= 0.5) return AMBER;
  return "var(--fg-dim)";
}

function Chip({ children, tone }: { children: React.ReactNode; tone?: string }) {
  return (
    <span
      style={{
        padding: "2px 8px",
        borderRadius: 999,
        border: `1px solid ${tone ?? "var(--border-strong)"}`,
        background: tone ? "rgba(232,121,249,0.08)" : "var(--bg)",
        color: tone ?? "var(--fg-muted)",
        fontSize: 11,
        fontFamily: "var(--font-sans)",
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </span>
  );
}

function Label({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: 9.5,
        textTransform: "uppercase",
        letterSpacing: "0.09em",
        color: "var(--fg-dim)",
        marginBottom: 5,
      }}
    >
      {children}
    </div>
  );
}

// ── A ranked candidate (resume-screen) ──────────────────────────────────────
// Every field is optional: this renders whatever Prism actually sent and skips
// what it did not, rather than showing empty rows for a shape that shifted.
function CandidateCard({ c, rank }: { c: Record<string, unknown>; rank: number }) {
  const name = typeof c.name === "string" ? c.name : `Candidate ${rank}`;
  const role = typeof c.role === "string" ? c.role : null;
  const domain = typeof c.primary_domain === "string" ? c.primary_domain : null;
  const score = typeof c.match_score === "number" ? c.match_score : null;
  const reason = typeof c.reason === "string" ? c.reason : null;
  const skills = Array.isArray(c.key_skills)
    ? c.key_skills.filter((s): s is string => typeof s === "string")
    : [];
  const altRoles = Array.isArray(c.alternative_roles)
    ? c.alternative_roles.filter((s): s is string => typeof s === "string")
    : [];

  // Anything Prism sent that this card does not have a place for. Shown rather
  // than dropped — a field we have not seen before is exactly the thing the
  // user would otherwise have to open the raw JSON to find.
  const known = new Set([
    "name",
    "role",
    "primary_domain",
    "match_score",
    "reason",
    "key_skills",
    "alternative_roles",
  ]);
  const extra = Object.entries(c).filter(([k]) => !known.has(k) && !PAYMENT_NOISE.has(k));

  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: "var(--r-2)",
        background: "var(--bg)",
        padding: 16,
        display: "flex",
        flexDirection: "column",
        gap: 12,
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 12 }}>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 14.5, fontWeight: 600, color: "var(--fg)" }}>{name}</div>
          {(role || domain) && (
            <div style={{ fontSize: 12, color: "var(--fg-muted)", marginTop: 2 }}>
              {[role, domain].filter(Boolean).join(" · ")}
            </div>
          )}
        </div>
        {score !== null && (
          <div style={{ textAlign: "right", flexShrink: 0 }}>
            <div
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 22,
                fontWeight: 600,
                color: scoreColor(score),
                lineHeight: 1.1,
              }}
            >
              {formatScore(score)}
            </div>
            <div style={{ fontSize: 10, color: "var(--fg-dim)" }}>match</div>
          </div>
        )}
      </div>

      {score !== null && score >= 0 && score <= 1 && (
        <div
          role="img"
          aria-label={`Match score ${formatScore(score)}`}
          style={{
            height: 4,
            borderRadius: 999,
            background: "var(--bg-elev-3)",
            overflow: "hidden",
          }}
        >
          <div
            style={{
              width: `${Math.round(score * 100)}%`,
              height: "100%",
              background: scoreColor(score),
            }}
          />
        </div>
      )}

      {skills.length > 0 && (
        <div>
          <Label>Key skills</Label>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
            {skills.map((s) => (
              <Chip key={s} tone={MAGENTA}>
                {s}
              </Chip>
            ))}
          </div>
        </div>
      )}

      {altRoles.length > 0 && (
        <div>
          <Label>Also suits</Label>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
            {altRoles.map((s) => (
              <Chip key={s}>{s}</Chip>
            ))}
          </div>
        </div>
      )}

      {reason && (
        <div>
          <Label>Why</Label>
          <p
            style={{
              margin: 0,
              fontSize: 12.5,
              lineHeight: 1.6,
              color: "var(--fg-muted)",
            }}
          >
            {reason}
          </p>
        </div>
      )}

      {extra.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {extra.map(([k, v]) => (
            <FieldRow key={k} name={k} value={v} />
          ))}
        </div>
      )}
    </div>
  );
}

// ── Generic structured rendering ────────────────────────────────────────────
// One key/value pair, laid out by what the value actually is.
function FieldRow({ name, value }: { name: string; value: unknown }) {
  const label = humanizeKey(name);

  if (Array.isArray(value)) {
    const scalars = value.filter(
      (v) => typeof v === "string" || typeof v === "number" || typeof v === "boolean",
    );
    // An array of plain values reads far better as chips than as a bulleted
    // list of quoted strings.
    if (scalars.length === value.length && value.length > 0) {
      return (
        <div>
          <Label>{label}</Label>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
            {scalars.map((v, i) => (
              <Chip key={`${String(v)}-${i}`}>{String(v)}</Chip>
            ))}
          </div>
        </div>
      );
    }
    return (
      <div>
        <Label>
          {label} · {value.length}
        </Label>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {value.map((v, i) => (
            <ValueBlock key={i} value={v} />
          ))}
        </div>
      </div>
    );
  }

  if (isRecord(value)) {
    return (
      <div>
        <Label>{label}</Label>
        <ValueBlock value={value} />
      </div>
    );
  }

  if (typeof value === "string" && value.length > 90) {
    return (
      <div>
        <Label>{label}</Label>
        <p style={{ margin: 0, fontSize: 12.5, lineHeight: 1.6, color: "var(--fg-muted)" }}>
          {value}
        </p>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", gap: 10, alignItems: "baseline", flexWrap: "wrap" }}>
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--fg-dim)",
          minWidth: 130,
        }}
      >
        {label}
      </span>
      <span style={{ fontSize: 12.5, color: "var(--fg)", overflowWrap: "anywhere" }}>
        {typeof value === "number" && value >= 0 && value <= 1 && !Number.isInteger(value)
          ? formatScore(value)
          : String(value)}
      </span>
    </div>
  );
}

function ValueBlock({ value }: { value: unknown }) {
  if (isRecord(value)) {
    const entries = Object.entries(value).filter(([k]) => !PAYMENT_NOISE.has(k));
    return (
      <div
        style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--r-2)",
          background: "var(--bg)",
          padding: 12,
          display: "flex",
          flexDirection: "column",
          gap: 8,
        }}
      >
        {entries.map(([k, v]) => (
          <FieldRow key={k} name={k} value={v} />
        ))}
      </div>
    );
  }
  return (
    <div style={{ fontSize: 12.5, color: "var(--fg-muted)", lineHeight: 1.6 }}>
      {String(value)}
    </div>
  );
}

function RawJSON({ value }: { value: unknown }) {
  const text = useMemo(() => {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }, [value]);
  return (
    <pre
      style={{
        margin: 0,
        padding: 14,
        background: "var(--bg)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-1)",
        fontFamily: "var(--font-mono)",
        fontSize: 11.5,
        lineHeight: 1.6,
        color: "var(--fg-muted)",
        whiteSpace: "pre-wrap",
        wordBreak: "break-word",
        maxHeight: 460,
        overflow: "auto",
      }}
    >
      {text}
    </pre>
  );
}

export function PrismResult({ response }: { response: unknown }) {
  const [showRaw, setShowRaw] = useState(false);

  const body = isRecord(response) ? response : null;
  const topArray = Array.isArray(response) ? response : null;
  // A bare top-level array is a real shape too: code-review's response schema
  // is undocumented and unseen, and if it comes back as an array this view
  // still has to render it rather than fall through to a raw dump — the whole
  // reason this component exists. Its items go through the same candidate /
  // ValueBlock path the wrapped `candidates` array uses.
  const candidates =
    (body && Array.isArray(body.candidates) ? body.candidates : null) ?? topArray;
  const rest = body
    ? Object.entries(body).filter(([k]) => k !== "candidates" && !PAYMENT_NOISE.has(k))
    : [];

  // Only a scalar (a plain string or number) has no structure worth imposing
  // one on; a record or an array does.
  const structured = body !== null || topArray !== null;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      {structured && (
        <div style={{ display: "flex", justifyContent: "flex-end" }}>
          <button
            type="button"
            onClick={() => setShowRaw((v) => !v)}
            style={{
              background: "none",
              border: "none",
              padding: 0,
              color: "var(--fg-dim)",
              fontFamily: "var(--font-mono)",
              fontSize: 10.5,
              cursor: "pointer",
              textDecoration: "underline",
            }}
          >
            {showRaw ? "Show summary" : "Show full response"}
          </button>
        </div>
      )}

      {!structured || showRaw ? (
        <RawJSON value={response} />
      ) : (
        <>
          {candidates && candidates.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
              {candidates.map((c, i) =>
                isRecord(c) ? (
                  <CandidateCard key={i} c={c} rank={i + 1} />
                ) : (
                  <ValueBlock key={i} value={c} />
                ),
              )}
            </div>
          )}

          {rest.length > 0 && (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: 10,
                border: candidates ? "1px solid var(--border)" : "none",
                borderRadius: candidates ? "var(--r-2)" : 0,
                padding: candidates ? 14 : 0,
                background: candidates ? "var(--bg)" : "transparent",
              }}
            >
              {rest.map(([k, v]) => (
                <FieldRow key={k} name={k} value={v} />
              ))}
            </div>
          )}

          {/* Prism answered, but with nothing this view can show — say so
              rather than rendering an empty panel that looks like a bug.
              `?.length` not `!candidates`: an empty array is truthy, so
              {"candidates": []} skipped both the list above and this fallback
              and rendered the exact blank panel this exists to prevent. */}
          {!candidates?.length && rest.length === 0 && (
            <div style={{ fontSize: 12.5, color: "var(--fg-dim)" }}>
              Prism sent nothing back. Open the full response to see exactly
              what arrived.
            </div>
          )}
        </>
      )}
    </div>
  );
}
