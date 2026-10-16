// The backend stores fixed UTC cron times. Weekly selections share the
// current offset, so a later clock change can move their local run time.

export type Cadence = "daily" | "weekly" | "monthly";

export interface CadenceValue {
  /** Local time-of-day, "HH:MM" (24h). */
  time: string;
  cadence: Cadence;
  /** 0 (Sun)-6 (Sat), local day-of-week, for a single weekly day. */
  dayOfWeek?: number;
  /** 0 (Sun)-6 (Sat), local days-of-week for a multi-day weekly cadence. */
  daysOfWeek?: number[];
  /** 1-28, local day-of-month. Read only when cadence === "monthly". */
  dayOfMonth?: number;
}

export function cadenceToCron(
  value: CadenceValue,
  now: Date = new Date(),
): string {
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(value.time)) {
    throw new RangeError("Choose a valid time.");
  }
  const [hStr, mStr] = value.time.split(":");
  const localHour = Number(hStr);
  const localMinute = Number(mStr);

  let local = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    localHour,
    localMinute,
    0,
    0,
  );
  if (value.cadence === "weekly") {
    // A stored cron has one fixed UTC time. Use one offset for every day,
    // including weeks that straddle a clock change.
    const utc = localHour * 60 + localMinute + now.getTimezoneOffset();
    const shift = Math.floor(utc / 1440);
    const minutes = (utc + 1440) % 1440;
    const days = selectedDaysOfWeek(value)
      .map((day) => (day + shift + 7) % 7)
      .sort((a, b) => a - b);
    return `${minutes % 60} ${Math.floor(minutes / 60)} * * ${days.join(",")}`;
  }
  if (value.cadence === "monthly" && value.dayOfMonth !== undefined) {
    local = new Date(
      now.getFullYear(),
      now.getMonth(),
      value.dayOfMonth,
      localHour,
      localMinute,
      0,
      0,
    );
  }

  const minute = local.getUTCMinutes();
  const hour = local.getUTCHours();

  if (value.cadence === "daily") {
    return `${minute} ${hour} * * *`;
  }
  // Monthly. A standard cron names a UTC day of the month, so the local day
  // has to be expressed as one: day 15 at 21:00 in New York is the 16th at
  // 02:00 UTC, every month. That only works when the UTC day exists in every
  // month (1-28) and is the same all year. Day 28 late in the evening west
  // of UTC is the 29th, which February lacks; day 1 early in the morning
  // east of UTC is the last day of the previous month, which cron cannot
  // name; and near midnight UTC, daylight saving moves the day back and
  // forth between winter and summer. Those are refused rather than saved on
  // a day that is wrong for part of the year.
  const dayOfMonth = value.dayOfMonth ?? local.getDate();
  const utcDom = monthlyUtcDay(dayOfMonth, localHour, localMinute, now);
  return `${minute} ${hour} ${utcDom} * *`;
}

/** Return canonical local weekdays, keeping the legacy single-day field valid. */
export function selectedDaysOfWeek(value: CadenceValue): number[] {
  const raw =
    value.daysOfWeek ??
    (value.dayOfWeek === undefined ? [1] : [value.dayOfWeek]);
  if (
    !raw.length ||
    raw.some((day) => !Number.isInteger(day) || day < 0 || day > 6)
  ) {
    throw new RangeError("Choose at least one valid weekday.");
  }
  return [...new Set(raw)].sort((a, b) => a - b);
}

/** Thrown when a local monthly time has no single UTC day-of-month. */
export class ScheduleDayError extends Error {
  constructor() {
    super(
      "That time falls on a different day in UTC for some months, so it cannot be scheduled monthly. Pick a different time or day.",
    );
    this.name = "ScheduleDayError";
  }
}

const DAY_MS = 86_400_000;

/**
 * The UTC day-of-month a local monthly time runs on, checked in winter and
 * in summer. The backend's set_schedule applies the same rule (see
 * backend/internal/engine/nodes/graphschedule.go); keep the two in step.
 */
function monthlyUtcDay(
  dayOfMonth: number,
  hour: number,
  minute: number,
  now: Date,
): number {
  const shiftIn = (month: number) => {
    const at = new Date(now.getFullYear(), month, dayOfMonth, hour, minute);
    const localDay = Date.UTC(at.getFullYear(), at.getMonth(), at.getDate());
    const utcDay = Date.UTC(
      at.getUTCFullYear(),
      at.getUTCMonth(),
      at.getUTCDate(),
    );
    return Math.round((utcDay - localDay) / DAY_MS);
  };
  const winter = shiftIn(0);
  const summer = shiftIn(6);
  const utcDom = dayOfMonth + winter;
  if (winter !== summer || utcDom < 1 || utcDom > 28) {
    throw new ScheduleDayError();
  }
  return utcDom;
}

// cronToCadence is cadenceToCron's inverse, for pre-filling the picker when
// a schedule already exists. Only recognizes the exact shapes
// cadenceToCron produces (month field always "*") -- returns null for
// anything else. Callers must protect a custom schedule until the user
// explicitly chooses to replace it.
export function cronToCadence(
  cron: string,
  now: Date = new Date(),
): CadenceValue | null {
  const parts = cron.trim().split(/\s+/);
  if (parts.length !== 5) return null;
  const [mStr, hStr, domStr, monStr, dowStr] = parts;
  const utcMinute = Number(mStr);
  const utcHour = Number(hStr);
  if (
    !/^\d{1,2}$/.test(mStr) ||
    utcMinute > 59 ||
    !/^\d{1,2}$/.test(hStr) ||
    utcHour > 23 ||
    monStr !== "*"
  ) {
    return null;
  }

  if (domStr === "*" && dowStr === "*") {
    const instant = new Date(
      Date.UTC(
        now.getUTCFullYear(),
        now.getUTCMonth(),
        now.getUTCDate(),
        utcHour,
        utcMinute,
      ),
    );
    return { cadence: "daily", time: toLocalTimeString(instant) };
  }
  if (domStr === "*" && dowStr !== "*") {
    const utcDays = parseExactDaysOfWeek(dowStr);
    if (!utcDays) return null;
    const local = utcHour * 60 + utcMinute - now.getTimezoneOffset();
    const shift = Math.floor(local / 1440);
    const minutes = (local + 1440) % 1440;
    const time = `${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`;
    const localDays = utcDays
      .map((day) => (day + shift + 7) % 7)
      .sort((a, b) => a - b);
    return localDays.length === 1
      ? {
          cadence: "weekly",
          time,
          dayOfWeek: localDays[0],
        }
      : {
          cadence: "weekly",
          time,
          daysOfWeek: localDays,
        };
  }
  if (domStr !== "*" && dowStr === "*") {
    const utcDom = Number(domStr);
    // Reject unsupported days before Date normalizes them into another month.
    if (!/^\d{1,2}$/.test(domStr) || utcDom < 1 || utcDom > 28) return null;
    const base = new Date(
      Date.UTC(
        now.getUTCFullYear(),
        now.getUTCMonth(),
        utcDom,
        utcHour,
        utcMinute,
      ),
    );
    // A day outside 1-28 here means this cron wasn't produced by
    // cadenceToCron (which only ever emits a day it can prove round-trips
    // to one of 1-28) -- possibly set by another tool, per this function's
    // own docstring. Clamping into range would silently misreport a real
    // stored day-29/30/31 schedule as a materially different day instead
    // of returning null as promised, letting a subsequent Save silently
    // overwrite it with the fabricated value.
    const localDom = base.getDate();
    if (localDom < 1 || localDom > 28) return null;
    return {
      cadence: "monthly",
      time: toLocalTimeString(base),
      dayOfMonth: localDom,
    };
  }
  return null;
}

function toLocalTimeString(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function parseExactDaysOfWeek(field: string): number[] | null {
  const parts = field.split(",");
  if (parts.length === 0 || parts.some((part) => !/^\d+$/.test(part))) {
    return null;
  }
  const days = parts.map(Number);
  if (
    days.some((day) => day < 0 || day > 6) ||
    days.some((day, index) => index > 0 && day <= days[index - 1])
  ) {
    return null;
  }
  return days;
}
