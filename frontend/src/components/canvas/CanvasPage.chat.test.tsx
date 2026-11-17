import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Workflow } from "@/lib/types";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
  estimate: vi.fn(),
  send: vi.fn(),
  refreshBalance: vi.fn(),
  router: { push: vi.fn(), replace: vi.fn() },
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
vi.mock("@/hooks/useIsHandheld", () => ({ useIsHandheld: () => false }));
vi.mock("@/hooks/useReadOnly", () => ({ useReadOnly: () => false }));
vi.mock("./CanvasGraph", () => ({ CanvasGraph: () => null }));
vi.mock("./PalettePanel", () => ({ PalettePanel: () => null }));
vi.mock("./Inspector", () => ({ Inspector: () => <div>Inspector contents</div> }));
vi.mock("./ConsolePanel", () => ({ ConsolePanel: () => null }));
vi.mock("@/components/workflows/ShareModal", () => ({ ShareModal: () => null }));
vi.mock("./chat/useChatConsole", () => ({
  useChatConsole: () => ({
    logs: [], elapsed: 0, done: false, deadLetters: [], leaseId: null, costs: null,
    session: { messages: [], hydrated: true }, busy: false, handleSend: mocks.send,
  }),
}));

import { CanvasPage } from "./CanvasPage";

const workflow: Workflow = {
  id: "composer-workflow", name: "Draft persistence", status: "draft",
  nodes: [{ id: "trigger", type: "trigger", template: "chat", x: 0, y: 0 }],
  edges: [],
};
const draft = "Summarize monthly expenses without sending this draft.";
const observers = new Set<() => void>();
let rowWidth = 1200;

function composer() {
  return screen.getByPlaceholderText("Ask something…");
}

async function resize(width: number) {
  expect(observers.size).toBeGreaterThan(0);
  await act(async () => {
    rowWidth = width;
    for (const notify of observers) notify();
  });
}

async function openDraft() {
  await act(async () => { render(<CanvasPage workflowId={workflow.id} />); });
  expect(screen.getByRole("tab", { name: "Build" })).toHaveProperty("ariaSelected", "true");
  fireEvent.change(composer(), { target: { value: draft } });
  expect(composer()).toHaveProperty("value", draft);
}

beforeEach(() => {
  vi.clearAllMocks();
  rowWidth = 1200;
  observers.clear();
  localStorage.clear();
  mocks.get.mockImplementation(async (id: string) => ({ ...workflow, id }));
  mocks.update.mockImplementation(async (_id: string, saved: Workflow) => saved);
  mocks.estimate.mockResolvedValue(null);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({
    width: rowWidth, height: 800, x: 0, y: 0, left: 0, right: rowWidth,
    top: 0, bottom: 800, toJSON: () => ({}),
  }));
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: false, addEventListener() {}, removeEventListener() {},
  })));
  vi.stubGlobal("ResizeObserver", class {
    private notify: () => void;
    constructor(notify: () => void) { this.notify = notify; }
    observe() { observers.add(this.notify); }
    disconnect() { observers.delete(this.notify); }
  });
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true, value: vi.fn(),
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("CanvasPage composer persistence", () => {
  it("keeps an unsent draft through manual collapse and expansion", async () => {
    await openDraft();
    fireEvent.click(screen.getByRole("button", { name: "Collapse the chat panel" }));
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
    expect(screen.queryByRole("separator", { name: "Resize chat panel" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Expand the chat panel" }));
    expect(composer()).toHaveProperty("value", draft);
    expect(mocks.send).not.toHaveBeenCalled();
  });

  it("keeps an unsent draft when narrowing hides chat and widening restores it", async () => {
    await openDraft();
    await resize(500);
    expect(screen.getByRole("button", { name: "Expand the chat panel" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
    await resize(1200);
    expect(composer()).toHaveProperty("value", draft);
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(mocks.send).not.toHaveBeenCalled();
  });

  it("keeps an unsent draft when opening the library replaces the visible chat panel", async () => {
    rowWidth = 700;
    await openDraft();
    fireEvent.click(screen.getByRole("button", { name: "Expand the library" }));
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Expand the chat panel" }));
    expect(composer()).toHaveProperty("value", draft);
    expect(mocks.send).not.toHaveBeenCalled();
  });

  it("preserves the draft across a chat and inspector tab switch", async () => {
    await openDraft();
    fireEvent.click(screen.getByRole("tab", { name: "Inspect" }));
    expect(screen.queryByRole("button", { name: "Send message" })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Chat" }));
    expect(composer()).toHaveProperty("value", draft);
  });

  it("clears a submitted draft once without restoring it after collapse", async () => {
    await openDraft();
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith(draft);
    expect(composer()).toHaveProperty("value", "");
    fireEvent.click(screen.getByRole("button", { name: "Collapse the chat panel" }));
    fireEvent.click(screen.getByRole("button", { name: "Expand the chat panel" }));
    expect(composer()).toHaveProperty("value", "");
    expect(mocks.send).toHaveBeenCalledTimes(1);
  });

  it("starts with an empty draft when navigating to a different workflow", async () => {
    const view = render(<CanvasPage key={workflow.id} workflowId={workflow.id} />);
    await act(async () => {});
    fireEvent.change(composer(), { target: { value: draft } });
    await act(async () => {
      view.rerender(<CanvasPage key="next-workflow" workflowId="next-workflow" />);
    });
    expect(composer()).toHaveProperty("value", "");
    expect(mocks.get).toHaveBeenLastCalledWith("next-workflow");
    expect(mocks.send).not.toHaveBeenCalled();
  });
});
