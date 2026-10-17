import { describe, expect, it } from "vitest";
import {
  formatDuration,
  formatSpend,
  formatUntil,
  triggerLabel,
} from "./runFormat";

describe("formatUntil", () => {
  const now = Date.parse("2026-09-19T10:00:00.000Z");
  const at = (ms: number) => new Date(now + ms).toISOString();

  it("counts minutes up to the hour, rounding up", () => {
    expect(formatUntil(at(20_000), now)).toBe("in 1 min");
    expect(formatUntil(at(4 * 60_000), now)).toBe("in 4 min");
    expect(formatUntil(at(59 * 60_000), now)).toBe("in 59 min");
    // Just past 59 minutes rounds up to the hour, not down to "in 0 h".
    expect(formatUntil(at(59 * 60_000 + 1), now)).toBe("in 1 h");
  });

  it("switches to hours, days and weeks", () => {
    expect(formatUntil(at(3.5 * 3_600_000), now)).toBe("in 3 h");
    expect(formatUntil(at(26 * 3_600_000), now)).toBe("in 1 day");
    expect(formatUntil(at(9 * 86_400_000), now)).toBe("in 9 days");
    expect(formatUntil(at(22 * 86_400_000), now)).toBe("in 3 wks");
  });

  it("reads a reached time as now, and nothing as a dash", () => {
    expect(formatUntil(at(-30_000), now)).toBe("now");
    expect(formatUntil(undefined, now)).toBe("-");
    expect(formatUntil("soon", now)).toBe("-");
  });
});

describe("triggerLabel", () => {
  it("names each trigger the backend writes", () => {
    expect(triggerLabel("manual")).toBe("Manual");
    expect(triggerLabel("schedule")).toBe("Schedule");
    expect(triggerLabel("geofence")).toBe("Location");
    expect(triggerLabel("webhook")).toBe("Webhook");
    expect(triggerLabel("tendril-console")).toBe("Console");
    expect(triggerLabel("prism-repo-review")).toBe("Console");
    expect(triggerLabel("something-new")).toBe("something-new");
  });
});

describe("formatSpend", () => {
  it("keeps small charges readable", () => {
    expect(formatSpend(0)).toBe("$0");
    expect(formatSpend(500)).toBe("<$0.001");
    expect(formatSpend(65_000)).toBe("$0.065");
    expect(formatSpend(1_250_000)).toBe("$1.25");
  });
});

describe("formatDuration", () => {
  const start = "2026-09-14T10:00:00.000Z";

  it("formats finished runs", () => {
    expect(formatDuration(start, "2026-09-14T10:00:45.000Z")).toBe("45s");
    expect(formatDuration(start, "2026-09-14T10:03:20.000Z")).toBe("3m 20s");
    expect(formatDuration(start, "2026-09-14T11:05:00.000Z")).toBe("1h 5m");
  });

  it("measures a running run against now", () => {
    expect(formatDuration(start, undefined, Date.parse(start) + 12_000)).toBe(
      "12s",
    );
  });

  it("refuses to show a negative or unreadable duration", () => {
    expect(formatDuration(start, "2026-09-14T09:00:00.000Z")).toBe("-");
    expect(formatDuration("nope")).toBe("-");
  });
});
