// Why a run cannot start, in the user's terms.
//
// Both entry points (the Run button and a message typed into the console) used
// to say "Deploy first to run" for every un-deployed workflow. That is only
// sometimes the real obstacle. A workflow with no provider node has nothing to
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
  /** Does the graph contain a provider node -- i.e. is there anything to run? */
  hasProviderNode: boolean;
  /** May THIS client deploy? False for a read-only viewer. */
  canDeploy: boolean;
}

// Returns the message to show, or null when nothing is blocking the run.
export function runBlockedMessage({
  deployed,
  hasProviderNode,
  canDeploy,
}: RunBlockedInput): string | null {
  if (deployed) return null;

  // Nothing to deploy yet. Say what is actually missing rather than pointing at
  // a Deploy button that would not help.
  if (!hasProviderNode) {
    return canDeploy
      ? "Add a provider node before running"
      : "This workflow has no agent yet — build it in the AgentMesh desktop app";
  }

  // Deployable, but not deployed. Only mention the button to someone who has it.
  return canDeploy
    ? "Deploy first to run"
    : "Not deployed yet — deploy it from the AgentMesh desktop app";
}
