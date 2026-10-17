// Why a run cannot start, in the user's terms.
//
// Both entry points (the Run button and a message typed into the console) used
// to say "Deploy first to run" for every un-deployed workflow. That is only
// sometimes the real obstacle. A graph that cannot run yet has nothing to
// deploy YET -- telling someone to deploy is a dead end, because the button
// they are being pointed at will not help them. And a read-only viewer cannot
// deploy at all, so naming deployment as the next step is advice they cannot
// take.
//
// Pure and framework-free so the whole table is unit-testable, which matters
// here: this is messaging logic, and messaging is exactly the kind of thing
// that rots silently.

export interface RunBlockedInput {
  /** Has the workflow been deployed? */
  deployed: boolean;
  /**
   * Could the graph run -- see isGraphRunnable. Decided from the graph itself,
   * not from whether a provider node exists: a tool-only pipeline needs no
   * provider, and a provider on the canvas does not make an unwired graph
   * runnable.
   */
  graphReady: boolean;
  /** Does some agent lack a provider on its model port -- see agentMissingModel. */
  agentMissingModel: boolean;
  /** Name of a flow step nothing flows into, if any -- see firstUnreachedStep. */
  unreachedStep?: string;
  /** Does the flow loop back on itself -- see hasFlowLoop. */
  flowLoop?: boolean;
  /** May THIS client deploy? False for a read-only viewer. */
  canDeploy: boolean;
}

/**
 * What the card shows. `title` is the headline (what is wrong), `detail` is
 * the sentence under it (what to do about it), and `action` is the button to
 * render -- null when there is no button this client could usefully press.
 *
 * Split out from the single string the toast used to take because a string
 * cannot carry a button: "Deploy first to run" flashed for 2.4 seconds and
 * pointed at a control the user then had to go find.
 */
export interface RunBlockedReason {
  code: "not-ready" | "not-deployed";
  title: string;
  detail: string;
  action: "deploy" | null;
}

export function runBlockedReason({
  deployed,
  graphReady,
  agentMissingModel,
  unreachedStep,
  flowLoop,
  canDeploy,
}: RunBlockedInput): RunBlockedReason | null {
  if (deployed) return null;

  // Nothing that could run yet. Say what is actually missing rather than
  // pointing at a Deploy button that would not help -- and offer no action,
  // because there is no single click that fixes this. "No model" only when an
  // agent really lacks one: a pipeline with no agent needs no model, and
  // telling that user to add a provider sends them after something the
  // workflow does not need.
  if (!graphReady) {
    if (!canDeploy) {
      return {
        code: "not-ready",
        title: agentMissingModel
          ? "No model attached yet"
          : flowLoop
            ? "The flow loops back on itself"
            : unreachedStep
              ? "A step isn't connected"
              : "Nothing to run yet",
        detail: "This workflow isn't finished yet: build it in the AgentMesh desktop app",
        action: null,
      };
    }
    if (agentMissingModel) {
      return {
        code: "not-ready",
        title: "No model attached yet",
        detail: "Attach a provider to the agent's model port before running",
        action: null,
      };
    }
    if (flowLoop) {
      return {
        code: "not-ready",
        title: "The flow loops back on itself",
        detail: "Remove the connection that closes the loop before running",
        action: null,
      };
    }
    // Name the step: a step nothing flows into is run first on an empty
    // input, so the run would fail there -- say which one to connect.
    return unreachedStep
      ? {
          code: "not-ready",
          title: "A step isn't connected",
          detail: `“${unreachedStep}” has nothing flowing into it: connect its input before running`,
          action: null,
        }
      : {
          code: "not-ready",
          title: "Nothing to run yet",
          detail: "Connect the trigger to a step before running",
          action: null,
        };
  }

  // Runnable, but not deployed. Only offer the button to someone who has it.
  return {
    code: "not-deployed",
    title: "Not deployed",
    detail: canDeploy
      ? "Deploy first to run"
      : "Not deployed yet: deploy it from the AgentMesh desktop app",
    action: canDeploy ? "deploy" : null,
  };
}

// Returns the message to show, or null when nothing is blocking the run.
// Retained for callers that only need the sentence. Derived from
// runBlockedReason so the two can never disagree about what is wrong.
export function runBlockedMessage(input: RunBlockedInput): string | null {
  return runBlockedReason(input)?.detail ?? null;
}
