package nodes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A fixed winter instant, so DST zones have a known offset.
var schedNow = time.Date(2026, 1, 14, 12, 0, 0, 0, time.UTC)

func setSchedule(t *testing.T, tz string, args map[string]any) (*builderSchedule, string) {
	t.Helper()
	s := newBuilderSchedule(tz)
	msg, err := s.set(args, schedNow)
	if err != nil {
		t.Fatalf("set_schedule(%v): %v", args, err)
	}
	return s, msg
}

// The bug this exists for: "9 am" typed straight into a UTC cron fires at
// 2:30 pm in India.
func TestSetScheduleConvertsLocalTimeToUTC(t *testing.T) {
	cases := []struct {
		name, tz string
		args     map[string]any
		want     string
	}{
		{"india daily", "Asia/Kolkata", map[string]any{"cadence": "daily", "time": "09:00"}, "30 3 * * *"},
		{"utc daily", "UTC", map[string]any{"cadence": "daily", "time": "09:00"}, "0 9 * * *"},
		{"new york daily in winter", "America/New_York", map[string]any{"cadence": "daily", "time": "09:00"}, "0 14 * * *"},
		// Crosses back a day: Monday 08:00 in Tokyo is Sunday 23:00 UTC.
		{"tokyo weekly", "Asia/Tokyo", map[string]any{"cadence": "weekly", "time": "08:00", "day": "Monday"}, "0 23 * * 0"},
		// Crosses forward a day: Monday 20:00 in LA is Tuesday 04:00 UTC.
		{"los angeles weekly", "America/Los_Angeles", map[string]any{"cadence": "weekly", "time": "20:00", "day": "monday"}, "0 4 * * 2"},
		{"india monthly", "Asia/Kolkata", map[string]any{"cadence": "monthly", "time": "09:00", "dayOfMonth": float64(15)}, "30 3 15 * *"},
		{"new york monthly, same UTC day", "America/New_York", map[string]any{"cadence": "monthly", "time": "09:00", "dayOfMonth": float64(28)}, "0 14 28 * *"},
		// 20:00 in New York is the next UTC day all year: the 16th, every month.
		{"new york monthly, next UTC day", "America/New_York", map[string]any{"cadence": "monthly", "time": "20:00", "dayOfMonth": float64(15)}, "0 1 16 * *"},
		// 02:00 in India is the previous UTC day: fine while that day is >= 1.
		{"india monthly, previous UTC day", "Asia/Kolkata", map[string]any{"cadence": "monthly", "time": "02:00", "dayOfMonth": float64(10)}, "30 20 9 * *"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := setSchedule(t, c.tz, c.args)
			if s.cron == nil || *s.cron != c.want {
				t.Fatalf("cron = %v, want %q", s.cron, c.want)
			}
		})
	}
}

// The model has to be able to tell the user exactly what was set, in their
// own terms, and that nothing fires until the workflow is deployed.
func TestSetScheduleTellsTheModelWhatItSet(t *testing.T) {
	_, msg := setSchedule(t, "Asia/Kolkata", map[string]any{"cadence": "daily", "time": "09:00"})
	for _, want := range []string{"every day", "9:00 AM", "Asia/Kolkata", "Deploy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q is missing %q", msg, want)
		}
	}
}

// People read schedules in words. The cron is only what gets stored, so the
// model is never handed one to repeat in its reply.
func TestSetScheduleKeepsTheCronFromTheModel(t *testing.T) {
	for _, tc := range []struct {
		zone, summary string
		args          map[string]any
	}{
		{"UTC", "every day at 9:00 AM (UTC)", map[string]any{"cadence": "daily", "time": "09:00"}},
		{"America/New_York", "every Friday at 8:30 PM (America/New_York)", map[string]any{"cadence": "weekly", "time": "20:30", "day": "friday"}},
		{"Europe/Berlin", "on day 15 of every month at 1:30 PM (Europe/Berlin)", map[string]any{"cadence": "monthly", "time": "13:30", "dayOfMonth": 15.0}},
	} {
		s, msg := setSchedule(t, tc.zone, tc.args)
		if s.cron == nil || *s.cron == "" {
			t.Fatalf("%v: no schedule was set", tc.args)
		}
		if strings.Contains(msg, *s.cron) || strings.Contains(strings.ToLower(msg), "cron \"") {
			t.Errorf("%v: message exposes stored cron %q: %s", tc.args, *s.cron, msg)
		}
		if !strings.Contains(msg, tc.summary) || strings.Contains(msg, "Asia/Kolkata") || strings.Contains(msg, "Monday") {
			t.Errorf("%v: message does not describe only the actual cadence and zone: %s", tc.args, msg)
		}
	}
}

func TestUnsupportedScheduleDoesNotRedirectToUnsupportedEditor(t *testing.T) {
	s, _ := setSchedule(t, "UTC", map[string]any{"cadence": "daily", "time": "09:00"})
	previous := *s.cron
	_, err := s.set(map[string]any{"cadence": "hourly"}, schedNow)
	if err == nil || strings.Contains(err.Error(), "set it on the Workflows page") {
		t.Fatalf("unsupported cadence must not redirect to an incapable editor: %v", err)
	}
	if *s.cron != previous {
		t.Fatal("unsupported cadence changed the existing schedule")
	}
}

// An unknown or missing zone must not silently become some other zone the
// user never named. UTC, stated as UTC, is the honest fallback.
func TestSetScheduleFallsBackToUTCAndSaysSo(t *testing.T) {
	for _, tz := range []string{"", "Not/AZone"} {
		s, msg := setSchedule(t, tz, map[string]any{"cadence": "daily", "time": "09:00"})
		if *s.cron != "0 9 * * *" {
			t.Errorf("tz %q: cron = %q", tz, *s.cron)
		}
		if !strings.Contains(msg, "UTC") {
			t.Errorf("tz %q: the message must say the time is UTC: %s", tz, msg)
		}
	}
}

func TestSetScheduleOffClearsIt(t *testing.T) {
	s, _ := setSchedule(t, "UTC", map[string]any{"cadence": "off"})
	if s.cron == nil || *s.cron != "" {
		t.Fatalf("cron = %v, want a pointer to the empty string", s.cron)
	}
}

// A build that never calls set_schedule must not touch an existing schedule.
func TestNoSetScheduleLeavesTheScheduleAlone(t *testing.T) {
	if s := newBuilderSchedule("Asia/Kolkata"); s.cron != nil {
		t.Fatalf("cron = %q, want nil", *s.cron)
	}
}

func TestSetScheduleRejectsWhatThePickerCannotShow(t *testing.T) {
	bad := []map[string]any{
		{"cadence": "hourly", "time": "09:00"},
		{"cadence": "daily", "time": "9am"},
		{"cadence": "daily", "time": "24:00"},
		{"cadence": "weekly", "time": "09:00"},
		{"cadence": "weekly", "time": "09:00", "day": "weekdays"},
		{"cadence": "monthly", "time": "09:00", "dayOfMonth": float64(31)},
		{"cadence": "monthly", "time": "09:00"},
	}
	for _, args := range bad {
		s := newBuilderSchedule("UTC")
		if _, err := s.set(args, schedNow); err == nil {
			t.Errorf("set_schedule(%v) was accepted", args)
		}
		if s.cron != nil {
			t.Errorf("set_schedule(%v) changed the schedule despite failing", args)
		}
	}
}

// Through the real build loop: the schedule the model set comes back on the
// result, converted with the timezone the request carried.
func TestBuildGraphReturnsTheScheduleTheModelSet(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		w.Header().Set("Content-Type", "application/json")
		switch turn {
		case 1:
			io.WriteString(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"set_schedule","args":{"cadence":"daily","time":"09:00"}}}]}}]}`)
		default:
			io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"Scheduled for 9 am every day. Deploy it to start."}]}}]}`)
		}
	}))
	defer srv.Close()
	SetGeminiBaseURL(srv.URL)
	defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	res, err := BuildGraph(context.Background(), BuildRequest{
		APIKey: "k", Message: "run this every morning at 9", TimeZone: "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	if res.Schedule == nil {
		t.Fatal("the schedule the model set was dropped")
	}
	// Asia/Kolkata has no DST, so this holds whatever today's date is.
	if *res.Schedule != "30 3 * * *" {
		t.Errorf("schedule = %q, want 30 3 * * *", *res.Schedule)
	}
}

func TestBuildGraphWithoutSetScheduleLeavesItNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"Nothing to change."}]}}]}`)
	}))
	defer srv.Close()
	SetGeminiBaseURL(srv.URL)
	defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	res, err := BuildGraph(context.Background(), BuildRequest{APIKey: "k", Message: "hi"})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	if res.Schedule != nil {
		t.Errorf("schedule = %q, want nil so an existing schedule is left alone", *res.Schedule)
	}
}

// A monthly time whose UTC equivalent falls on a different day cannot be
// written as a standard cron without firing on the wrong day. Day 1 at 02:00
// in India is 20:30 UTC on the LAST day of the previous month, which cron
// cannot name; day 28 at 23:00 in New York is 04:00 UTC on the 29th, which
// February does not have. Refused, not saved wrong.
func TestSetScheduleRefusesAMonthlyTimeThatChangesDayInUTC(t *testing.T) {
	cases := []struct {
		tz   string
		time string
		day  float64
	}{
		{"Asia/Kolkata", "02:00", 1},
		{"America/New_York", "23:00", 28},
		// 19:30 is the next UTC day in winter and the same day in summer.
		{"America/New_York", "19:30", 10},
		// Same UTC day in winter (00:30), the previous day in summer
		// (23:30): only a summer check catches it.
		{"Europe/Berlin", "01:30", 10},
	}
	for _, c := range cases {
		s := newBuilderSchedule(c.tz)
		_, err := s.set(map[string]any{"cadence": "monthly", "time": c.time, "dayOfMonth": c.day}, schedNow)
		if err == nil {
			t.Errorf("%s day %v at %s was accepted as %q", c.tz, c.day, c.time, *s.cron)
			continue
		}
		if s.cron != nil {
			t.Errorf("%s: the schedule changed despite the refusal", c.tz)
		}
		if !strings.Contains(err.Error(), "different time or day") {
			t.Errorf("%s: the refusal should say what to do instead: %v", c.tz, err)
		}
	}
}

// The same request must give the same cron whatever month the build runs in.
func TestSetScheduleMonthlyDoesNotDependOnTheCurrentMonth(t *testing.T) {
	args := map[string]any{"cadence": "monthly", "time": "09:00", "dayOfMonth": float64(28)}
	var first string
	for m := time.January; m <= time.December; m++ {
		s := newBuilderSchedule("America/New_York")
		if _, err := s.set(args, time.Date(2026, m, 3, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		// New York moves between UTC-5 and UTC-4, so the hour legitimately
		// differs with DST; the day must not.
		fields := strings.Fields(*s.cron)
		if first == "" {
			first = fields[2]
		}
		if fields[2] != "28" {
			t.Errorf("%s: day-of-month = %s, want 28", m, fields[2])
		}
	}
}
