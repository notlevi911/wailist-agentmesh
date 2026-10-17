import { describe, expect, it } from "vitest";
import { scopedWorkflowLabel, usageHrefForWorkflow } from "./usageScope";
import type { WorkflowSpend } from "./types";

describe("usageHrefForWorkflow", () => {
  it("links to the Usage page scoped by ?workflow=", () => {
    expect(usageHrefForWorkflow("wf_123")).toBe("/usage?workflow=wf_123");
  });

  // UsagePage reads the scope with URLSearchParams(location.search).get(),
  // so whatever the link carries must come back out as the exact id.
  it("round-trips through the same read UsagePage does", () => {
    const id = "a b&c=d/é";
    const href = usageHrefForWorkflow(id);
    const url = new URL(href, "https://app.example");
    expect(url.pathname).toBe("/usage");
    expect(url.searchParams.get("workflow")).toBe(id);
  });
});

describe("scopedWorkflowLabel", () => {
  const spend: WorkflowSpend[] = [
    { workflowId: "wf_1", name: "Morning digest", algo: 1, calls: 3 },
    { workflowId: "wf_2", name: "   ", algo: 0, calls: 0 },
  ];

  it("shows the workflow's name when its spend row is loaded", () => {
    expect(scopedWorkflowLabel("wf_1", spend)).toBe("Morning digest");
  });

  it("falls back to the id before data loads", () => {
    expect(scopedWorkflowLabel("wf_1", undefined)).toBe("wf_1");
  });

  it("falls back to the id when the workflow has no spend in range", () => {
    expect(scopedWorkflowLabel("wf_9", spend)).toBe("wf_9");
  });

  it("falls back to the id when the name is blank", () => {
    expect(scopedWorkflowLabel("wf_2", spend)).toBe("wf_2");
  });
});
