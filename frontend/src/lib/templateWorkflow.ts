import { workflows as workflowsApi } from "@/lib/api";
import type { Workflow } from "@/lib/types";

// loadTemplateWorkflow creates a fresh workflow row from a template (a demo
// or "try it" pipeline defined in lib/data.ts) and writes its full graph in
// one create()-then-update() pair -- the same two-call pattern the canvas
// editor's own save path already uses. Shared by every "load this template"
// button (WorkflowsPage's demo button, each partner ConsoleCard's "try a
// workflow" icon) so the create/update/rollback sequence has exactly one
// place to fix, not one copy per call site.
//
// On failure it best-effort deletes the row create() just made, so a
// partial failure never leaves an empty, unnamed orphan sitting in the
// user's workflow list. Returns the new workflow's id; callers own their own
// loading/error UI state and navigation.
export async function loadTemplateWorkflow(template: Workflow): Promise<string> {
  let wf: Workflow | undefined;
  try {
    wf = await workflowsApi.create(template.name);
    // UpdateWorkflow (backend/internal/api/handlers/workflows.go) overwrites
    // name unconditionally from the request body -- omitting it here would
    // blank out the name create() just set.
    await workflowsApi.update(wf.id, {
      name: template.name,
      nodes: template.nodes,
      edges: template.edges,
    });
    return wf.id;
  } catch (e) {
    if (wf) await workflowsApi.remove(wf.id).catch(() => {});
    throw e;
  }
}
