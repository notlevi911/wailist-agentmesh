import { describe, expect, it } from "vitest";
import { isExternalStop } from "./runStop";

describe("isExternalStop", () => {
  it("is a stop when the parent ends a run that was still going", () => {
    expect(
      isExternalStop({ running: false, streamOpened: true, completed: false }),
    ).toBe(true);
  });

  // #67: onRunComplete makes CanvasPage set running=false after a normal
  // finish. That must not be read as the user pressing Stop.
  it("is not a stop when running goes false because the run completed", () => {
    expect(
      isExternalStop({ running: false, streamOpened: true, completed: true }),
    ).toBe(false);
  });

  it("is not a stop while the run is still running", () => {
    expect(
      isExternalStop({ running: true, streamOpened: true, completed: false }),
    ).toBe(false);
  });

  it("is not a stop when no stream was ever opened for this run", () => {
    expect(
      isExternalStop({ running: false, streamOpened: false, completed: false }),
    ).toBe(false);
  });
});
