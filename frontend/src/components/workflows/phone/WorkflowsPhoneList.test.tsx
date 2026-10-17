import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Workflow } from "@/lib/types";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("@/components/runs/UpcomingRuns", () => ({ UpcomingRuns: () => null }));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({ balanceUSD: 3.4, balanceKnown: true }),
}));

import { WorkflowsPhoneList } from "./WorkflowsPhoneList";

function wf(overrides: Partial<Workflow>): Workflow {
  return {
    id: overrides.name ?? "wf",
    name: "Workflow",
    nodes: [],
    edges: [],
    status: "deployed",
    ...overrides,
  };
}

const LIST = [
  wf({ name: "Customer Support Triage", spend: "4.218", runs: 1842 }),
  wf({ name: "Daily Market Brief", spend: "1.482", runs: 38 }),
  wf({ name: "Invoice check", status: "paused", runs: 3 }),
];

const rowNames = () =>
  Array.from(
    document.querySelectorAll<HTMLAnchorElement>(".wfp-card__open"),
  ).map((a) => a.querySelector(".wfp-card__name")?.textContent);

afterEach(cleanup);

describe("WorkflowsPhoneList", () => {
  it("shows the header, search, filter and one card per workflow", () => {
    render(
      <WorkflowsPhoneList workflows={LIST} loading={false} error={null} />,
    );
    expect(screen.getByText("$3.40")).toBeTruthy();
    // The header counts the whole workspace and totals its spend.
    expect(screen.getByText(/3 total/)).toBeTruthy();
    expect(screen.getByText(/\$5\.70 spent/)).toBeTruthy();
    expect(
      screen.getByRole("searchbox", { name: "Search workflows" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Filter and sort" }),
    ).toBeTruthy();
    expect(rowNames()).toEqual([
      "Customer Support Triage",
      "Daily Market Brief",
      "Invoice check",
    ]);
  });

  it("has no view toggle and no status tabs", () => {
    render(
      <WorkflowsPhoneList workflows={LIST} loading={false} error={null} />,
    );
    expect(screen.queryByText(/Rows|Grid/)).toBeNull();
    expect(
      screen.queryByRole("button", { name: /^(all|active|paused|draft)$/i }),
    ).toBeNull();
  });

  it("reorders the rows from the filter menu, and back on a second tap", () => {
    render(
      <WorkflowsPhoneList workflows={LIST} loading={false} error={null} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Filter and sort" }));
    fireEvent.click(screen.getByRole("button", { name: /^Lowest cost/ }));
    expect(rowNames()).toEqual([
      "Invoice check",
      "Daily Market Brief",
      "Customer Support Triage",
    ]);
    fireEvent.click(screen.getByRole("button", { name: /^Lowest cost/ }));
    expect(rowNames()[0]).toBe("Customer Support Triage");
  });

  it("filters by search and by status", () => {
    render(
      <WorkflowsPhoneList workflows={LIST} loading={false} error={null} />,
    );
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "brief" },
    });
    expect(rowNames()).toEqual(["Daily Market Brief"]);
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Filter and sort" }));
    fireEvent.click(screen.getByRole("button", { name: "Paused" }));
    expect(rowNames()).toEqual(["Invoice check"]);
  });

  it("offers to clear filters when nothing matches", () => {
    render(
      <WorkflowsPhoneList workflows={LIST} loading={false} error={null} />,
    );
    fireEvent.change(screen.getByRole("searchbox"), {
      target: { value: "zzz" },
    });
    expect(screen.getByText("No workflows match.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(rowNames()).toHaveLength(3);
  });

  it("says where to create the first workflow", () => {
    render(<WorkflowsPhoneList workflows={[]} loading={false} error={null} />);
    expect(
      screen.getByText(/Create one in AgentMesh on a computer/),
    ).toBeTruthy();
  });

  it("shows row-shaped placeholders while loading, and a failure plainly", () => {
    render(
      <WorkflowsPhoneList
        workflows={[]}
        loading
        error="could not load your workflows"
      />,
    );
    expect(screen.getByLabelText("Loading workflows")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toBe(
      "could not load your workflows",
    );
  });

  // A failed first load says nothing about the account, so it must not be
  // presented as an empty one.
  it("shows a failure with a retry, not the empty state, when loading failed", () => {
    const onRetry = vi.fn();
    render(
      <WorkflowsPhoneList
        workflows={[]}
        loading={false}
        error="could not load your workflows"
        onRetry={onRetry}
      />,
    );
    expect(screen.getByRole("alert").textContent).toBe(
      "could not load your workflows",
    );
    expect(screen.queryByText(/No workflows yet/)).toBeNull();
    expect(screen.getByText(/Couldn.t load your workflows/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
    // Nothing loaded, so there are no figures to show -- not zeros.
    const summary = screen.getByLabelText("Workflows not loaded");
    expect(summary.textContent).not.toMatch(/0 total|\$0/);
    expect(screen.queryByLabelText(/0 workflows/)).toBeNull();
  });

  // The list itself can arrive while the aggregation behind it fails: the
  // backend returns the workflows and leaves runs and spend at their zero
  // values. The count is then real and the total is not, so they are said
  // separately rather than both being suppressed or both being printed.
  it("shows the count but not a total when the spend aggregation failed", () => {
    render(
      <WorkflowsPhoneList
        workflows={[
          wf({ id: "a", name: "First", statsUnavailable: true }),
          wf({ id: "b", name: "Second", statsUnavailable: true }),
        ]}
        loading={false}
        error={null}
        onRetry={vi.fn()}
      />,
    );
    const summary = screen.getByLabelText("2 workflows, spend not loaded");
    expect(summary.textContent).toContain("2 total");
    expect(summary.textContent).not.toMatch(/\$0/);
    expect(summary.textContent).toMatch(/-\s*spent/);
  });
});
