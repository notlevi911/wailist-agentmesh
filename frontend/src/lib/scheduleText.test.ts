import { describe, expect, it } from "vitest";
import { cadenceToCron } from "./cronCadence";

// Same pinning as cronCadence.test.ts: a plain top-level statement, before
// any Date exists, so every local-time accessor below is deterministic.
// America/New_York in January is a fixed UTC-5.
process.env.TZ = "America/New_York";

import {
  describeCadence,
  describeNextRun,
  describeRun,
  formatLocalTime,
  nextLocalRun,
  observesDaylightSaving,
  ordinal,
  timeZoneLabel,
} from "./scheduleText";

// Wednesday, Jan 14 2026, 12:00 local.
const NOW = new Date(2026, 0, 14, 12, 0, 0);

describe("formatLocalTime", () => {
  it("renders a 12-hour clock", () => {
    expect(formatLocalTime("09:00")).toBe("9:00 AM");
    expect(formatLocalTime("00:05")).toBe("12:05 AM");
    expect(formatLocalTime("12:00")).toBe("12:00 PM");
    expect(formatLocalTime("23:59")).toBe("11:59 PM");
  });
});

describe("ordinal", () => {
  it("handles the teens and the 1/2/3 endings", () => {
    expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 23, 28].map(ordinal)).toEqual([
      "1st",
      "2nd",
      "3rd",
      "4th",
      "11th",
      "12th",
      "13th",
      "21st",
      "22nd",
      "23rd",
      "28th",
    ]);
  });
});

describe("describeCadence", () => {
  it("names all selected weekdays in calendar order", () => {
    expect(describeCadence({ cadence: "weekly", time: "09:00", daysOfWeek: [5, 1] })).toBe("Every Monday and Friday at 9:00 AM");
    expect(describeCadence({ cadence: "weekly", time: "09:00", daysOfWeek: [5, 1, 3] })).toBe("Every Monday, Wednesday, and Friday at 9:00 AM");
  });
  it("says each cadence in plain English", () => {
    expect(describeCadence({ cadence: "daily", time: "09:00" })).toBe(
      "Every day at 9:00 AM",
    );
    expect(
      describeCadence({ cadence: "weekly", time: "13:30", dayOfWeek: 1 }),
    ).toBe("Every Monday at 1:30 PM");
    expect(
      describeCadence({ cadence: "monthly", time: "00:05", dayOfMonth: 22 }),
    ).toBe("On the 22nd of every month at 12:05 AM");
  });
});

describe("nextLocalRun", () => {
  it.each([
    {
      label: "daily spring transition",
      now: "2026-03-07T17:00:00Z",
      value: { cadence: "daily", time: "09:00" },
      cron: "0 14 * * *",
      expected: "2026-03-08T14:00:00.000Z",
      hour: 10,
    },
    {
      label: "daily fall transition",
      now: "2026-10-31T16:00:00Z",
      value: { cadence: "daily", time: "09:00" },
      cron: "0 13 * * *",
      expected: "2026-11-01T13:00:00.000Z",
      hour: 8,
    },
    {
      label: "monthly spring transition",
      now: "2026-03-07T17:00:00Z",
      value: { cadence: "monthly", time: "09:00", dayOfMonth: 1 },
      cron: "0 14 1 * *",
      expected: "2026-04-01T14:00:00.000Z",
      hour: 10,
    },
    {
      label: "monthly fall transition",
      now: "2026-10-02T16:00:00Z",
      value: { cadence: "monthly", time: "09:00", dayOfMonth: 1 },
      cron: "0 13 1 * *",
      expected: "2026-11-01T13:00:00.000Z",
      hour: 8,
    },
    {
      label: "monthly UTC day rollover across spring",
      now: "2026-02-21T17:00:00Z",
      value: { cadence: "monthly", time: "21:00", dayOfMonth: 20 },
      cron: "0 2 21 * *",
      expected: "2026-03-21T02:00:00.000Z",
      hour: 22,
    },
  ] as const)("previews the saved UTC occurrence for $label", ({ now, value, cron, expected, hour }) => {
    const reference = new Date(now);
    expect(cadenceToCron(value, reference)).toBe(cron);
    const next = nextLocalRun(value, reference);
    expect(next.toISOString()).toBe(expected);
    expect(next.getHours()).toBe(hour);
  });

  it.each([
    {
      cadence: "daily",
      time: "09:00",
      now: "2026-01-20T14:00:00Z",
      expected: "2026-01-21T14:00:00.000Z",
    },
    {
      cadence: "monthly",
      time: "09:00",
      dayOfMonth: 20,
      now: "2026-01-20T14:00:00Z",
      expected: "2026-02-20T14:00:00.000Z",
    },
  ] as const)("advances $cadence when now is exactly its scheduled occurrence", ({ now, expected, ...value }) => {
    expect(nextLocalRun(value, new Date(now)).toISOString()).toBe(expected);
  });

  it.each([
    {
      zone: "Asia/Kolkata",
      now: "2026-01-14T20:00:00Z",
      value: { cadence: "daily", time: "00:30" },
      expected: "2026-01-15T19:00:00.000Z",
    },
    {
      zone: "Asia/Kolkata",
      now: "2026-12-31T20:00:00Z",
      value: { cadence: "monthly", time: "09:00", dayOfMonth: 1 },
      expected: "2027-01-01T03:30:00.000Z",
    },
    {
      zone: "America/New_York",
      now: "2027-01-01T04:30:00Z",
      value: { cadence: "monthly", time: "09:00", dayOfMonth: 1 },
      expected: "2027-01-01T14:00:00.000Z",
    },
  ] as const)("uses the UTC calendar across local date boundaries in $zone at $now", ({ zone, now, value, expected }) => {
    const saved = process.env.TZ;
    process.env.TZ = zone;
    try {
      expect(nextLocalRun(value, new Date(now)).toISOString()).toBe(expected);
    } finally {
      process.env.TZ = saved;
    }
  });

  it("picks the nearest selected weekday, including later today", () => {
    expect(nextLocalRun({ cadence: "weekly", time: "09:00", daysOfWeek: [1, 3, 5] }, NOW)).toEqual(new Date(2026, 0, 16, 9));
    expect(nextLocalRun({ cadence: "weekly", time: "15:00", daysOfWeek: [1, 3, 5] }, NOW)).toEqual(new Date(2026, 0, 14, 15));
  });

  it("previews the actual UTC occurrence across a clock change", () => {
    const now = new Date(2026, 2, 5, 12);
    const next = nextLocalRun({ cadence: "weekly", time: "19:30", daysOfWeek: [0, 1] }, now);
    expect(next.toISOString()).toBe("2026-03-09T00:30:00.000Z");
    expect(next.getHours()).toBe(20);
    expect(next.getDay()).toBe(0);
  });
  it("daily: today while the time is still ahead, else tomorrow", () => {
    expect(nextLocalRun({ cadence: "daily", time: "15:00" }, NOW)).toEqual(
      new Date(2026, 0, 14, 15, 0),
    );
    expect(nextLocalRun({ cadence: "daily", time: "09:00" }, NOW)).toEqual(
      new Date(2026, 0, 15, 9, 0),
    );
  });

  it("weekly: the same weekday rolls a full week once its time has passed", () => {
    expect(
      nextLocalRun({ cadence: "weekly", time: "09:00", dayOfWeek: 3 }, NOW),
    ).toEqual(new Date(2026, 0, 21, 9, 0));
    expect(
      nextLocalRun({ cadence: "weekly", time: "15:00", dayOfWeek: 3 }, NOW),
    ).toEqual(new Date(2026, 0, 14, 15, 0));
    expect(
      nextLocalRun({ cadence: "weekly", time: "09:00", dayOfWeek: 1 }, NOW),
    ).toEqual(new Date(2026, 0, 19, 9, 0));
  });

  it("monthly: rolls into next month once the day has passed", () => {
    expect(
      nextLocalRun({ cadence: "monthly", time: "09:00", dayOfMonth: 20 }, NOW),
    ).toEqual(new Date(2026, 0, 20, 9, 0));
    expect(
      nextLocalRun({ cadence: "monthly", time: "09:00", dayOfMonth: 1 }, NOW),
    ).toEqual(new Date(2026, 1, 1, 9, 0));
  });
});

describe("describeRun", () => {
  it("says today and tomorrow by name", () => {
    expect(describeRun(new Date(2026, 0, 14, 15, 0), NOW)).toBe(
      "Today at 3:00 PM (in 3 hours)",
    );
    expect(describeRun(new Date(2026, 0, 14, 12, 30), NOW)).toBe(
      "Today at 12:30 PM (in 30 minutes)",
    );
    expect(describeRun(new Date(2026, 0, 14, 12, 1), NOW)).toBe(
      "Today at 12:01 PM (in 1 minute)",
    );
    expect(describeRun(new Date(2026, 0, 15, 9, 0), NOW)).toBe(
      "Tomorrow at 9:00 AM",
    );
  });

  it("gives a date and counts calendar days further out", () => {
    expect(describeRun(new Date(2026, 0, 19, 9, 0), NOW)).toBe(
      "Mon, Jan 19 at 9:00 AM (in 5 days)",
    );
    expect(describeRun(new Date(2026, 1, 1, 9, 0), NOW)).toBe(
      "Sun, Feb 1 at 9:00 AM (in 18 days)",
    );
  });
});

describe("describeNextRun", () => {
  it("reads as one sentence", () => {
    expect(describeNextRun(new Date(2026, 0, 14, 15, 0), NOW)).toBe(
      "Next run today at 3:00 PM (in 3 hours)",
    );
    expect(describeNextRun(new Date(2026, 0, 15, 9, 0), NOW)).toBe(
      "Next run tomorrow at 9:00 AM",
    );
    expect(describeNextRun(new Date(2026, 0, 19, 9, 0), NOW)).toBe(
      "Next run on Mon, Jan 19 at 9:00 AM (in 5 days)",
    );
  });
});

describe("timezone", () => {
  it("names the zone the way people say it", () => {
    expect(timeZoneLabel(NOW)).toBe("Eastern Standard Time");
  });

  it("knows whether the clocks change", () => {
    expect(observesDaylightSaving(NOW)).toBe(true);
    const saved = process.env.TZ;
    process.env.TZ = "Asia/Kolkata";
    try {
      expect(observesDaylightSaving(NOW)).toBe(false);
    } finally {
      process.env.TZ = saved;
    }
  });
});
