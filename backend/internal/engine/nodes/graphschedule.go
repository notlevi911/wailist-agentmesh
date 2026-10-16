package nodes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The builder accepts local times and stores fixed UTC schedules. Weekly
// requests here use one weekday; the Workflows editor also supports lists.
// Keep the monthly day guard aligned with frontend/src/lib/cronCadence.ts.

// builderSchedule is what set_schedule decided during one build.
type builderSchedule struct {
	// loc is the user's timezone, from the browser. UTC when the client did
	// not send one or sent one this server does not know.
	loc *time.Location
	// cron is the pending change: nil leaves the workflow's schedule alone,
	// a pointer to "" clears it, anything else is the UTC cron to save.
	cron *string
}

func newBuilderSchedule(timeZone string) *builderSchedule {
	loc := time.UTC
	if tz := strings.TrimSpace(timeZone); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	return &builderSchedule{loc: loc}
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

// parseClock reads "HH:MM" in 24-hour time.
func parseClock(s string) (hour, minute int, ok bool) {
	h, m, found := strings.Cut(strings.TrimSpace(s), ":")
	if !found {
		return 0, 0, false
	}
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// argInt reads an integer argument whether the model sent it as a number or
// as a string.
func argInt(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v), true
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// cadenceCron converts a local-time cadence to the UTC cron the scheduler
// runs, anchored on now's date in loc. See cadenceToCron in cronCadence.ts,
// which this mirrors line for line.
func cadenceCron(cadence string, hour, minute int, dayOfWeek time.Weekday, dayOfMonth int, loc *time.Location, now time.Time) string {
	today := now.In(loc)
	local := time.Date(today.Year(), today.Month(), today.Day(), hour, minute, 0, 0, loc)
	switch cadence {
	case "weekly":
		local = local.AddDate(0, 0, (int(dayOfWeek)-int(local.Weekday())+7)%7)
	case "monthly":
		local = time.Date(today.Year(), today.Month(), dayOfMonth, hour, minute, 0, 0, loc)
	}
	utc := local.UTC()
	switch cadence {
	case "weekly":
		return fmt.Sprintf("%d %d * * %d", utc.Minute(), utc.Hour(), int(utc.Weekday()))
	case "monthly":
		// dayOfMonth is already the UTC day (see monthlyUTCDay).
		return fmt.Sprintf("%d %d %d * *", utc.Minute(), utc.Hour(), dayOfMonth)
	default:
		return fmt.Sprintf("%d %d * * *", utc.Minute(), utc.Hour())
	}
}

// set handles one set_schedule call and returns what the model is told.
func (s *builderSchedule) set(args map[string]any, now time.Time) (string, error) {
	cadence := strings.ToLower(strings.TrimSpace(argString(args, "cadence")))
	if cadence == "off" {
		empty := ""
		s.cron = &empty
		return "schedule removed: the workflow will only run when someone starts it. Tell the user.", nil
	}
	if cadence != "daily" && cadence != "weekly" && cadence != "monthly" {
		return "", fmt.Errorf("set_schedule: cadence must be daily, weekly with one weekday, monthly with one date, or off, not %q. Other cadences are not supported by this tool. Ask the user to choose a supported cadence before changing an existing schedule", cadence)
	}
	hour, minute, ok := parseClock(argString(args, "time"))
	if !ok {
		return "", fmt.Errorf("set_schedule: time must be 24-hour HH:MM, such as 09:00 or 18:30, not %q", argString(args, "time"))
	}

	var dow time.Weekday
	dom := 1
	var when string
	switch cadence {
	case "weekly":
		name := strings.ToLower(strings.TrimSpace(argString(args, "day")))
		d, known := weekdayNames[name]
		if !known {
			return "", fmt.Errorf("set_schedule: a weekly schedule needs day, a weekday name such as monday, not %q", name)
		}
		dow = d
		when = "every " + d.String()
	case "monthly":
		n, known := argInt(args, "dayOfMonth")
		if !known || n < 1 || n > 28 {
			// 28, not 31: a day that does not exist in every month would
			// silently skip February, and the Schedule picker offers 1-28.
			return "", fmt.Errorf("set_schedule: a monthly schedule needs dayOfMonth between 1 and 28")
		}
		// A standard cron names a UTC day of the month, so the local day
		// has to be expressible as one: day 15 at 21:00 in New York is the
		// 16th at 02:00 UTC, every month. That holds only when the UTC day
		// exists in every month (1-28) and is the same in winter and in
		// summer. Day 28 late in New York is the 29th, which February
		// lacks; day 1 early in India is the previous month's last day,
		// which cron cannot name; near midnight UTC, daylight saving moves
		// the day. The Schedule picker applies the same rule
		// (monthlyUtcDay in frontend/src/lib/cronCadence.ts).
		utcDom, ok := monthlyUTCDay(n, hour, minute, s.loc)
		if !ok {
			return "", fmt.Errorf("set_schedule: %02d:%02d on day %d in %s falls on a different day in UTC for some months, so it cannot be scheduled monthly -- ask the user for a different time or day",
				hour, minute, n, zoneName(s.loc))
		}
		dom = utcDom
		when = fmt.Sprintf("on day %d of every month", n)
	default:
		when = "every day"
	}

	expr := cadenceCron(cadence, hour, minute, dow, dom, s.loc, now)
	s.cron = &expr
	// The UTC cron is deliberately not in the message: whatever the model is
	// handed, it tends to repeat, and a cron expression means nothing to the
	// person reading the reply.
	clock := time.Date(2000, time.January, 1, hour, minute, 0, 0, time.UTC).Format("3:04 PM")
	return fmt.Sprintf("schedule set: %s at %s (%s). "+
		"It only fires once the workflow is deployed, so tell the user to press Deploy. "+
		"Tell them this schedule and timezone so they can check it. "+
		"The workflow still starts from its manual trigger -- do not add another trigger.",
		when, clock, zoneName(s.loc)), nil
}

func zoneName(loc *time.Location) string {
	if loc == time.UTC {
		return "UTC"
	}
	return loc.String()
}

// monthlyUTCDay is the UTC day-of-month a local monthly time runs on,
// checked in winter and in summer. Reports false when there is no single
// such day in 1-28.
func monthlyUTCDay(day, hour, minute int, loc *time.Location) (int, bool) {
	shift := func(month time.Month) int {
		at := time.Date(2026, month, day, hour, minute, 0, 0, loc)
		u := at.UTC()
		localDay := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		utcDay := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
		return int(utcDay.Sub(localDay).Hours() / 24)
	}
	winter, summer := shift(time.January), shift(time.July)
	utcDom := day + winter
	if winter != summer || utcDom < 1 || utcDom > 28 {
		return 0, false
	}
	return utcDom, true
}
