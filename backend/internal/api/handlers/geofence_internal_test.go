package handlers

import (
	"testing"
	"time"
)

// pingLimiter is the only piece of the geofence path with no database in it,
// and it is worth pinning precisely because its job is easy to state and easy
// to get subtly wrong: it must throttle PER WORKFLOW, and it must not throttle
// the first fix a workflow ever sends.
func TestPingLimiterAllowsTheFirstFix(t *testing.T) {
	var p pingLimiter
	if !p.allow("wf-1", time.Now()) {
		t.Fatal("the first fix for a workflow must always be allowed")
	}
}

func TestPingLimiterThrottlesWithinTheInterval(t *testing.T) {
	var p pingLimiter
	now := time.Now()

	if !p.allow("wf-1", now) {
		t.Fatal("first fix should be allowed")
	}
	if p.allow("wf-1", now.Add(minPingInterval-time.Millisecond)) {
		t.Fatal("a fix inside the interval must be throttled")
	}
	if !p.allow("wf-1", now.Add(minPingInterval)) {
		t.Fatal("a fix exactly at the interval must be allowed")
	}
}

// One phone flooding must not silence a different user's workflow.
func TestPingLimiterIsPerWorkflow(t *testing.T) {
	var p pingLimiter
	now := time.Now()

	p.allow("wf-1", now)
	if !p.allow("wf-2", now) {
		t.Fatal("throttling one workflow must not throttle another")
	}
}

// A throttled fix must not push the window forward, or a client polling
// tightly would be locked out indefinitely rather than for one interval.
func TestThrottledFixDoesNotExtendTheWindow(t *testing.T) {
	var p pingLimiter
	now := time.Now()

	p.allow("wf-1", now)
	p.allow("wf-1", now.Add(time.Second)) // throttled
	p.allow("wf-1", now.Add(2*time.Second))
	if !p.allow("wf-1", now.Add(minPingInterval)) {
		t.Fatal("the window must run from the last ALLOWED fix, not the last attempt")
	}
}
