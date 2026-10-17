import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Dispatch, SetStateAction } from "react";
import type { Workflow } from "@/lib/types";
import type { BuildProgress } from "./chat/buildProgress";

const mocks = vi.hoisted(() => ({
  get: vi.fn(), update: vi.fn(), build: vi.fn(), estimate: vi.fn(),
  stop: vi.fn(), waitForFinishedBuild: vi.fn(), refreshBalance: vi.fn(),
  router: { push: vi.fn(), replace: vi.fn() },
  onBuild: undefined as undefined | ((text: string, progress?: (p: BuildProgress) => void) => Promise<{ ok: boolean; reply?: string }>),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => mocks.router,
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  workflows: { get: mocks.get, update: mocks.update, build: mocks.build, estimate: mocks.estimate },
  runs: {},
}));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({ balanceUSD: 10, balanceKnown: true, refreshBalance: mocks.refreshBalance }),
  refreshBalance: mocks.refreshBalance,
}));
vi.mock("@/hooks/useIsCompact", () => ({ useIsCompact: () => false }));
vi.mock("@/hooks/useReadOnly", () => ({ useReadOnly: () => false }));
vi.mock("./PalettePanel", () => ({ PalettePanel: () => null }));
vi.mock("./Inspector", () => ({ Inspector: () => null }));
vi.mock("./ConsolePanel", () => ({ ConsolePanel: () => null }));
vi.mock("./ResizeHandle", () => ({ ResizeHandle: () => null }));
vi.mock("./chat/ChatRail", () => ({ ChatRail: () => null }));
vi.mock("@/components/workflows/ShareModal", () => ({ ShareModal: () => null }));
vi.mock("./chat/useChatConsole", () => ({
  useChatConsole: (options: { onBuildMessage: typeof mocks.onBuild }) => {
    mocks.onBuild = options.onBuildMessage;
    return { logs: [], elapsed: 0, done: false, deadLetters: [], session: {}, busy: false };
  },
}));
vi.mock("./chat/buildProgress", () => ({
  newBuildId: () => "build-test",
  startProgressPolling: () => ({ stop: mocks.stop }),
  waitForFinishedBuild: mocks.waitForFinishedBuild,
}));
vi.mock("./CanvasGraph", () => ({
  CanvasGraph: ({ workflow, setWorkflow }: { workflow: Workflow; setWorkflow: Dispatch<SetStateAction<Workflow>> }) => (
    <>
      <button onClick={() => setWorkflow((wf) => ({
        ...wf,
        nodes: [...wf.nodes, { id: "unsaved", type: "agent", x: 100, y: 100, name: "Unsaved agent" }],
        edges: [...wf.edges, { id: "unsaved-edge", from: "trigger", to: "unsaved", kind: "flow" }],
      }))}>Edit graph</button>
      <output data-testid="graph">{JSON.stringify({ nodes: workflow.nodes, edges: workflow.edges })}</output>
    </>
  ),
}));

import { CanvasPage } from "./CanvasPage";

const workflow: Workflow = {
  id: "workflow-test", name: "Save before Build", status: "draft",
  nodes: [{ id: "trigger", type: "trigger", x: 0, y: 0 }], edges: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.get.mockResolvedValue(workflow);
  mocks.estimate.mockResolvedValue(null);
  mocks.stop.mockResolvedValue(undefined);
  mocks.build.mockResolvedValue({ workflow, reply: "Built stale graph" });
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("CanvasPage Build save boundary", () => {
  it("stops progress and preserves unsaved nodes and edges when the pending save fails", async () => {
    mocks.update.mockRejectedValue(new Error("Save unavailable"));
    render(<CanvasPage workflowId={workflow.id} />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit graph" }));
    const localGraph = screen.getByTestId("graph").textContent;

    let result: { ok: boolean; reply?: string } | undefined;
    await act(async () => { result = await mocks.onBuild!("Add a step", vi.fn()); });

    expect(mocks.update).toHaveBeenCalledWith(workflow.id, expect.objectContaining({
      nodes: expect.arrayContaining([expect.objectContaining({ id: "unsaved" })]),
      edges: expect.arrayContaining([expect.objectContaining({ id: "unsaved-edge" })]),
    }));
    expect(mocks.build).not.toHaveBeenCalled();
    expect(mocks.stop).toHaveBeenCalledTimes(1);
    expect(mocks.waitForFinishedBuild).not.toHaveBeenCalled();
    expect(result).toEqual({ ok: false, reply: expect.stringContaining("failed to save") });
    expect(screen.getByTestId("graph").textContent).toBe(localGraph);
  });

  it("builds after the latest graph saves successfully", async () => {
    mocks.update.mockResolvedValue(workflow);
    render(<CanvasPage workflowId={workflow.id} />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit graph" }));

    await act(async () => {
      await expect(mocks.onBuild!("Add a step", vi.fn())).resolves.toEqual({ ok: true, reply: "Built stale graph" });
    });

    expect(mocks.update.mock.invocationCallOrder[0]).toBeLessThan(mocks.build.mock.invocationCallOrder[0]);
    expect(mocks.build).toHaveBeenCalledWith(workflow.id, "Add a step", "build-test");
    expect(mocks.stop).toHaveBeenCalledTimes(1);
  });
});
