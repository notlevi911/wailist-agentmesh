import type { Workflow } from "./types";
import { formatSpend, formatUntil } from "./runFormat";
import { describeSchedule } from "./describeSchedule";

// How one workflow reads on the phone list: a line of figures ending in the
// one thing worth knowing about its state. Kept apart from the component so
// the wording can be tested on its own, and so the header's total and a
// card's figure are parsed the same way.

// Colour carries the state. "dim" means the state is unremarkable.
export type Tone = "accent" | "warm" | "danger" | "dim";

// The list sends spend as a dollar string ("4.218") and omits it at zero.
export function spendDollars(spend: string | undefined): number {
  const dollars = Number.parseFloat(spend ?? "");
  return Number.isFinite(dollars) ? dollars : 0;
}

export function totalSpend(list: Workflow[]): number {
  return list.reduce((sum, wf) => sum + spendDollars(wf.spend), 0);
}

/** The dash this screen shows wherever a figure is not known. */
export const UNKNOWN = "-";

/**
 * Whether a workflow's run and spend figures mean anything.
 *
 * False when the backend's aggregation failed: `runs` and `spend` are then
 * absent for the same reason they are absent on a workflow that never ran,
 * and summing or printing them states an outage as fact.
 */
export function statsKnown(wf: Workflow): boolean {
  return wf.statsUnavailable !== true;
}

/** True only when every row's figures are real, so a total can be summed. */
export function totalSpendKnown(list: Workflow[]): boolean {
  return list.every(statsKnown);
}

// Dollars through the same formatter every run's spend uses, so $0.890 and
// $4.22 read alike wherever they appear.
export function formatDollars(dollars: number): string {
  return formatSpend(Math.round(dollars * 1e6));
}

export interface WorkflowMeta {
  // A workflow that has never run says so instead of printing $0 and 0.
  draft: boolean;
  spent: string;
  state: { text: string; tone: Tone };
  statusWord: string;
}

function statusWord(status: Workflow["status"]): string {
  return status === "active" ? "deployed" : (status ?? "draft");
}

function state(
  wf: Workflow,
  now: number,
  timeZone?: string,
): { text: string; tone: Tone } {
  switch (wf.status) {
    case "paused":
      return { text: "paused", tone: "warm" };
    case "error":
      return { text: "error", tone: "danger" };
    case "draft":
      // Only reached once a draft has runs behind it -- one that never ran
      // is described by `draft` instead.
      return { text: "not deployed", tone: "dim" };
    default:
      break;
  }
  const until = wf.scheduleNextRunAt
    ? formatUntil(wf.scheduleNextRunAt, now)
    : "-";
  // formatUntil says "—" for a time it cannot read.
  if (until === "-") {
    // A workflow on a schedule is not "no run queued" just because no next
    // time reached this screen: it still runs every weekday, whether or not
    // the list knows when. Saying the schedule is both truer and more useful.
    if (wf.scheduleCron) {
      const words = describeSchedule(wf.scheduleCron, new Date(now), timeZone);
      // Sentence case on its own line elsewhere, lower case in this line of
      // figures ("$1.48 · every weekday at 7:00 AM").
      return {
        text: words.charAt(0).toLowerCase() + words.slice(1),
        tone: "dim",
      };
    }
    return { text: "no run queued", tone: "dim" };
  }
  // It says "now" for a time already reached; "next now" is not English.
  return {
    text: until === "now" ? "due now" : `next ${until}`,
    tone: "accent",
  };
}

/**
 * The figures a workflow row shows, as of `now`. `timeZone` is the zone a
 * schedule is described in, the reader's own when left out; it is passed
 * explicitly where a caller needs the answer not to depend on where the
 * machine happens to be.
 */
export function workflowMeta(
  wf: Workflow,
  now: number,
  timeZone?: string,
): WorkflowMeta {
  // The count is no longer shown -- it named no period, so it read as neither
  // a rate nor a total. It is still read here because `draft` is "never ran",
  // not "status is draft": a draft with runs behind it says "not deployed".
  const runs = wf.runs ?? 0;
  return {
    // "Never run" is a claim about history, and an unavailable aggregation
    // is not evidence for it.
    draft: statsKnown(wf) && wf.status === "draft" && runs === 0,
    spent: statsKnown(wf) ? formatDollars(spendDollars(wf.spend)) : UNKNOWN,
    state: state(wf, now, timeZone),
    statusWord: statusWord(wf.status),
  };
}

// The card is one link, and its rail carries the status as colour alone.
// This says the same thing in words, for anyone who cannot see the rail.
export function workflowAriaLabel(wf: Workflow, meta: WorkflowMeta): string {
  if (meta.draft) return `${wf.name}, draft. Never run.`;
  const parts = [`${meta.spent} spent`];
  // "paused ... paused" adds nothing; the status is already named.
  if (meta.state.text !== meta.statusWord) parts.push(meta.state.text);
  return `${wf.name}, ${meta.statusWord}. ${parts.join(", ")}.`;
}
