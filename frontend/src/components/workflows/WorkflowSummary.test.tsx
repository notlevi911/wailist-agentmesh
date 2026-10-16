import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { RunPage, RunSummary, Workflow } from "@/lib/types";

// The summary is tested against a stubbed API. The top bar, the pull gesture
// and the sheet have their own tests, so they are reduced to what this screen
// hands them.
const api = vi.hoisted(() => {
  class RunsUnavailableError extends Error {}
  return {
    RunsUnavailableError,
    get: vi.fn(),
    run: vi.fn(),
    stop: vi.fn(),
    listForWorkflow: vi.fn(),
    runGet: vi.fn(),
  };
});

vi.mock("@/lib/api", () => ({
  RunsUnavailableError: api.RunsUnavailableError,
  // A scheduled workflow also shows its upcoming runs, which have their own
  // tests; here they only need to answer.
  UpcomingUnavailableError: class extends Error {},
  schedules: { upcoming: vi.fn(async () => []) },
  workflows: { get: api.get, run: api.run, stop: api.stop },
  runs: { listForWorkflow: api.listForWorkflow, get: api.runGet },
}));
vi.mock("@/components/Topbar", () => ({ Topbar: () => null }));
vi.mock("@/components/PullToRefresh", () => ({
  PullToRefresh: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));
vi.mock("@/components/runs/RunSheet", () => ({
  RunSheet: ({ run }: { run: RunSummary }) => (
    <div role="dialog">sheet for {run.id}</div>
  ),
}));

import { WorkflowSummary } from "./WorkflowSummary";
import { describeSchedule } from "@/lib/describeSchedule";

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: "wf-1",
    name: "Morning digest",
    status: "deployed",
    nodes: [
      { id: "t", type: "trigger", template: "manual", x: 0, y: 0 },
      { id: "p", type: "provider", x: 0, y: 0 },
    ],
    edges: [],
    ...overrides,
  };
}

const FINISHED: RunSummary = {
  id: "r-1",
  workflowId: "wf-1",
  workflowName: "Morning digest",
  triggeredBy: "schedule",
  status: "success",
  startedAt: new Date(Date.now() - 60_000).toISOString(),
  finishedAt: new Date(Date.now() - 30_000).toISOString(),
  spendUsdMicros: 21_000,
};

function page(runs: RunSummary[], nextCursor: string | null = null): RunPage {
  return { runs, nextCursor };
}

beforeEach(() => {
  api.get.mockReset().mockResolvedValue(workflow());
  api.listForWorkflow.mockReset().mockResolvedValue(page([FINISHED]));
  api.run.mockReset().mockResolvedValue({ runId: "r-2" });
  api.stop.mockReset().mockResolvedValue(undefined);
  api.runGet.mockReset();
  // A poll asks runs.get about a run the list has not picked up yet, so it
  // needs a promise back. Still running: the pending row stays as it is.
  api.runGet.mockResolvedValue({
    run: {
      id: "r-2",
      workflowId: "wf-1",
      triggeredBy: "manual",
      status: "running",
      startedAt: new Date().toISOString(),
    },
    logs: [],
    deadLetters: [],
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("WorkflowSummary", () => {
  it("shows the new run straight away after Run, and offers Stop", async () => {
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Morning digest");
    await screen.findByText("Succeeded");

    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    expect(await screen.findByText("Running")).toBeTruthy();
    expect(api.run).toHaveBeenCalledWith("wf-1");
    expect(await screen.findByRole("button", { name: "Stop" })).toBeTruthy();
    // Once for the first load, once after the run started.
    expect(api.listForWorkflow).toHaveBeenCalledTimes(2);
  });

  it("does not offer Run for a workflow that starts from a chat message", async () => {
    api.get.mockResolvedValue(
      workflow({
        nodes: [
          { id: "t", type: "trigger", template: "chat", x: 0, y: 0 },
          { id: "p", type: "provider", x: 0, y: 0 },
        ],
      }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);

    const button = await screen.findByRole("button", { name: "Run" });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(true),
    );
    expect(
      screen.getByText(/run it from the AgentMesh desktop app/),
    ).toBeTruthy();
  });

  // The cron is for the scheduler. A person gets it in words, in their time.
  it("says its schedule in words, not as a cron expression", async () => {
    api.get.mockResolvedValue(workflow({ scheduleCron: "0 7 * * 1-5" }));
    const { container } = render(<WorkflowSummary workflowId="wf-1" />);
    const line = await screen.findByText(describeSchedule("0 7 * * 1-5"));
    expect(line.textContent).toMatch(/^Every weekday at /);
    expect(line.getAttribute("title")).toBe("0 7 * * 1-5");
    expect(container.textContent).not.toContain("0 7 * * 1-5");
  });

  it("explains a workflow that is not deployed", async () => {
    api.get.mockResolvedValue(workflow({ status: "draft" }));
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(
      await screen.findByText(
        "Not deployed yet: finish and deploy it in the AgentMesh desktop app",
      ),
    ).toBeTruthy();
  });

  it("labels a paused workflow and says where to resume it", async () => {
    api.get.mockResolvedValue(workflow({ status: "paused" }));
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(
      await screen.findByText(
        "Paused: resume it in the AgentMesh desktop app",
      ),
    ).toBeTruthy();
    expect(screen.getByText("Paused")).toBeTruthy();
  });

  it("says so when the server has no run history", async () => {
    api.listForWorkflow.mockRejectedValue(new api.RunsUnavailableError());
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(
      await screen.findByText(
        "Run history is not available on this server yet.",
      ),
    ).toBeTruthy();
  });

  it("opens a run's sheet when its row is tapped", async () => {
    render(<WorkflowSummary workflowId="wf-1" />);
    fireEvent.click((await screen.findByText("Succeeded")).closest("button")!);
    expect(screen.getByRole("dialog").textContent).toBe("sheet for r-1");
  });

  it("appends older runs without losing the ones already shown", async () => {
    const older: RunSummary = {
      ...FINISHED,
      id: "r-0",
      status: "failed",
      startedAt: new Date(Date.now() - 120_000).toISOString(),
    };
    api.listForWorkflow
      .mockResolvedValueOnce(page([FINISHED], "c1"))
      .mockResolvedValueOnce(page([older]));
    render(<WorkflowSummary workflowId="wf-1" />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Show older runs" }),
    );

    expect(await screen.findByText("Failed")).toBeTruthy();
    expect(screen.getByText("Succeeded")).toBeTruthy();
    expect(api.listForWorkflow).toHaveBeenLastCalledWith("wf-1", {
      cursor: "c1",
      limit: 20,
    });
    expect(
      screen.queryByRole("button", { name: "Show older runs" }),
    ).toBeNull();
  });

  it("picks up a run started elsewhere without a pull", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      api.listForWorkflow
        .mockResolvedValueOnce(page([FINISHED]))
        .mockResolvedValue(
          page([
            {
              ...FINISHED,
              id: "r-2",
              triggeredBy: "manual",
              status: "running",
              startedAt: new Date().toISOString(),
              finishedAt: undefined,
            },
            FINISHED,
          ]),
        );
      render(<WorkflowSummary workflowId="wf-1" />);
      await screen.findByText("Succeeded");

      await act(async () => {
        vi.advanceTimersByTime(10_000);
      });

      expect(await screen.findByText("Running")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("refreshes on coming back to the foreground", async () => {
    api.listForWorkflow
      .mockResolvedValueOnce(page([FINISHED]))
      .mockResolvedValue(
        page([{ ...FINISHED, id: "r-2", status: "failed" }, FINISHED]),
      );
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");

    document.dispatchEvent(new Event("visibilitychange"));

    expect(await screen.findByText("Failed")).toBeTruthy();
  });

  // The figures come with the workflow, which used to be read once, so they
  // stayed at their pre-run values while the run list moved on.
  // Every read of the workflow is numbered. The read triggered when a run
  // starts can be slow and land after the one triggered when it finishes; it
  // must not put the older figures back.
  it("keeps the newest figures when an older workflow read lands last", async () => {
    let settleStart!: (wf: Workflow) => void;
    api.get
      .mockResolvedValueOnce(workflow({ totalRuns: 1 }))
      .mockReturnValueOnce(
        new Promise<Workflow>((r) => {
          settleStart = r;
        }),
      )
      .mockResolvedValue(workflow({ totalRuns: 3 }));
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");
    const fact = (label: string) =>
      screen.queryByText(label)?.nextElementSibling?.textContent;

    const started = {
      ...FINISHED,
      id: "r-2",
      status: "running" as const,
      startedAt: new Date().toISOString(),
      finishedAt: undefined,
    };
    api.listForWorkflow.mockResolvedValue(page([started, FINISHED]));
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));

    api.listForWorkflow.mockResolvedValue(
      page([{ ...started, status: "success" }, FINISHED]),
    );
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => expect(fact("Total runs")).toBe("3"));

    await act(async () => {
      settleStart(workflow({ totalRuns: 2 }));
    });
    expect(fact("Total runs")).toBe("3");
  });

  // The quiet re-read a run starting triggers has no error handler: failing
  // keeps what is shown. It used to count as "newer" all the same, so a first
  // load still in flight was thrown away when it landed after that failure,
  // and the screen stayed on its skeleton. Only a read that has succeeded
  // outranks an older one.
  it("still shows the first load when a newer quiet read fails first", async () => {
    let settleFirst!: (wf: Workflow) => void;
    let settleHistory!: (runs: RunPage) => void;
    api.listForWorkflow.mockReturnValueOnce(
      new Promise<RunPage>((resolve) => {
        settleHistory = resolve;
      }),
    );
    api.get
      .mockReturnValueOnce(
        new Promise<Workflow>((r) => {
          settleFirst = r;
        }),
      )
      .mockRejectedValueOnce(new Error("offline"));
    render(<WorkflowSummary workflowId="wf-1" />);
    await waitFor(() => expect(api.listForWorkflow).toHaveBeenCalledTimes(1));

    // Establish the initial activity before simulating a newly started run.
    await act(async () => {
      settleHistory(page([FINISHED]));
    });

    const started = {
      ...FINISHED,
      id: "r-2",
      status: "running" as const,
      startedAt: new Date().toISOString(),
      finishedAt: undefined,
    };
    api.listForWorkflow.mockResolvedValue(page([started, FINISHED]));
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));
    await act(async () => {});

    await act(async () => {
      settleFirst(workflow({ totalRuns: 1 }));
    });
    const fact = (label: string) =>
      screen.queryByText(label)?.nextElementSibling?.textContent;
    await waitFor(() => expect(fact("Total runs")).toBe("1"));
  });

  it("re-reads its figures when a run starts and when it finishes", async () => {
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");
    expect(api.get).toHaveBeenCalledTimes(1);

    api.get.mockResolvedValue(workflow({ totalRuns: 2 }));
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    await screen.findByText("Running");
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));

    api.get.mockResolvedValue(workflow({ totalRuns: 2, runs: 2 }));
    api.listForWorkflow.mockResolvedValue(
      page([{ ...FINISHED, id: "r-2" }, FINISHED]),
    );
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(3));
    const fact = (label: string) =>
      screen.getByText(label).nextElementSibling?.textContent;
    await waitFor(() => expect(fact("Total runs")).toBe("2"));
  });

  it("describes itself from its graph until a description is written", async () => {
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(await screen.findByText(/^Runs when started\./)).toBeTruthy();
    expect(screen.getByText("Summarised from its steps.")).toBeTruthy();
  });

  it("shows a written description as it is", async () => {
    api.get.mockResolvedValue(
      workflow({ description: "Sorts support email." }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(await screen.findByText("Sorts support email.")).toBeTruthy();
    expect(screen.queryByText("Summarised from its steps.")).toBeNull();
  });

  // GetWorkflow leaves the 30-day pair at zero when its aggregation fails,
  // exactly as it does for a workflow that had no runs and no spend. Printed
  // as "0" and "$0.00" that is a figure the reader has no reason to doubt.
  it("shows dashes for the 30-day figures the server could not total", async () => {
    api.get.mockResolvedValue(
      workflow({
        statsUnavailable: true,
        totalRuns: 7,
        runs: 0,
        spend: undefined,
      }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");
    const fact = (label: string) =>
      screen.getByText(label).nextElementSibling?.textContent;

    expect(fact("Runs · 30 days")).toBe("-");
    expect(fact("Spent · 30 days")).toBe("-");
    // totalRuns has its own nullable field and was answered, so it stands.
    expect(fact("Total runs")).toBe("7");
  });

  it("shows its run figures", async () => {
    api.get.mockResolvedValue(
      workflow({ totalRuns: 1842, runs: 38, spend: "1.482" }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    const fact = async (label: string) =>
      (await screen.findByText(label)).nextElementSibling?.textContent;
    expect(await fact("Total runs")).toBe((1842).toLocaleString());
    expect(await fact("Runs · 30 days")).toBe("38");
    expect(await fact("Spent · 30 days")).toBe("$1.48");
    expect(await fact("Next run")).toBe("-");
  });

  it("lists its agents with the model and tools each uses", async () => {
    api.get.mockResolvedValue(
      workflow({
        nodes: [
          { id: "t", type: "trigger", template: "manual", x: 0, y: 0 },
          { id: "a", type: "agent", name: "Triage agent", x: 0, y: 0 },
          { id: "p", type: "provider", model: "gemini-2.5-flash", x: 0, y: 0 },
          { id: "h", type: "tool", template: "http", x: 0, y: 0 },
        ],
        edges: [
          { id: "e1", from: "p", to: "a", kind: "attach", toPort: "model" },
          { id: "e2", from: "h", to: "a", kind: "attach", toPort: "tools" },
        ],
      }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    expect(await screen.findByText("Triage agent")).toBeTruthy();
    expect(screen.getByText("gemini-2.5-flash · 1 tool")).toBeTruthy();
  });

  it("shows status as a word beside a dot, not a pill", async () => {
    render(<WorkflowSummary workflowId="wf-1" />);
    const status = await screen.findByText("Deployed");
    expect(status.className).toBe("wfd-status");
  });

  // Tapping Run used to change a button and add a row; nothing said what the
  // run was doing. The dock follows it node by node.
  it("shows a run's progress at the bottom once it starts", async () => {
    api.get.mockResolvedValue(
      workflow({
        nodes: [
          { id: "t", type: "trigger", template: "manual", x: 0, y: 0 },
          { id: "a", type: "agent", name: "Triage Agent", x: 0, y: 0 },
          { id: "p", type: "provider", name: "Anthropic", x: 0, y: 0 },
        ],
        edges: [
          { id: "e1", from: "t", to: "a", kind: "flow" },
          { id: "e2", from: "p", to: "a", kind: "attach", toPort: "model" },
        ],
      }),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");
    expect(document.querySelector(".run-dock")).toBeNull();

    // The run's detail answers with the trigger already done.
    api.runGet.mockResolvedValue({
      run: {
        id: "r-2",
        workflowId: "wf-1",
        triggeredBy: "manual",
        status: "running",
        startedAt: new Date().toISOString(),
      },
      logs: [{ nodeId: "t", status: "success", durationMs: 20 }],
      deadLetters: [],
    });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    // Two milestones: the trigger and the agent. The provider hangs off the
    // agent and is not a step.
    expect(await screen.findByText("1/2")).toBeTruthy();
    // The name is in the Agents section too, so this asks the dock itself.
    const dock = document.querySelector(".run-dock")!;
    expect(dock.textContent).toContain("Triage Agent");
  });

  // Two rapid activations, before React has committed `acting`, called
  // workflows.run twice and billed for two runs.
  it("starts one run however fast Run is pressed twice", async () => {
    let start!: (v: { runId: string }) => void;
    api.run.mockReturnValueOnce(
      new Promise<{ runId: string }>((r) => (start = r)),
    );
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");

    const button = screen.getByRole("button", { name: "Run" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(api.run).toHaveBeenCalledTimes(1);

    await act(async () => {
      start({ runId: "r-2" });
    });
    expect(api.run).toHaveBeenCalledTimes(1);
  });

  it("starts one run however fast the dock's Run again is pressed", async () => {
    api.listForWorkflow.mockResolvedValue(
      page([
        { ...FINISHED, id: "r-2", status: "failed", finishedAt: undefined },
      ]),
    );
    api.runGet.mockResolvedValue({
      run: {
        id: "r-2",
        workflowId: "wf-1",
        triggeredBy: "manual",
        status: "failed",
        startedAt: new Date().toISOString(),
      },
      logs: [{ nodeId: "t", status: "failed" }],
      deadLetters: [],
    });
    let start!: (v: { runId: string }) => void;
    api.run
      .mockResolvedValueOnce({ runId: "r-2" })
      .mockReturnValueOnce(new Promise<{ runId: string }>((r) => (start = r)));
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByRole("button", { name: "Run" });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    const again = await screen.findByRole("button", { name: "Run again" });
    fireEvent.click(again);
    fireEvent.click(again);
    expect(api.run).toHaveBeenCalledTimes(2);

    await act(async () => {
      start({ runId: "r-3" });
    });
    expect(api.run).toHaveBeenCalledTimes(2);
  });

  const runDetail = (status: string) => ({
    run: {
      id: "r-2",
      workflowId: "wf-1",
      triggeredBy: "manual",
      status,
      startedAt: new Date().toISOString(),
    },
    logs: [{ nodeId: "t", status: status === "failed" ? "failed" : "running" }],
    deadLetters: [],
  });

  // useRunDetail stops polling a terminal run, so a Resume under the same id
  // left the dock on "failed" for good while the list moved back to running.
  // The list saying "running" now restarts that polling.
  it("follows the list back to running after a resume", async () => {
    api.runGet.mockResolvedValue(runDetail("failed"));
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByText("Succeeded");
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    await screen.findByRole("button", { name: "Run again" });
    const readsBefore = api.runGet.mock.calls.length;

    // The list picks the same run up again, resumed -- and so, now, does the
    // run's own detail, which is what a real Resume writes.
    api.listForWorkflow.mockResolvedValue(
      page([
        { ...FINISHED, id: "r-2", status: "running", finishedAt: undefined },
      ]),
    );
    api.runGet.mockResolvedValue(runDetail("running"));
    document.dispatchEvent(new Event("visibilitychange"));

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Run again" })).toBeNull(),
    );
    // It did not merely defer to the list: it went and asked again.
    expect(api.runGet.mock.calls.length).toBeGreaterThan(readsBefore);
    expect(
      document.querySelector(".run-dock")!.getAttribute("data-state"),
    ).toBe("running");
  });

  // The mirror image of that case. The list says running, the detail then
  // reads terminal, and the next list poll fails. useRunDetail stops after a
  // terminal answer, so a dock that always took the list row sat on a stale
  // "running" for good, with no Details and no Dismiss.
  it("follows a terminal detail once the list stops answering", async () => {
    vi.useFakeTimers();
    try {
      await terminalDetailOutlastsTheList();
    } finally {
      vi.useRealTimers();
    }
  });

  async function terminalDetailOutlastsTheList() {
    api.runGet.mockResolvedValue(runDetail("running"));
    render(<WorkflowSummary workflowId="wf-1" />);
    await act(async () => {});
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    await act(async () => {});

    // The server's own row now carries the run, still going.
    api.listForWorkflow.mockResolvedValue(
      page([
        { ...FINISHED, id: "r-2", status: "running", finishedAt: undefined },
      ]),
    );
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => {});
    expect(
      document.querySelector(".run-dock")!.getAttribute("data-state"),
    ).toBe("running");

    // The run then ends, and the list goes quiet, so the detail's own poll
    // is the last thing to answer about this run.
    api.runGet.mockResolvedValue(runDetail("failed"));
    api.listForWorkflow.mockRejectedValue(new Error("offline"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });

    expect(
      document.querySelector(".run-dock")!.getAttribute("data-state"),
    ).toBe("failed");
    expect(screen.getByRole("button", { name: "Run again" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeTruthy();
  }

  // Date.now() has millisecond resolution, so two answers landing back to
  // back read the same value -- 10,000 ties in 10,000 pairs, measured. While
  // freshness was two wall-clock stamps compared with `>`, a tie always went
  // to the list, and a terminal detail that answered second in the same
  // millisecond stayed suppressed once list polling failed.
  it("prefers the answer that landed second even on one clock tick", async () => {
    vi.useFakeTimers();
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    try {
      await terminalDetailOutlastsTheList();
    } finally {
      clock.mockRestore();
      vi.useRealTimers();
    }
  });

  // A GET /runs/{id} that keeps failing left a dock assuming "running", with
  // no headline it could stand behind and no way to dismiss it.
  it("offers a way out when the run's detail cannot be read", async () => {
    api.listForWorkflow.mockResolvedValue(page([]));
    api.runGet.mockRejectedValue(new Error("offline"));
    render(<WorkflowSummary workflowId="wf-1" />);
    await screen.findByRole("button", { name: "Run" });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    expect(await screen.findByText("Cannot read this run")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(document.querySelector(".run-dock")).toBeNull());
  });
});
