package engine

import "testing"

// TestRunRegistryStaleDeregisterDoesNotDropNewerRun guards against a stale
// run's deregister (delayed behind a long-running deferred settlement call)
// deleting a same-workflow retrigger's registration instead of its own.
// Without the generation token, Stop() on the newer run would silently
// no-op even though it is actively running.
func TestRunRegistryStaleDeregisterDoesNotDropNewerRun(t *testing.T) {
	reg := newRunRegistry()

	var aCanceled, bCanceled bool
	genA := reg.register("wf1", func() { aCanceled = true })
	genB := reg.register("wf1", func() { bCanceled = true })

	// register("wf1", ...) a second time cancels the first run's context --
	// that's the existing, intentional "only the most recent run per
	// workflow is tracked" behavior, unrelated to this test.
	if !aCanceled {
		t.Fatal("registering run B should have canceled run A's context")
	}
	aCanceled = false

	// Run A's deferred deregister finally fires, but it's stale: run B's
	// registration has since taken over the "wf1" slot.
	reg.deregister("wf1", genA)

	// Run B must still be registered and stoppable.
	if !reg.cancel("wf1") {
		t.Fatal("run B's registration was dropped by run A's stale deregister")
	}
	if !bCanceled {
		t.Fatal("cancel(\"wf1\") should have invoked run B's cancel func")
	}

	// Run B's own (matching-generation) deregister does remove the entry.
	reg.deregister("wf1", genB)
	if reg.cancel("wf1") {
		t.Fatal("want no entry left after run B's own deregister")
	}
}
