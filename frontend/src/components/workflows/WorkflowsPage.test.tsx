import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

// The page is tested against a stubbed API. The top bar and the pull gesture
// have their own tests, so they are reduced to what this page hands them.
const state = vi.hoisted(() => ({
  readOnly: false,
  list: vi.fn(),
  push: vi.fn(),
}));

vi.mock("@/hooks/useReadOnly", () => ({ useReadOnly: () => state.readOnly }));
vi.mock("@/lib/api", () => ({ workflows: { list: state.list } }));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({
    balanceUSD: 7,
    balanceKnown: true,
    refreshBalance: () => Promise.resolve(),
  }),
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: state.push }),
}));
vi.mock("@/components/Topbar", () => ({ Topbar: () => null }));
vi.mock("@/components/runs/UpcomingRuns", () => ({
  UpcomingRuns: () => null,
}));
vi.mock("@/components/PullToRefresh", () => ({
  PullToRefresh: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

import { WorkflowsPage } from "./WorkflowsPage";

beforeEach(() => {
  state.list.mockResolvedValue([
    {
      id: "wf-1",
      name: "Customer Support Triage",
      status: "deployed",
      nodes: [],
      edges: [],
      runs: 12,
      spend: "4.20",
    },
  ]);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  state.readOnly = false;
});

describe("WorkflowsPage", () => {
  it("keeps the page-size control available after all workflows fit on one page", async () => {
    state.list.mockResolvedValue(Array.from({ length: 12 }, (_, i) => ({
      id: `wf-${i}`, name: `Workflow ${i}`, nodes: [], edges: [], status: "draft",
    })));
    render(<WorkflowsPage />);
    const size = await screen.findByLabelText("workflows per page");
    fireEvent.change(size, { target: { value: "20" } });
    expect(screen.getByLabelText("workflows per page")).toBe(size);
    fireEvent.change(size, { target: { value: "5" } });
    expect(screen.getByText("Page 1 of 3")).toBeTruthy();
  });

  it("keeps row and grid pagination aligned and clamps a filtered page", async () => {
    state.list.mockResolvedValue(Array.from({ length: 12 }, (_, i) => ({
      id: `wf-${i}`, name: `Workflow ${i}`, nodes: [], edges: [], status: "draft",
    })));
    render(<WorkflowsPage />);
    await screen.findByLabelText("workflows per page");
    expect(screen.getByRole("link", { name: "Workflow 0" })).toBeTruthy();
    expect(screen.queryByText("Workflow 10")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(screen.getByRole("link", { name: "Workflow 10" })).toBeTruthy();
    expect(screen.queryByText("Workflow 0")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Grid" }));
    expect(screen.getByText("Workflow 10")).toBeTruthy();
    expect(screen.getByText("Page 2 of 2")).toBeTruthy();

    fireEvent.change(screen.getByPlaceholderText("Search workflows, tags…"), {
      target: { value: "Workflow 0" },
    });
    expect(screen.getByText("Workflow 0")).toBeTruthy();
    expect(screen.queryByText("Workflow 10")).toBeNull();
    expect(screen.queryByText("no workflows match")).toBeNull();
  });

  it("gives a phone the thin list, without the desktop chrome", async () => {
    state.readOnly = true;
    render(<WorkflowsPage />);
    expect(
      await screen.findByRole("link", {
        name: /Customer Support Triage, deployed/,
      }),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: /add credits/i })).toBeTruthy();
    expect(screen.queryByText(/Rows|Grid/)).toBeNull();
    expect(screen.queryByText(/your workspace/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /^Open$|^Zone/ })).toBeNull();
  });

  it("keeps desktop navigation and Usage actions available", async () => {
    render(<WorkflowsPage />);
    expect(await screen.findByText("Customer Support Triage")).toBeTruthy();
    expect(screen.getByText(/Rows/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Open" })).toBeNull();
    const link = screen.getByRole("link", { name: "Customer Support Triage" });
    expect(link.getAttribute("href")).toContain("/workflows/");
    expect(screen.getByText(/your workspace/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Usage" }));
    expect(state.push).toHaveBeenCalledWith("/usage?workflow=wf-1");
  });

  // The row name is an <a> over a div that also routes on click, so its
  // handler has to tell the two kinds of click apart.
  describe.each(["Rows", "Grid"])("the %s workflow link", (view) => {
    async function workflowLink() {
      render(<WorkflowsPage />);
      await screen.findByText("Customer Support Triage");
      if (view === "Grid") {
        fireEvent.click(screen.getByRole("button", { name: "Grid" }));
      }
      return screen.getByRole("link", { name: "Customer Support Triage" });
    }

    it("routes a plain click client-side instead of loading the page", async () => {
      const link = await workflowLink();
      // fireEvent returns false when the event was cancelled, which is what
      // preventDefault-then-router.push looks like from the outside.
      expect(fireEvent.click(link)).toBe(false);
      expect(state.push).toHaveBeenCalledWith(
        expect.stringContaining("/workflows/wf-1"),
      );
    });

    it("lets a cmd-click open a new tab rather than routing this one", async () => {
      const link = await workflowLink();
      // Not cancelled: the browser is left to honour the href, which is the
      // whole point of the name being a link. The handler used to
      // preventDefault every click, so a cmd-click navigated this tab.
      expect(fireEvent.click(link, { metaKey: true })).toBe(true);
      // And the row's own onClick must not route underneath it, or the reader
      // gets a new tab AND loses the one they were on.
      expect(state.push).not.toHaveBeenCalled();
    });

    it("treats ctrl and shift the same way", async () => {
      const link = await workflowLink();
      expect(fireEvent.click(link, { ctrlKey: true })).toBe(true);
      expect(fireEvent.click(link, { shiftKey: true })).toBe(true);
      expect(state.push).not.toHaveBeenCalled();
    });
  });
});
