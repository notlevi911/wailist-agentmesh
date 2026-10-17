import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook } from "@testing-library/react";
import { useCloseOnBack, useSheetHistory } from "./useCloseOnBack";

const device = vi.hoisted(() => ({ handheld: false, native: false }));
vi.mock("./useIsHandheld", () => ({
  useIsHandheld: () => device.handheld,
}));
vi.mock("@/lib/nativeAuth", () => ({
  get IS_NATIVE() {
    return device.native;
  },
}));

beforeEach(() => {
  device.handheld = false;
  device.native = false;
  window.history.replaceState({ page: 1 }, "", "/workflows");
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("useCloseOnBack on desktop", () => {
  it("does not consume another history marker on desktop", () => {
    window.history.replaceState({ page: 1, __amSheet: true }, "");
    const replace = vi.spyOn(window.history, "replaceState");
    const { result } = renderHook(() => useSheetHistory(() => {}));
    expect(result.current.consumeHistoryEntry()).toBe(false);
    expect(replace).not.toHaveBeenCalled();
  });

  it.each(["dismiss", "deactivate", "unmount"])(
    "does not change history on open or %s",
    (closeMethod) => {
      const onClose = vi.fn();
      const push = vi.spyOn(window.history, "pushState");
      const replace = vi.spyOn(window.history, "replaceState");
      const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
      const { result, rerender, unmount } = renderHook(
        ({ active }) => useCloseOnBack(onClose, active),
        { initialProps: { active: false } },
      );

      rerender({ active: true });
      if (closeMethod === "dismiss") result.current();
      if (closeMethod === "deactivate") rerender({ active: false });
      unmount();
      vi.runAllTimers();

      expect(push).not.toHaveBeenCalled();
      expect(replace).not.toHaveBeenCalled();
      expect(back).not.toHaveBeenCalled();
      expect(window.history.state).toEqual({ page: 1 });
      expect(onClose).toHaveBeenCalledTimes(closeMethod === "dismiss" ? 1 : 0);
    },
  );

  it("leaves browser Back navigation to the router", () => {
    const onClose = vi.fn();
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const { unmount } = renderHook(() => useCloseOnBack(onClose));

    window.dispatchEvent(new PopStateEvent("popstate"));
    unmount();
    vi.runAllTimers();

    expect(onClose).not.toHaveBeenCalled();
    expect(back).not.toHaveBeenCalled();
  });
});

describe.each(["handheld", "native"])("useCloseOnBack on %s", (platform) => {
  beforeEach(() => {
    device.handheld = platform === "handheld";
    device.native = platform === "native";
  });

  it("consumes its entry once for navigation without a delayed Back", () => {
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const { result, rerender } = renderHook(
      ({ active }) => useSheetHistory(() => {}, active),
      { initialProps: { active: true } },
    );
    expect(result.current.consumeHistoryEntry()).toBe(true);
    expect(window.history.state).toEqual({ page: 1 });
    expect(result.current.consumeHistoryEntry()).toBe(false);
    rerender({ active: false });
    vi.runAllTimers();
    expect(back).not.toHaveBeenCalled();
  });

  it.each(["cleared", "different route", "already popped"])(
    "does not consume a stale entry that is %s",
    (state) => {
      const { result } = renderHook(() => useSheetHistory(() => {}));
      if (state === "cleared") window.history.replaceState({ page: 2 }, "");
      if (state === "different route") window.history.replaceState({ page: 2, __amSheet: true }, "", "/usage");
      if (state === "already popped") window.dispatchEvent(new PopStateEvent("popstate"));
      const replace = vi.spyOn(window.history, "replaceState");
      expect(result.current.consumeHistoryEntry()).toBe(false);
      expect(replace).not.toHaveBeenCalled();
    },
  );

  it("adds one history entry for the sheet, keeping the URL and state", () => {
    window.history.replaceState({ page: 1 }, "");
    const push = vi.spyOn(window.history, "pushState");
    renderHook(() => useCloseOnBack(() => {}));

    expect(push).toHaveBeenCalledTimes(1);
    expect(push.mock.calls[0][0]).toEqual({ page: 1, __amSheet: true });
    expect(push.mock.calls[0][2]).toBeUndefined();
  });

  it("closes the sheet when Back takes that entry off", () => {
    const onClose = vi.fn();
    renderHook(() => useCloseOnBack(onClose));

    window.dispatchEvent(new PopStateEvent("popstate"));
    window.dispatchEvent(new PopStateEvent("popstate"));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("removes the entry when the sheet closes itself, and closes once", () => {
    const onClose = vi.fn();
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const { result } = renderHook(() => useCloseOnBack(onClose));

    result.current();
    // The pop that history.back() causes must not close it a second time.
    window.dispatchEvent(new PopStateEvent("popstate"));

    expect(back).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("adds nothing while inactive, and adds the entry when it opens", () => {
    window.history.replaceState({ page: 1 }, "");
    const push = vi.spyOn(window.history, "pushState");
    const { rerender } = renderHook(
      ({ active }: { active: boolean }) => useCloseOnBack(() => {}, active),
      { initialProps: { active: false } },
    );

    expect(push).not.toHaveBeenCalled();

    rerender({ active: true });
    expect(push).toHaveBeenCalledTimes(1);
  });

  it("removes the entry when the sheet closes without calling back", () => {
    vi.useFakeTimers();
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const { rerender } = renderHook(
      ({ active }: { active: boolean }) => useCloseOnBack(() => {}, active),
      { initialProps: { active: true } },
    );

    rerender({ active: false });
    expect(back).not.toHaveBeenCalled();
    vi.runAllTimers();

    expect(back).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("keeps the entry when the effect re-runs, as it does in development", () => {
    vi.useFakeTimers();
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const onClose = vi.fn();
    const { rerender } = renderHook(
      ({ active }: { active: boolean }) => useCloseOnBack(onClose, active),
      { initialProps: { active: true } },
    );

    // Off and straight back on, before the scheduled removal can run.
    rerender({ active: false });
    rerender({ active: true });
    vi.runAllTimers();

    expect(back).not.toHaveBeenCalled();
    // The entry is still the sheet's, so Back still closes it.
    window.dispatchEvent(new PopStateEvent("popstate"));
    expect(onClose).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("does not add a second entry when one is already there", () => {
    window.history.replaceState({ __amSheet: true }, "");
    const push = vi.spyOn(window.history, "pushState");
    renderHook(() => useCloseOnBack(() => {}));
    expect(push).not.toHaveBeenCalled();
  });

  // A control inside the sheet navigated; the sheet unmounts on the new page.
  it("leaves history alone when the sheet goes away by navigating", () => {
    vi.useFakeTimers();
    const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const { unmount } = renderHook(() => useCloseOnBack(() => {}));

    window.history.pushState({ router: true }, "", "/workflows/app?id=wf-1");
    unmount();
    vi.runAllTimers();

    expect(back).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
