"use client";
import Link from "next/link";
import type { Workflow } from "@/lib/types";
import { workflowHref } from "@/lib/routes";
import { usageHrefForWorkflow } from "@/lib/usageScope";
import { workflowAriaLabel, workflowMeta } from "@/lib/workflowMeta";

// One workflow on the phone list: a card with a coloured rail down its left
// edge and a single line of figures under the name. The main area opens the
// workflow, while the small Usage action opens its read-only spend view.
// Spent covers the last 30 days, the window the list endpoint counts.
//
// No run count. A bare "1,842 runs" names no period, so it reads as neither a
// rate nor a total, and the website's Usage page answers that question
// properly. Spend stays because its window is stated.
export function WorkflowPhoneRow({
  workflow: wf,
  now,
}: {
  workflow: Workflow;
  now: number;
}) {
  const meta = workflowMeta(wf, now);
  return (
    <li className="wfp-card" data-status={meta.statusWord}>
      <span className="wfp-card__rail" aria-hidden />
      <Link
        href={workflowHref(wf.id)}
        className="wfp-card__open"
        aria-label={workflowAriaLabel(wf, meta)}
      >
        <span className="wfp-card__name">{wf.name}</span>
        <p className="wfp-card__meta">
          {meta.draft ? (
            <>
              draft <span aria-hidden>·</span>{" "}
              <span className="wfp-card__state" data-tone="dim">
                never run
              </span>
            </>
          ) : (
            <>
              {meta.spent} <span aria-hidden>·</span>{" "}
              <span className="wfp-card__state" data-tone={meta.state.tone}>
                {meta.state.text}
              </span>
            </>
          )}
        </p>
      </Link>
      <Link
        href={usageHrefForWorkflow(wf.id)}
        className="wfp-card__usage"
        aria-label={`View usage for ${wf.name}`}
      >
        Usage
      </Link>
    </li>
  );
}
