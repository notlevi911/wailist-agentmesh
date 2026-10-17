import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Dispatch, SetStateAction } from "react";
import type { Workflow } from "@/lib/types";

const mocks = vi.hoisted(() => ({
  get: vi.fn(), update: vi.fn(), estimate: vi.fn(), refreshBalance: vi.fn(),
  router: { push: vi.fn(), replace: vi.fn() },
  handheld: false, readOnly: false,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => mocks.router,
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  workflows: { get: mocks.get, update: mocks.update, estimate: mocks.estimate },
  runs: {},
}));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({ balanceUSD: 10, balanceKnown: true, refreshBalance: mocks.refreshBalance }),
  refreshBalance: mocks.refreshBalance,
}));
vi.mock("@/hooks/useIsCompact", () => ({ useIsCompact: () => false }));
vi.mock("@/hooks/useIsHandheld", () => ({ useIsHandheld: () => mocks.handheld }));
vi.mock("@/hooks/useReadOnly", () => ({ useReadOnly: () => mocks.readOnly }));
vi.mock("./PalettePanel", () => ({ PalettePanel: () => null }));
vi.mock("./Inspector", () => ({ Inspector: () => null }));
vi.mock("./ConsolePanel", () => ({ ConsolePanel: () => null }));
vi.mock("./ResizeHandle", () => ({ ResizeHandle: () => null }));
vi.mock("./chat/ChatRail", () => ({ ChatRail: () => null }));
vi.mock("@/components/workflows/ShareModal", () => ({ ShareModal: () => null }));
vi.mock("./chat/useChatConsole", () => ({
  useChatConsole: () => ({
    logs: [], elapsed: 0, done: false, deadLetters: [], session: {}, busy: false,
  }),
}));
vi.mock("./CanvasGraph", () => ({
  CanvasGraph: ({ workflow, setWorkflow }: {
    workflow: Workflow;
    setWorkflow: Dispatch<SetStateAction<Workflow>>;
  }) => (
    <>
      <button onClick={() => setWorkflow((wf) => ({
        ...wf,
        nodes: [...wf.nodes, { id: "pending-node", type: "agent", x: 100, y: 100 }],
        edges: [...wf.edges, { id: "pending-edge", from: "trigger", to: "pending-node", kind: "flow" }],
      }))}>Edit graph</button>
      <output data-testid="graph">{JSON.stringify(workflow)}</output>
    </>
  ),
}));

import { CanvasPage } from "./CanvasPage";

const workflow: Workflow = {
  id: "resize-workflow", name: "Breakpoint draft", status: "draft",
  nodes: [{ id: "trigger", type: "trigger", x: 0, y: 0 }], edges: [],
};
const media = { matches: false, listeners: new Set<() => void>() };

async function resize(width: number) {
  const matches = width <= 768;
  expect(media.listeners.size).toBeGreaterThan(0);
  await act(async () => {
    if (media.matches !== matches) {
      media.matches = matches;
      for (const listener of media.listeners) listener();
    }
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers();
  mocks.handheld = false;
  mocks.readOnly = false;
  media.matches = false;
  media.listeners.clear();
  mocks.get.mockImplementation(async (id: string) => ({ ...workflow, id }));
  mocks.update.mockImplementation(async (_id: string, saved: Workflow) => saved);
  mocks.estimate.mockResolvedValue(null);
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    get matches() { return media.matches; },
    addEventListener: (_type: string, listener: () => void) => media.listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => media.listeners.delete(listener),
  })));
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("CanvasPage breakpoint save boundary", () => {
  for (const crossing of [
    { name: "wide to narrow", initiallyNarrow: false, width: 700 },
    { name: "narrow to wide", initiallyNarrow: true, width: 1000 },
  ]) {
    it(`preserves and saves pending graph edits from ${crossing.name}`, async () => {
      media.matches = crossing.initiallyNarrow;
      await act(async () => { render(<CanvasPage workflowId={workflow.id} />); });
      const initialGets = mocks.get.mock.calls.length;
      fireEvent.click(screen.getByRole("button", { name: "Edit graph" }));
      const draft = screen.getByTestId("graph").textContent;
      expect(draft).toContain("pending-node");
      expect(draft).toContain("pending-edge");

      await act(async () => { await vi.advanceTimersByTimeAsync(500); });
      expect(mocks.update).not.toHaveBeenCalled();
      await resize(crossing.width);

      expect(screen.getByTestId("graph").textContent).toBe(draft);
      expect(mocks.get).toHaveBeenCalledTimes(initialGets);
      await act(async () => { await vi.advanceTimersByTimeAsync(999); });
      expect(mocks.update).not.toHaveBeenCalled();
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(mocks.update).toHaveBeenCalledExactlyOnceWith(workflow.id, expect.objectContaining({
        nodes: expect.arrayContaining([expect.objectContaining({ id: "pending-node" })]),
        edges: expect.arrayContaining([expect.objectContaining({ id: "pending-edge" })]),
      }));
      expect(screen.getByTestId("graph").textContent).toBe(draft);
    });
  }

  it("loads an initially narrow desktop without an editing wall or an initial save", async () => {
    media.matches = true;
    await act(async () => { render(<CanvasPage workflowId={workflow.id} />); });
    expect(screen.getByTestId("graph").textContent).toContain(workflow.id);
    expect(screen.queryByText("The editor needs a wider screen.")).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("does not autosave read-only state when the viewport changes", async () => {
    mocks.readOnly = true;
    mocks.handheld = true;
    await act(async () => { render(<CanvasPage workflowId={workflow.id} />); });
    fireEvent.click(screen.getByRole("button", { name: "Edit graph" }));
    await resize(700);
    expect(screen.queryByText("The editor needs a wider screen.")).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("keeps the narrow handheld editing wall and loads when that guard clears", async () => {
    mocks.handheld = true;
    media.matches = true;
    await act(async () => { render(<CanvasPage workflowId={workflow.id} />); });
    expect(screen.getByText("The editor needs a wider screen.")).toBeTruthy();
    expect(screen.queryByTestId("graph")).toBeNull();
    await resize(1000);
    expect(screen.getByTestId("graph").textContent).toContain(workflow.id);
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("loads the next workflow on a keyed route remount", async () => {
    const view = render(<CanvasPage key={workflow.id} workflowId={workflow.id} />);
    await act(async () => {});
    await act(async () => {
      view.rerender(<CanvasPage key="next-workflow" workflowId="next-workflow" />);
    });
    expect(screen.getByTestId("graph").textContent).toContain("next-workflow");
    expect(mocks.get).toHaveBeenLastCalledWith("next-workflow");
    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(mocks.update).not.toHaveBeenCalled();
  });
});
