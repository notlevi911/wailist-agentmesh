// The Usage page can be scoped to a single workflow's spend with
// ?workflow=<id> (see UsagePage). These helpers are the one place that link is
// built and that scope is labelled, so the workflows list that links in and
// the page that reads the param can't drift apart (#9).

import type { WorkflowSpend } from "./types";

/** The Usage page, scoped to one workflow's spend. */
export function usageHrefForWorkflow(workflowId: string): string {
  return `/usage?workflow=${encodeURIComponent(workflowId)}`;
}

/**
 * What the Usage page's "filtered to" banner shows for a scoped workflow: its
 * name, once the spend data has loaded and includes it, otherwise the raw id.
 * A workflow with no spend in the selected range is not in byWorkflow at all,
 * so the id fallback is a real case, not just a loading state.
 */
export function scopedWorkflowLabel(
  workflowId: string,
  byWorkflow: WorkflowSpend[] | undefined,
): string {
  const name = byWorkflow
    ?.find((w) => w.workflowId === workflowId)
    ?.name?.trim();
  return name || workflowId;
}
