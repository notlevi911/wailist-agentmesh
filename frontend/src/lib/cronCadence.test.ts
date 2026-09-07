import { describe, expect, it } from "vitest";

// Pin a fixed, non-UTC timezone with a stable (non-DST-ambiguous) offset for
// the whole file, set before any Date is constructed below — Node reads
// process.env.TZ when resolving the local timezone for Date's local
// accessors, so this makes every test deterministic regardless of which
// machine/CI runner executes it. America/New_York in January is a fixed
// UTC-5 (EST, no DST), avoiding a DST-transition-week false failure.
//
// This must be a plain top-level statement, not a beforeAll(...) callback:
// beforeAll's body doesn't run until vitest actually executes lifecycle
// hooks, which happens AFTER all of this module's top-level code (including
// the NOW constant below) has already run during module collection. A
// beforeAll here would pin TZ too late to affect NOW's underlying instant —
// only a synchronous statement, in file order, actually runs first.
process.env.TZ = "America/New_York";

import { cadenceToCron, cronToCadence } from "./cronCadence";

// A fixed "now" anchor: Wednesday, Jan 14 2026, in whatever timezone the
// test runner's Date resolves TZ to (America/New_York, set above).
const NOW = new Date(2026, 0, 14, 12, 0, 0);

describe("cadenceToCron", () => {
  it("converts a daily local time to UTC minute/hour, no day fields", () => {
    // 09:00 EST (UTC-5) -> 14:00 UTC
    expect(cadenceToCron({ cadence: "daily", time: "09:00" }, NOW)).toBe(
      "0 14 * * *",
    );
  });

  it("converts a weekly local time+day to UTC minute/hour/day-of-week", () => {
    // Monday (1) 09:00 EST -> Monday 14:00 UTC, same day (no rollover at 9am)
    expect(
      cadenceToCron({ cadence: "weekly", time: "09:00", dayOfWeek: 1 }, NOW),
    ).toBe("0 14 * * 1");
  });

  it("rolls the day-of-week forward when local time crosses midnight UTC", () => {
    // Sunday (0) 21:00 EST = 02:00 UTC the next day (Monday) -> dow rolls 0 -> 1
    expect(
      cadenceToCron({ cadence: "weekly", time: "21:00", dayOfWeek: 0 }, NOW),
    ).toBe("0 2 * * 1");
  });

  it("converts a monthly local time+day to UTC minute/hour/day-of-month", () => {
    expect(
      cadenceToCron({ cadence: "monthly", time: "09:00", dayOfMonth: 15 }, NOW),
    ).toBe("0 14 15 * *");
  });

  it("keeps day 29 when the rollover stays within the same (31-day) month", () => {
    // day 28, 21:00 EST rolls to day 29 UTC, but NOW is January -- day 29
    // always exists in January, so no fallback is needed here at all. Only
    // a rollover that actually crosses INTO a shorter next month (see the
    // February test below) needs the originally-picked day preserved.
    expect(
      cadenceToCron({ cadence: "monthly", time: "21:00", dayOfMonth: 28 }, NOW),
    ).toBe("0 2 29 * *");
  });

  it("preserves the exact day picked for a non-edge rollover, not just the 1-28 range", () => {
    // day 15, 21:00 EST rolls to day 16 UTC -- must stay 16. The clamp above
    // exists ONLY to stop day 28 rolling into the nonexistent Feb 29; every
    // other day's rollover must round-trip to the day it actually landed on,
    // not silently snap back to the originally-picked day.
    expect(
      cadenceToCron({ cadence: "monthly", time: "21:00", dayOfMonth: 15 }, NOW),
    ).toBe("0 2 16 * *");
  });

  it("falls back to the picked day when a rollover crosses into a different month, not just past day 28", () => {
    // day 28, 21:00 EST, in a non-leap February (2026) -- rolls into March 1
    // UTC, not day 29. A fallback that only recognized "landed on 29" would
    // miss this and silently emit day 1, firing on the wrong day every month.
    const febNow = new Date(2026, 1, 10, 12, 0, 0);
    expect(
      cadenceToCron(
        { cadence: "monthly", time: "21:00", dayOfMonth: 28 },
        febNow,
      ),
    ).toBe("0 2 28 * *");
  });
});

describe("cronToCadence", () => {
  it("round-trips a daily cron back to local time", () => {
    const cron = cadenceToCron({ cadence: "daily", time: "09:00" }, NOW);
    expect(cronToCadence(cron, NOW)).toEqual({
      cadence: "daily",
      time: "09:00",
    });
  });

  it("round-trips a weekly cron back to local time+day", () => {
    const cron = cadenceToCron(
      { cadence: "weekly", time: "09:00", dayOfWeek: 3 },
      NOW,
    );
    expect(cronToCadence(cron, NOW)).toEqual({
      cadence: "weekly",
      time: "09:00",
      dayOfWeek: 3,
    });
  });

  it("round-trips a monthly cron back to local time+day", () => {
    const cron = cadenceToCron(
      { cadence: "monthly", time: "09:00", dayOfMonth: 10 },
      NOW,
    );
    expect(cronToCadence(cron, NOW)).toEqual({
      cadence: "monthly",
      time: "09:00",
      dayOfMonth: 10,
    });
  });

  it("returns null for a cron this UI never produces (explicit month field)", () => {
    expect(cronToCadence("0 14 1 6 *", NOW)).toBeNull();
  });

  it("returns null for a malformed expression", () => {
    expect(cronToCadence("not a cron", NOW)).toBeNull();
  });

  it("returns null for a day-of-month outside 1-28, rather than clamping to a fabricated day", () => {
    // day 30, 12:00 UTC -> 07:00 EST, same calendar day -- a real day-30
    // schedule this UI could never have set (the picker only offers 1-28),
    // so it must be rejected, not silently reported as some other day.
    expect(cronToCadence("0 12 30 * *", NOW)).toBeNull();
  });
});
