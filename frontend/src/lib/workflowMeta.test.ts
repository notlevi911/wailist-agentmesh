import { describe, expect, it } from "vitest";
import type { Workflow } from "./types";
import {
  formatDollars,
  spendDollars,
  totalSpend,
  totalSpendKnown,
  workflowAriaLabel,
  workflowMeta,
} from "./workflowMeta";

const NOW = Date.parse("2026-09-20T12:00:00.000Z");
const at = (ms: number) => new Date(NOW + ms).toISOString();

function wf(over: Partial<Workflow> = {}): Workflow {
  return {
    id: "w1",
    name: "Daily Market Brief",
    nodes: [],
    edges: [],
    status: "deployed",
    runs: 38,
    spend: "1.48",
    ...over,
  };
}

// The line reads "$1.48 · next in 2 h", and its last part is the only one
// that changes colour.
describe("workflowMeta", () => {
  it("ends a scheduled workflow with the time until its next run", () => {
    const m = workflowMeta(wf({ scheduleNextRunAt: at(2 * 3_600_000) }), NOW);
    expect(m.spent).toBe("$1.48");
    expect(m.state).toEqual({ text: "next in 2 h", tone: "accent" });
    expect(m.draft).toBe(false);
  });

  it("says a run already due is due now, never 'next now'", () => {
    const m = workflowMeta(wf({ scheduleNextRunAt: at(-30_000) }), NOW);
    expect(m.state).toEqual({ text: "due now", tone: "accent" });
  });

  it("says nothing is queued when a deployed workflow has no schedule", () => {
    const m = workflowMeta(wf({ runs: 1842, spend: "4.218" }), NOW);
    expect(m.spent).toBe("$4.22");
    expect(m.state).toEqual({ text: "no run queued", tone: "dim" });
  });

  it("falls back to no run queued when the next run is unreadable", () => {
    const m = workflowMeta(wf({ scheduleNextRunAt: "soon" }), NOW);
    expect(m.state).toEqual({ text: "no run queued", tone: "dim" });
  });

  it("names paused and error states in their own tones", () => {
    expect(
      workflowMeta(wf({ status: "paused", spend: "0.89" }), NOW).state,
    ).toEqual({ text: "paused", tone: "warm" });
    expect(workflowMeta(wf({ status: "error" }), NOW).state).toEqual({
      text: "error",
      tone: "danger",
    });
  });

  it("describes a workflow that never ran as a draft, without figures", () => {
    const m = workflowMeta(
      wf({ status: "draft", runs: 0, spend: undefined }),
      NOW,
    );
    expect(m.draft).toBe(true);
  });

  it("still shows the figures of a draft that has run before", () => {
    const m = workflowMeta(wf({ status: "draft", runs: 4 }), NOW);
    expect(m.draft).toBe(false);
    expect(m.state).toEqual({ text: "not deployed", tone: "dim" });
  });

  // The count named no period, so it read as neither a rate nor a total, and
  // the website's Usage page answers that question properly.
  it("carries no run count", () => {
    expect(workflowMeta(wf({ runs: 1842 }), NOW)).not.toHaveProperty("runs");
  });

  // `draft` means "never ran", not "status is draft", so it still reads the
  // count even though nothing prints it. Removing the read would relabel a
  // draft that has runs behind it.
  it("still reads the count to tell a never-run draft from a stopped one", () => {
    expect(workflowMeta(wf({ status: "draft", runs: 0 }), NOW).draft).toBe(
      true,
    );
    expect(
      workflowMeta(wf({ status: "draft", runs: undefined }), NOW).draft,
    ).toBe(true);
    expect(workflowMeta(wf({ status: "draft", runs: 1 }), NOW).draft).toBe(
      false,
    );
  });

  it("treats a missing or unreadable spend as zero", () => {
    expect(workflowMeta(wf({ spend: undefined }), NOW).spent).toBe("$0");
    expect(workflowMeta(wf({ spend: "" }), NOW).spent).toBe("$0");
  });

  it("calls the legacy active status deployed", () => {
    expect(workflowMeta(wf({ status: "active" }), NOW).statusWord).toBe(
      "deployed",
    );
  });
});

describe("spendDollars and totalSpend", () => {
  it("adds up the sample workspace to $8.71", () => {
    const list = [
      wf({ spend: "4.218" }),
      wf({ spend: "1.48" }),
      wf({ spend: "0.89" }),
      wf({ spend: undefined }),
      wf({ spend: "2.12" }),
      wf({ spend: undefined }),
    ];
    expect(formatDollars(totalSpend(list))).toBe("$8.71");
  });

  it("reads a dollar string, and zero when there is none", () => {
    expect(spendDollars("4.218")).toBeCloseTo(4.218);
    expect(spendDollars(undefined)).toBe(0);
    expect(spendDollars("nope")).toBe(0);
  });
});

// The rail carries the status as colour alone, so the label has to say it.
describe("workflowAriaLabel", () => {
  it("names the workflow, its status and its figures", () => {
    const w = wf({
      name: "Customer Support Triage",
      runs: 1842,
      spend: "4.218",
    });
    expect(workflowAriaLabel(w, workflowMeta(w, NOW))).toBe(
      "Customer Support Triage, deployed. $4.22 spent, no run queued.",
    );
  });

  it("does not say paused twice", () => {
    const w = wf({
      name: "Invoice Reconciliation",
      status: "paused",
      runs: 217,
      spend: "0.89",
    });
    expect(workflowAriaLabel(w, workflowMeta(w, NOW))).toBe(
      "Invoice Reconciliation, paused. $0.890 spent.",
    );
  });

  it("keeps a never-run draft short", () => {
    const w = wf({
      name: "Content Pipeline",
      status: "draft",
      runs: 0,
      spend: undefined,
    });
    expect(workflowAriaLabel(w, workflowMeta(w, NOW))).toBe(
      "Content Pipeline, draft. Never run.",
    );
  });

  // A scheduled workflow whose next time has not reached the list said "no
  // run queued", which reads as "nothing will happen" about a workflow that
  // runs every weekday.
  //
  // The zone is given explicitly. Which weekdays 07:00 UTC falls on is a
  // property of where the reader is -- in Anchorage or Honolulu it is the
  // evening before, so the same cron is Sunday to Thursday -- and this test
  // is about the sentence being the schedule rather than "no run queued",
  // not about the conversion, which describeSchedule's own tests cover.
  it("says the schedule when no next run time is known", () => {
    const meta = workflowMeta(
      {
        id: "wf-1",
        name: "Daily Market Brief",
        status: "deployed",
        nodes: [],
        edges: [],
        scheduleCron: "0 7 * * 1-5",
      },
      NOW,
      "America/New_York",
    );
    expect(meta.state.text).toMatch(/^every weekday at /);
    expect(meta.state.text).not.toContain("0 7 * * 1-5");
  });

  // ListWorkflows returns the base list when attachWorkflowStats fails, and
  // `runs` is omitted at zero, so an outage looks exactly like a workflow
  // that never ran. Printed as figures it reads as fact: "$0 spent".
  describe("when the run and spend aggregation failed", () => {
    const failed = (extra: Partial<Workflow> = {}) =>
      wf({
        statsUnavailable: true,
        runs: undefined,
        spend: undefined,
        ...extra,
      });

    it("shows a dash for spend rather than $0", () => {
      expect(workflowMeta(failed(), NOW).spent).toBe("-");
    });

    it("does not call a draft never run", () => {
      const m = workflowMeta(failed({ status: "draft" }), NOW);
      expect(m.draft).toBe(false);
      expect(workflowAriaLabel(failed({ status: "draft" }), m)).not.toContain(
        "Never run",
      );
    });

    it("keeps saying $0 when the figures really are zero", () => {
      expect(workflowMeta(wf({ spend: undefined }), NOW).spent).toBe("$0");
    });

    it("reports whether a total can be summed at all", () => {
      expect(totalSpendKnown([wf({ spend: "1.00" })])).toBe(true);
      expect(totalSpendKnown([wf({ spend: "1.00" }), failed()])).toBe(false);
    });
  });

  it("still says no run queued when there is no schedule at all", () => {
    const meta = workflowMeta(
      {
        id: "wf-2",
        name: "Manual only",
        status: "deployed",
        nodes: [],
        edges: [],
      },
      Date.now(),
    );
    expect(meta.state.text).toBe("no run queued");
  });

  // A known next run is the more useful thing, so it still wins.
  it("prefers the next run time when the list knows it", () => {
    const meta = workflowMeta(
      {
        id: "wf-3",
        name: "Daily Market Brief",
        status: "deployed",
        nodes: [],
        edges: [],
        scheduleCron: "0 7 * * 1-5",
        scheduleNextRunAt: new Date(Date.now() + 3 * 3_600_000).toISOString(),
      },
      Date.now(),
    );
    expect(meta.state.text).toMatch(/^next in /);
  });
});
