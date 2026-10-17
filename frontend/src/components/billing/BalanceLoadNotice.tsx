"use client";
import { rowBtn } from "@/components/ui/buttons";

export function BalanceLoadNotice({
  known,
  failed,
  loading,
  onRetry,
}: {
  known: boolean;
  failed: boolean;
  loading: boolean;
  onRetry: () => Promise<void>;
}) {
  if (!failed) return null;
  return (
    <div
      role="status"
      style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: 8, marginTop: 12, fontSize: 13, color: "var(--fg-muted)", position: "relative" }}
    >
      <span>
        {known
          ? "Could not refresh your balance. Showing the last known amount."
          : "Could not load your balance."}
      </span>
      <button
        type="button"
        aria-label="Retry balance"
        disabled={loading}
        onClick={() => void onRetry()}
        style={{ ...rowBtn, minHeight: 44, color: "var(--accent)", opacity: loading ? 0.6 : 1 }}
      >
        {loading ? "Retrying…" : "Retry"}
      </button>
    </div>
  );
}
