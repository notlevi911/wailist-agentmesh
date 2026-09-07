"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { workflows } from "@/lib/api";
import { useModalDismissal } from "@/hooks/useModalDismissal";
import {
  encodePendingNode,
  resourceToNode,
  type BazaarResource,
} from "@/lib/bazaar";
import type { Workflow } from "@/lib/types";

// Picks which canvas an endpoint lands on. The node itself travels in the
// destination URL rather than being written server-side — this page never
// saves anything on its own. The canvas DOES auto-save on any workflow
// change (including this auto-added node) on a ~1.5s debounce, but it
// deliberately suppresses exactly that one auto-save cycle after applying a
// pending add (see the `justLoaded` reuse in CanvasPage.tsx's `?add=`
// effect) — the same skip-once mechanism used for the initial load. So the
// node is never persisted by the act of adding it; only the user's next real
// edit (drag, wire, field change) triggers an actual save.
export function AddToWorkflowDialog({
  resource,
  onClose,
}: {
  resource: BazaarResource;
  onClose: () => void;
}) {
  const router = useRouter();
  const [list, setList] = useState<Workflow[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    workflows
      .list()
      .then((w) => {
        if (!cancelled) setList(w);
      })
      .catch((e: unknown) => {
        if (!cancelled)
          setError(e instanceof Error ? e.message : "could not load workflows");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useModalDismissal(onClose);

  const choose = (id: string) => {
    const encoded = encodePendingNode(resourceToNode(resource));
    router.push(`/workflows/${id}?add=${encoded}`);
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Add endpoint to a workflow"
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 50,
        padding: 20,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(440px, 100%)",
          maxHeight: "70vh",
          overflowY: "auto",
          background: "var(--bg-elev-1)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-2)",
          padding: 18,
          display: "flex",
          flexDirection: "column",
          gap: 14,
        }}
      >
        <div>
          <div style={{ fontSize: 15, fontWeight: 600, color: "var(--fg)" }}>
            Add to a workflow
          </div>
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              color: "var(--fg-dim)",
              marginTop: 4,
              wordBreak: "break-all",
            }}
          >
            {resource.url}
          </div>
        </div>

        {error && (
          <p style={{ margin: 0, fontSize: 12, color: "var(--danger)" }}>{error}</p>
        )}
        {!list && !error && (
          <p style={{ margin: 0, fontSize: 12, color: "var(--fg-dim)" }}>
            Loading your workflows…
          </p>
        )}
        {list?.length === 0 && (
          <p style={{ margin: 0, fontSize: 12, color: "var(--fg-dim)" }}>
            You have no workflows yet. Create one first, then add this endpoint
            to it.
          </p>
        )}

        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {list?.map((w) => (
            <button
              key={w.id}
              type="button"
              onClick={() => choose(w.id)}
              style={{
                textAlign: "left",
                padding: "10px 12px",
                border: "1px solid var(--border)",
                background: "var(--bg)",
                borderRadius: "var(--r-2)",
                color: "var(--fg)",
                fontSize: 13,
                cursor: "pointer",
                fontFamily: "var(--font-sans)",
              }}
            >
              {w.name}
            </button>
          ))}
        </div>

        <button
          type="button"
          onClick={onClose}
          style={{
            height: 34,
            border: "1px solid var(--border)",
            background: "transparent",
            color: "var(--fg-muted)",
            borderRadius: "var(--r-2)",
            fontSize: 12,
            cursor: "pointer",
            fontFamily: "var(--font-sans)",
          }}
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
