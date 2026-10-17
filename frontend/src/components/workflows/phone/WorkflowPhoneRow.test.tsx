import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import type { Workflow } from "@/lib/types";
import { WorkflowPhoneRow } from "./WorkflowPhoneRow";

const NOW = Date.parse("2026-09-19T10:00:00.000Z");

function renderRow(overrides: Partial<Workflow> = {}) {
  const wf: Workflow = {
    id: "wf-triage",
    name: "Customer Support Triage",
    status: "deployed",
    nodes: [],
    edges: [],
    runs: 1842,
    spend: "4.218",
    tags: ["support", "production"],
    updatedAt: "2026-09-19T09:58:00.000Z",
    scheduleNextRunAt: new Date(NOW + 12 * 60_000).toISOString(),
    ...overrides,
  };
  render(
    <ul>
      <WorkflowPhoneRow workflow={wf} now={NOW} />
    </ul>,
  );
  return screen.getByRole("listitem");
}

afterEach(cleanup);

describe("WorkflowPhoneRow", () => {
  it("opens the workflow and its scoped usage from separate links", () => {
    const card = renderRow();
    expect(
      within(card)
        .getByRole("link", {
          name: "Customer Support Triage, deployed. $4.22 spent, next in 12 min.",
        })
        .getAttribute("href"),
    ).toContain("wf-triage");
    expect(
      within(card)
        .getByRole("link", { name: "View usage for Customer Support Triage" })
        .getAttribute("href"),
    ).toBe("/usage?workflow=wf-triage");
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
    expect(within(card).getAllByRole("link")).toHaveLength(2);
  });

  it("puts the figures on one line ending in the workflow's state", () => {
    const card = renderRow();
    expect(within(card).getByText("Customer Support Triage")).toBeTruthy();
    expect(card.textContent).toContain("$4.22");
    expect(card.textContent).toContain("next in 12 min");
    // A count with no period behind it read as neither a rate nor a total;
    // the website's Usage page answers that question properly.
    expect(card.textContent).not.toContain("1,842");
    // The three-column Spent / Runs / Upcoming run grid is gone.
    expect(within(card).queryByText("Spent")).toBeNull();
    expect(within(card).queryByText("Upcoming run")).toBeNull();
  });

  it("carries the status on the card so the rail can be coloured", () => {
    expect(renderRow().dataset.status).toBe("deployed");
    cleanup();
    expect(renderRow({ status: "paused" }).dataset.status).toBe("paused");
    cleanup();
    expect(renderRow({ status: "error" }).dataset.status).toBe("error");
    cleanup();
    // The legacy "active" is the same state, and must colour the same rail.
    expect(renderRow({ status: "active" }).dataset.status).toBe("deployed");
  });

  // A deployed workflow with nothing queued has nothing to shout about, so
  // the colour follows what the line says rather than the status.
  it("colours the state by what it says, not by the status", () => {
    const tone = (card: HTMLElement) =>
      card.querySelector(".wfp-card__state")?.getAttribute("data-tone");
    expect(tone(renderRow())).toBe("accent");
    cleanup();
    expect(tone(renderRow({ scheduleNextRunAt: undefined }))).toBe("dim");
    cleanup();
    expect(tone(renderRow({ status: "paused" }))).toBe("warm");
    cleanup();
    expect(tone(renderRow({ status: "error" }))).toBe("danger");
  });

  it("names the status in words, because the rail is only colour", () => {
    const card = renderRow();
    expect(
      within(card).getAllByRole("link")[0].getAttribute("aria-label"),
    ).toBe("Customer Support Triage, deployed. $4.22 spent, next in 12 min.");
  });

  it("leaves out the logo, tags, updated time and Open or Zone", () => {
    const card = renderRow();
    expect(card.querySelector("svg")).toBeNull();
    // The tags are "support" and "production"; the title also says Support.
    expect(within(card).queryByText(/production|^#?support$/i)).toBeNull();
    expect(within(card).queryByText(/Open|Zone|Updated|ago/)).toBeNull();
  });

  it("says a workflow that never ran is a draft, and shows no figures", () => {
    const card = renderRow({
      status: "draft",
      runs: undefined,
      spend: undefined,
      scheduleNextRunAt: undefined,
    });
    expect(card.dataset.status).toBe("draft");
    expect(card.textContent).toContain("draft");
    expect(card.textContent).toContain("never run");
    expect(card.textContent).not.toContain("$");
    expect(
      within(card).getAllByRole("link")[0].getAttribute("aria-label"),
    ).toBe("Customer Support Triage, draft. Never run.");
  });

  it("says nothing is queued for a deployed workflow with no schedule", () => {
    const card = renderRow({ scheduleNextRunAt: undefined });
    expect(card.textContent).toContain("no run queued");
  });
});
