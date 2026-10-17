"use client";
import React from "react";

// Callers own pagination because Bazaar fetches pages while other lists slice locally.

export const PAGE_SIZE_OPTIONS = [5, 10, 20] as const;
export type PageSize = (typeof PAGE_SIZE_OPTIONS)[number];
export const DEFAULT_PAGE_SIZE: PageSize = 10;

// Filtering can remove the selected page; clamp it to the remaining list.
export function pageSlice<T>(
  items: T[],
  page: number,
  size: number,
): { rows: T[]; page: number; totalPages: number } {
  const totalPages = Math.max(1, Math.ceil(items.length / size));
  const safe = Math.min(Math.max(0, page), totalPages - 1);
  return {
    rows: items.slice(safe * size, safe * size + size),
    page: safe,
    totalPages,
  };
}

function btn(disabled: boolean): React.CSSProperties {
  return {
    height: 32,
    padding: "0 14px",
    background: "var(--bg-elev-1)",
    border: "1px solid var(--border)",
    borderRadius: "var(--r-2)",
    color: "var(--fg-muted)",
    fontFamily: "var(--font-sans)",
    fontSize: "var(--t-2)",
    cursor: disabled ? "default" : "pointer",
    opacity: disabled ? 0.45 : 1,
  };
}

export function Pager({
  page,
  totalPages,
  total,
  pageSize,
  onPage,
  onPageSize,
  noun = "results",
}: {
  /** 0-indexed. */
  page: number;
  totalPages: number;
  /** Size of the whole list, not of the current page. */
  total: number;
  pageSize: number;
  onPage: (next: number) => void;
  onPageSize: (next: PageSize) => void;
  /** Plural, lowercase: "workflows", "endpoints", "settlements". */
  noun?: string;
}) {
  const canPrev = page > 0;
  const canNext = page < totalPages - 1;

  return (
    <div className="am-pager">
      <div className="am-pager__size">
        <span>Show</span>
        <select
          value={pageSize}
          onChange={(e) => onPageSize(Number(e.target.value) as PageSize)}
          aria-label={`${noun} per page`}
          style={{
            height: 28,
            padding: "0 var(--s-2)",
            border: "1px solid var(--border)",
            background: "var(--bg)",
            borderRadius: "var(--r-2)",
            color: "var(--fg)",
            fontSize: "var(--t-2)",
            fontFamily: "var(--font-sans)",
            cursor: "pointer",
          }}
        >
          {PAGE_SIZE_OPTIONS.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
        <span>
          per page · {total} {noun}
        </span>
      </div>

      <div className="am-pager__nav">
        <span style={{ fontSize: "var(--t-2)", color: "var(--fg-dim)" }}>
          Page {page + 1} of {totalPages}
        </span>
        <button
          type="button"
          onClick={() => onPage(page - 1)}
          disabled={!canPrev}
          style={btn(!canPrev)}
        >
          ← Prev
        </button>
        <button
          type="button"
          onClick={() => onPage(page + 1)}
          disabled={!canNext}
          style={btn(!canNext)}
        >
          Next →
        </button>
      </div>
    </div>
  );
}
