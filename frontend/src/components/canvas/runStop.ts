// Decides when useRunTranscript should treat a run as stopped from outside
// (the Run button's stop branch) rather than as finished on its own.
//
// The only outside signal the transcript gets is `running` going false. That
// same prop also goes false AFTER a normal completion: the transcript calls
// onRunComplete, and CanvasPage answers with setRunning(false). Deciding on
// `running` plus "a stream was opened" alone therefore marked every run that
// succeeded as stopped the moment it finished (#67), and its chat turn said
// "Run stopped" instead of the answer.

export interface RunStopSignals {
  /** The parent still considers the run in progress. */
  running: boolean;
  /** This transcript opened a live stream for the current run. */
  streamOpened: boolean;
  /** The run already reached a terminal state on its own (completeOnce ran). */
  completed: boolean;
}

/**
 * True only for a run the parent stopped: no longer running, a stream was
 * opened for it, and it had not already completed by itself.
 */
export function isExternalStop({
  running,
  streamOpened,
  completed,
}: RunStopSignals): boolean {
  return !running && streamOpened && !completed;
}
