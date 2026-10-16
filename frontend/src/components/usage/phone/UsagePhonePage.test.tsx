import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { buildUsage } from "@/lib/data";
import { workflowHref } from "@/lib/routes";

// The charts draw SVG from measured sizes; what is under test is the screen
// around them.
vi.mock("../AreaChart", () => ({
  AreaChart: () => <div data-testid="chart" />,
}));
vi.mock("../Donut", () => ({ Donut: () => <div data-testid="donut" /> }));
const credits = vi.hoisted(() => ({
  balanceUSD: 12.5,
  balanceKnown: true,
  lowBalanceThresholdUSD: 5,
  refreshBalance: vi.fn(async () => {}),
}));
vi.mock("@/lib/credits/store", () => ({ useCredits: () => credits }));
vi.mock("@/components/PullToRefresh", () => ({
  PullToRefresh: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

import { UsagePhonePage, type UsagePhoneProps } from "./UsagePhonePage";

function renderPage(over: Partial<UsagePhoneProps> = {}) {
  const props: UsagePhoneProps = {
    range: "30d",
    onRange: vi.fn(),
    data: buildUsage("30d"),
    loading: false,
    error: null,
    onRetry: vi.fn(),
    scopedWorkflowId: null,
    onClearScope: vi.fn(),
    ...over,
  };
  return { ...render(<UsagePhonePage {...props} />), props };
}

afterEach(() => {
  cleanup();
  credits.balanceUSD = 12.5;
  credits.balanceKnown = true;
  credits.lowBalanceThresholdUSD = 5;
});

describe("UsagePhonePage", () => {
  // Usage is a tab, so the bottom bar is the way around; no back link.
  it("has no back link of its own", () => {
    renderPage();
    expect(screen.queryByRole("link", { name: /Account|Back/ })).toBeNull();
  });

  // What is left, beside what was spent -- and the way to top up.
  it("shows the credits left, linked to Credits", () => {
    renderPage();
    const row = screen.getByRole("link", { name: /Credits left/ });
    expect(row.getAttribute("href")).toBe("/billing");
    expect(row.textContent).toContain("$12.50");
    expect(row.textContent).not.toContain("Low");
    expect(credits.refreshBalance).toHaveBeenCalled();
  });

  it("flags a low balance, and shows a dash until it is known", () => {
    credits.balanceUSD = 1.2;
    renderPage();
    expect(
      screen.getByRole("link", { name: /Credits left/ }).textContent,
    ).toContain("Low");
    cleanup();
    credits.balanceKnown = false;
    renderPage();
    expect(
      screen.getByRole("link", { name: /Credits left/ }).textContent,
    ).toContain("-");
  });

  it.each([
    { threshold: 0, low: false },
    { threshold: 1, low: false },
    { threshold: 2, low: true },
  ])("uses the saved threshold $threshold", ({ threshold, low }) => {
    credits.balanceUSD = 1.2;
    credits.lowBalanceThresholdUSD = threshold;
    renderPage();
    const text = screen.getByRole("link", { name: /Credits left/ }).textContent;
    expect(text?.includes("Low")).toBe(low);
  });

  it("marks the chosen range and reports a new one", () => {
    const { props } = renderPage();
    expect(
      screen.getByRole("radio", { name: "30d" }).getAttribute("aria-checked"),
    ).toBe("true");
    fireEvent.click(screen.getByRole("radio", { name: "30d" }));
    expect(props.onRange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("radio", { name: "7d" }));
    expect(props.onRange).toHaveBeenCalledWith("7d");
  });

  it("leads with what was spent and how many calls, for the range", () => {
    renderPage();
    expect(screen.getByText("Spent · 30d")).toBeTruthy();
    expect(screen.getByText("Calls · 30d")).toBeTruthy();
  });

  // The desktop endpoints table is 984px wide; a phone gets five rows.
  it("lists the top five endpoints, and all of them on request", () => {
    const data = buildUsage("30d");
    renderPage({ data });
    const heading = screen.getByRole("heading", { name: "Endpoints" });
    const list = heading.parentElement!.querySelector("ul")!;
    expect(list.children).toHaveLength(5);
    fireEvent.click(
      screen.getByRole("button", {
        name: `See all endpoints (${data.byEndpoint.length})`,
      }),
    );
    expect(list.children).toHaveLength(data.byEndpoint.length);

    // And it closes again.
    const fewer = screen.getByRole("button", { name: "Show fewer" });
    expect(fewer.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(fewer);
    expect(list.children).toHaveLength(5);
    expect(
      screen
        .getByRole("button", { name: /See all endpoints/ })
        .getAttribute("aria-expanded"),
    ).toBe("false");
  });

  // Part of the hash, as the website's settlements table shows it.
  it("shows the start of each settlement's hash, linked to the explorer", () => {
    const data = buildUsage("30d");
    renderPage({ data });
    const s = data.settlements[0];
    const link = screen.getByRole("link", { name: `Transaction ${s.txId}` });
    expect(link.textContent).toBe(`${s.txId.slice(0, 10)}…`);
    expect(link.getAttribute("href")).toBe(s.explorerURL);
  });

  // The page fetches more settlements than it first shows; the rest must be
  // reachable, and the list must close again.
  it("lists the latest five settlements, and all of them on request", () => {
    const data = buildUsage("30d");
    expect(data.settlements.length).toBeGreaterThan(5);
    renderPage({ data });
    const heading = screen.getByRole("heading", { name: "Recent settlements" });
    const list = heading.parentElement!.querySelector("ul")!;
    expect(list.children).toHaveLength(5);

    fireEvent.click(
      screen.getByRole("button", {
        name: `See all settlements (${data.settlements.length})`,
      }),
    );
    expect(list.children).toHaveLength(data.settlements.length);

    const fewer = screen.getAllByRole("button", { name: "Show fewer" }).at(-1)!;
    expect(fewer.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(fewer);
    expect(list.children).toHaveLength(5);
  });

  it("opens a workflow from its spend row", () => {
    const data = buildUsage("30d");
    renderPage({ data });
    const top = [...data.byWorkflow].sort((a, b) => b.algo - a.algo)[0];
    expect(
      screen
        .getByRole("link", { name: new RegExp(top.name) })
        .getAttribute("href"),
    ).toBe(workflowHref(top.workflowId));
  });

  it("shows and clears a workflow scope while filtering spend rows", () => {
    const data = buildUsage("30d");
    const scoped = data.byWorkflow.at(-1)!;
    const { props } = renderPage({
      data,
      scopedWorkflowId: scoped.workflowId,
    });

    expect(
      screen.getByText(`Workflows by spend · filtered to ${scoped.name}`),
    ).toBeTruthy();
    const section = screen
      .getByRole("heading", { name: "Workflows by spend" })
      .closest("section")!;
    expect(within(section).getAllByRole("link")).toHaveLength(1);
    expect(within(section).getByText(scoped.name)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(props.onClearScope).toHaveBeenCalledTimes(1);
  });

  it("offers a retry when nothing could be loaded", () => {
    const { props } = renderPage({ data: null, error: new Error("down") });
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(props.onRetry).toHaveBeenCalledTimes(1);
  });

  it("shows a skeleton, not an error, while the first load runs", () => {
    renderPage({ data: null, loading: true });
    expect(screen.getByLabelText("Loading usage")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
