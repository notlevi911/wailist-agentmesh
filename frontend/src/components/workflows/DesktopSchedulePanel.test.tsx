import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Pinned before anything builds a Date, as in cronCadence.test.ts. New York
// is UTC-5 in winter, so 14:00 UTC reads as 9:00 AM.
process.env.TZ = "America/New_York";

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { Workflow } from "@/lib/types";

const get = vi.fn<(id: string) => Promise<Workflow>>();
vi.mock("@/lib/api", () => ({ workflows: { get: (id: string) => get(id) } }));

import { DesktopSchedulePanel } from "./DesktopSchedulePanel";

// Anything shaped like a five-field cron. The panel must never show one:
// schedules are always said in words.
const CRON_LIKE = /(^|\s)[\d*/,-]+( [\d*/,-]+){4}(\s|$)/;

function wf(extra: Partial<Workflow> = {}): Workflow {
  return { id: "wf_1", name: "Morning brief", ...extra } as Workflow;
}

// Text as a person reads it: the panel keeps "9:00 AM" together with a
// non-breaking space.
const text = () => (document.body.textContent ?? "").replace(/ /g, " ");

function renderPanel() {
  const onSave = vi.fn<(cron: string) => Promise<void>>(async () => {});
  const onRemove = vi.fn<() => Promise<void>>(async () => {});
  const onClose = vi.fn();
  const view = render(
    <DesktopSchedulePanel
      workflowId="wf_1"
      workflowName="Morning brief"
      onSave={onSave}
      onRemove={onRemove}
      onClose={onClose}
    />,
  );
  return { onSave, onRemove, onClose, ...view };
}

beforeEach(() => {
  get.mockReset();
  // Only Date is faked, so waitFor's timers still run. Wednesday, Jan 14 2026,
  // noon in New York.
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(2026, 0, 14, 12, 0, 0));
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("DesktopSchedulePanel", () => {
  it("offers a daily 9 AM default and saves it, in words only", async () => {
    get.mockResolvedValue(wf());
    const { onSave } = renderPanel();

    await screen.findByRole("button", { name: "Save schedule" });
    expect(screen.queryByText("Active")).toBeNull();
    expect(text()).toContain("Every day at 9:00 AM");
    expect(text()).not.toMatch(CRON_LIKE);

    fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("0 14 * * *"));
  });

  it("reads an existing schedule back in words and only saves a change", async () => {
    get.mockResolvedValue(
      wf({
        scheduleCron: "0 14 * * 1",
        scheduleNextRunAt: "2026-01-19T14:00:00Z",
      }),
    );
    const { onSave } = renderPanel();

    await screen.findByText("Active");
    expect(text()).toContain("Every Monday at 9:00 AM");
    expect(text()).not.toMatch(CRON_LIKE);
    const update = screen.getByRole("button", { name: "Update" });
    expect(update).toHaveProperty("disabled", true);

    expect(text()).toContain("Next run on Mon, Jan 19 at 9:00 AM (in 5 days)");

    fireEvent.click(screen.getByRole("button", { name: "Tuesday" }));
    fireEvent.click(screen.getByRole("button", { name: "Monday" }));
    expect(text()).toContain("Every Tuesday at 9:00 AM");
    expect(update).toHaveProperty("disabled", false);
    fireEvent.click(update);
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("0 14 * * 2"));
  });

  it("ignores a next run the server left in the past", async () => {
    get.mockResolvedValue(
      wf({
        scheduleCron: "0 14 * * *",
        scheduleNextRunAt: "2026-01-01T14:00:00Z",
      }),
    );
    renderPanel();

    await screen.findByText("Active");
    expect(text()).toContain("Next run tomorrow at 9:00 AM");
  });

  it("says a schedule it can't show is custom, without printing it", async () => {
    get.mockResolvedValue(wf({ scheduleCron: "*/15 * * * *" }));
    renderPanel();

    await screen.findByText("This workflow has a custom schedule");
    expect(text()).not.toContain("*/15");
  });

  it("requires explicit replacement before saving a custom schedule", async () => {
    get.mockResolvedValue(wf({ scheduleCron: "0 14 * * 1-5" }));
    const { onSave } = renderPanel();
    const update = await screen.findByRole("button", { name: "Update" });
    expect(update).toHaveProperty("disabled", true);
    fireEvent.click(update);
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("Time")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Replace schedule" }));
    fireEvent.click(update);
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
  });

  it.each([29, 30, 31])("does not reinterpret stored day %i during February", async (day) => {
    vi.setSystemTime(new Date(2026, 1, 15, 12));
    get.mockResolvedValue(wf({ scheduleCron: `0 14 ${day} * *` }));
    const { onSave, onRemove } = renderPanel();
    const update = await screen.findByRole("button", { name: "Update" });

    expect(update).toHaveProperty("disabled", true);
    expect(screen.getByText("This workflow has a custom schedule")).toBeTruthy();
    expect(screen.queryByLabelText("Time")).toBeNull();
    fireEvent.click(update);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(onRemove).not.toHaveBeenCalled();
  });

  it("saves multiple selected days and restores them on a fresh read", async () => {
    get.mockResolvedValue(wf());
    const { onSave, unmount } = renderPanel();
    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Weekly" }));
    fireEvent.click(screen.getByRole("button", { name: "Wednesday" }));
    fireEvent.click(screen.getByRole("button", { name: "Friday" }));
    expect(text()).toContain("Every Monday, Wednesday, and Friday at 9:00 AM");
    fireEvent.click(screen.getByRole("button", { name: "Save schedule" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("0 14 * * 1,3,5"));
    unmount();

    get.mockResolvedValue(wf({ scheduleCron: onSave.mock.calls[0][0] }));
    const reloaded = renderPanel();
    await screen.findByText("Active");
    for (const name of ["Monday", "Wednesday", "Friday"]) {
      expect(screen.getByRole("button", { name }).getAttribute("aria-pressed")).toBe("true");
    }
    expect(screen.getByRole("button", { name: "Tuesday" }).getAttribute("aria-pressed")).toBe("false");
    expect(screen.getByRole("button", { name: "Update" })).toHaveProperty("disabled", true);
    expect(reloaded.onSave).not.toHaveBeenCalled();
  });

  it("keeps one selected day and cancels an edited weekly schedule without saving", async () => {
    get.mockResolvedValue(wf({ scheduleCron: "0 14 * * 1,5" }));
    const { onSave, onRemove, onClose } = renderPanel();
    await screen.findByText("Active");
    fireEvent.click(screen.getByRole("button", { name: "Friday" }));
    fireEvent.click(screen.getByRole("button", { name: "Monday" }));
    expect(screen.getByRole("button", { name: "Monday" }).getAttribute("aria-pressed")).toBe("true");
    expect(text()).toContain("Every Monday at 9:00 AM");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onSave).not.toHaveBeenCalled();
    expect(onRemove).not.toHaveBeenCalled();
  });

  it.each(["save", "remove"])("masks a cron rejection on %s", async (action) => {
    get.mockResolvedValue(wf(action === "remove" ? { scheduleCron: "0 14 * * *" } : {}));
    const { onSave, onRemove } = renderPanel();
    onSave.mockRejectedValue(new Error("cron 0 14 * * * rejected"));
    onRemove.mockRejectedValue(new Error("cron storage failure"));
    if (action === "save") {
      fireEvent.click(await screen.findByRole("button", { name: "Save schedule" }));
    } else {
      fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
      fireEvent.click(screen.getByRole("button", { name: "Confirm remove" }));
    }
    const message = action === "save" ? "The schedule couldn't be saved. Please try again." : "The schedule couldn't be removed. Please try again.";
    await screen.findByText(message);
    expect(screen.getAllByText(message)).toHaveLength(1);
    expect(text()).not.toContain("cron");
  });

  it("flags a monthly time that can't repeat before Save", async () => {
    get.mockResolvedValue(wf());
    renderPanel();

    // Radix tabs switch on mouse down.
    fireEvent.mouseDown(await screen.findByRole("tab", { name: "Monthly" }));
    fireEvent.click(screen.getByRole("radio", { name: "The 28th" }));
    fireEvent.change(screen.getByLabelText("Time"), {
      target: { value: "21:00" },
    });
    // 9 PM on the 28th in New York is the 29th in UTC, which February lacks.
    expect(
      await screen.findByText("This time can't repeat monthly"),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Save schedule" }),
    ).toHaveProperty("disabled", true);
  });

  it("asks twice before removing a schedule", async () => {
    get.mockResolvedValue(wf({ scheduleCron: "0 14 * * *" }));
    const { onRemove } = renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    expect(onRemove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm remove" }));
    await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(1));
  });

  it("never shows a form it couldn't load, and retries", async () => {
    get.mockRejectedValueOnce(new Error("network down"));
    get.mockResolvedValueOnce(wf());
    renderPanel();

    await screen.findByText("Couldn't load the schedule");
    expect(screen.queryByRole("button", { name: "Save schedule" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("button", { name: "Save schedule" });
    expect(get).toHaveBeenCalledTimes(2);
  });
});
