"use client";
import { useEffect, useState } from "react";
import { CanvasPage } from "@/components/canvas/CanvasPage";
import { TendrilConsolePage } from "@/components/tendril/TendrilConsolePage";
import { PrismConsolePage } from "@/components/prism/PrismConsolePage";
import { tendril } from "@/lib/tendril";
import { prism } from "@/lib/prism";

// Most workflow ids open the normal canvas. A handful are consoles: one
// hidden row per user per partner (backend: GetOrCreateSystemWorkflow),
// matched here by id -- never by name, since the backend's real row name has
// no fixed relationship to any frontend constant. Those rows never show the
// editor, because there is nothing on their canvas to show: renting hardware
// or running a paid AI task is a fill-a-form-and-press-a-button job, not
// something a node graph makes better.
//
// Both lookups use the consoleWorkflowIdIfExists() variants, NOT console() --
// the latter creates the console row on first call, which here would mean
// every workflow-page visit silently minting a hidden console row for users
// who have never touched either partner, just from opening one of their own
// unrelated workflows. A "not found yet" answer trivially resolves to "this
// isn't a console" without ever needing to create one.
type ConsoleKind = "tendril" | "prism" | null;

export function WorkflowRoute({ workflowId }: { workflowId: string }) {
  const [consoleKind, setConsoleKind] = useState<ConsoleKind | undefined>(
    () => (workflowId === "new" ? null : undefined),
  );

  useEffect(() => {
    if (workflowId === "new") return;
    let stale = false;
    // One parallel round, not two sequential awaits: this blocks the page
    // behind a "loading…" state on EVERY workflow visit, so awaiting the
    // consoles one after another would double that delay for every user who
    // is just opening an ordinary workflow.
    Promise.allSettled([
      tendril.consoleWorkflowIdIfExists(),
      prism.consoleWorkflowIdIfExists(),
    ]).then(([tendrilResult, prismResult]) => {
      if (stale) return;
      // A rejected lookup degrades to "not a console" rather than an error
      // screen: the canvas is the overwhelmingly likely correct answer, and
      // a transient failure of this check should never block a user from
      // their own workflow.
      const tendrilId =
        tendrilResult.status === "fulfilled" ? tendrilResult.value : null;
      const prismId =
        prismResult.status === "fulfilled" ? prismResult.value : null;
      if (tendrilId && workflowId === tendrilId) setConsoleKind("tendril");
      else if (prismId && workflowId === prismId) setConsoleKind("prism");
      else setConsoleKind(null);
    });
    return () => {
      stale = true;
    };
  }, [workflowId]);

  if (consoleKind === undefined) {
    return (
      <div
        className="am-viewport"
        style={{
          height: "100dvh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "var(--bg)",
          color: "var(--fg-dim)",
          fontFamily: "var(--font-mono)",
          fontSize: 12,
        }}
      >
        loading…
      </div>
    );
  }
  if (consoleKind === "tendril") return <TendrilConsolePage />;
  if (consoleKind === "prism") return <PrismConsolePage />;
  return <CanvasPage key={workflowId} workflowId={workflowId} />;
}
