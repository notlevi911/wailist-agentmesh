"use client";
import { useCallback, useEffect, useState } from "react";

// The chat transcript for one workflow.
//
// Persisted to localStorage so a page reload doesn't lose the conversation,
// and so a turn stranded mid-run (see useChatConsole's recovery effect) can
// still be found and settled after the reload. All binding/settling below
// targets messages by predicate (last unbound pending turn, matching runId,
// matching id) rather than by array position or a freshly-returned handle --
// that's what lets a turn started before a page reload still resolve
// correctly once the run's outcome comes back.
//
// Scoped per workflow so opening a different workflow shows its own
// conversation rather than whatever was typed last, matching how
// useRunTranscript scopes its cached transcript.

export interface ChatMessage {
  id: string;
  sender: "user" | "assistant";
  text: string;
  /** ISO-8601 UTC. */
  ts: string;
  /** The run this message belongs to; set once the backend returns a run id. */
  runId?: string;
  /** An assistant turn still waiting on its run. */
  pending?: boolean;
  isError?: boolean;
  /**
   * The turn was stranded — a reload cut it off from its run. Distinct from
   * isError: the run itself may well have succeeded, we just stopped watching.
   */
  interrupted?: boolean;
  /** Activity-strip figures, filled in when the run finishes. */
  toolCount?: number;
  elapsedS?: number;
  spendUSD?: number;
}

export interface ChatSession {
  messages: ChatMessage[];
  sessionId: string;
  /**
   * Records the user's turn plus the pending assistant turn awaiting it.
   * Returns the assistant turn's id so the caller can settle that exact turn
   * if the run never starts.
   */
  startTurn: (text: string) => string;
  /** Binds the pending turn to the run the backend actually started. */
  attachRun: (runId: string) => void;
  /** Fills in the turn bound to this run. */
  completeTurnForRun: (
    runId: string,
    patch: Omit<ChatMessage, "id" | "sender" | "ts">,
  ) => void;
  /** Fills in one exact turn, by id. */
  completeTurnById: (
    id: string,
    patch: Omit<ChatMessage, "id" | "sender" | "ts">,
  ) => void;
  /**
   * Re-opens the turn bound to this run so a resumed attempt's eventual
   * outcome can settle it again. See reopenTurnForRunIn's doc comment.
   */
  reopenTurnForRun: (runId: string) => void;
  /** Clears the transcript and starts a new session id. */
  reset: () => void;
  hydrated: boolean;
}

const CHAT_PREFIX = "agentmesh_chat_";
// Keep the stored transcript bounded on both axes: a long conversation of
// large agent answers would otherwise grow without limit in a ~5 MB store
// shared with the run cache. Only the *stored* history is trimmed.
const MAX_STORED_MESSAGES = 60;
const MAX_STORED_BYTES = 256 * 1024;

interface StoredSession {
  sessionId: string;
  messages: ChatMessage[];
}

/**
 * Index of the most recent turn that is pending AND not yet bound to a run.
 *
 * Two conditions, both load-bearing. LAST rather than first: a turn stranded by
 * a reload sits earlier in the transcript than anything sent afterwards, so
 * matching the first pending would hand a fresh turn's answer to the stale
 * bubble. UNBOUND rather than merely pending: without it a second run could
 * rebind its id onto a turn already waiting on a different run.
 *
 * Exported for tests -- this predicate is what keeps a run attached to the
 * turn that started it.
 */
export function lastUnboundPendingIndex(messages: ChatMessage[]): number {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].pending && !messages[i].runId) return i;
  }
  return -1;
}

/**
 * Bind `runId` to the last unbound pending turn, or -- if there is none --
 * seed a bare assistant turn already bound to it.
 *
 * A run can start with no turn waiting at all: the topbar Run button on a
 * manual-trigger workflow calls startRun() directly, without going through
 * startTurn. Without a turn to bind, that run's outcome would only ever
 * reach the logs dock, never the chat rail. Guarded against already having
 * a turn for this runId so a duplicate call (e.g. an effect firing twice
 * under React Strict Mode) can't append two bubbles for one run.
 *
 * Exported for tests, same as settleIn/lastUnboundPendingIndex below.
 */
export function attachRunIn(
  messages: ChatMessage[],
  runId: string,
): ChatMessage[] {
  const idx = lastUnboundPendingIndex(messages);
  if (idx >= 0) {
    const next = [...messages];
    next[idx] = { ...next[idx], runId };
    return next;
  }
  if (messages.some((m) => m.runId === runId)) return messages;
  const now = new Date().toISOString();
  const seq = Math.random().toString(36).slice(2, 8);
  return [
    ...messages,
    {
      id: `a-${now}-${seq}`,
      sender: "assistant",
      text: "",
      ts: now,
      pending: true,
      runId,
    },
  ];
}

/**
 * Re-open the turn bound to `runId` so a later completion can settle it
 * again, undoing settleIn's `pending: false`.
 *
 * A resumed run reaches a terminal state a SECOND time for the same run id
 * (useRunTranscript resets its done/stopped state when the caller bumps
 * `attempt` after a successful resume, then flips `done` true again once the
 * resumed attempt finishes). Without this, completeTurnForRun's second call
 * for that run id is a silent no-op -- settleIn only ever touches a `pending`
 * turn, and the first attempt's completion already cleared that flag -- so
 * the chat bubble stays frozen on the pre-resume outcome (e.g. "run failed")
 * even after the resumed attempt goes on to succeed.
 *
 * Exported for tests, same as settleIn/attachRunIn.
 */
export function reopenTurnForRunIn(
  messages: ChatMessage[],
  runId: string,
): ChatMessage[] {
  const idx = messages.findIndex((m) => m.runId === runId);
  if (idx < 0 || messages[idx].pending) return messages;
  const next = [...messages];
  next[idx] = { ...next[idx], pending: true };
  return next;
}

/**
 * Settle the first pending turn matching `match`. Exported for tests: the
 * targeting rules here are what keep an answer attached to the question that
 * asked it, so they are worth asserting directly.
 */
export function settleIn(
  messages: ChatMessage[],
  match: (m: ChatMessage) => boolean,
  patch: Omit<ChatMessage, "id" | "sender" | "ts">,
): ChatMessage[] {
  const idx = messages.findIndex((m) => m.pending && match(m));
  if (idx < 0) return messages;
  const next = [...messages];
  next[idx] = { ...next[idx], ...patch, pending: false };
  return next;
}

function newSessionId(): string {
  // crypto.randomUUID is unavailable on http:// origins in some browsers, and
  // this id is a display/reset handle rather than a security token.
  return Math.random().toString(36).slice(2, 10);
}

function read(workflowId: string | undefined): StoredSession | null {
  if (!workflowId || typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(CHAT_PREFIX + workflowId);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as StoredSession;
    return Array.isArray(parsed.messages) ? parsed : null;
  } catch {
    return null;
  }
}

/**
 * Serialise a session, dropping the oldest turns until it fits the budget.
 *
 * Returning early when oversized -- which this used to do -- left the previous
 * snapshot in place, and a reload would then hydrate that stale copy: the
 * newest turn (already run, possibly already billed) vanished from the
 * transcript. Losing the *newest* message is the worst possible thing to
 * drop, so the oldest go first and the last turn is always kept even if it
 * alone exceeds the cap.
 *
 * Exported for tests: this is a data-loss path, so its behaviour is asserted
 * directly rather than inferred.
 */
export function serialiseForStorage(session: StoredSession): string {
  let messages = session.messages.slice(-MAX_STORED_MESSAGES);
  for (;;) {
    const serialized = JSON.stringify({
      sessionId: session.sessionId,
      messages,
    });
    if (serialized.length <= MAX_STORED_BYTES || messages.length <= 1) {
      return serialized;
    }
    messages = messages.slice(1);
  }
}

function write(workflowId: string | undefined, session: StoredSession): void {
  if (!workflowId || typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      CHAT_PREFIX + workflowId,
      serialiseForStorage(session),
    );
  } catch {
    /* quota or unavailable storage: persistence is best-effort */
  }
}

export function useChatSession(workflowId: string | undefined): ChatSession {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [sessionId, setSessionId] = useState("");
  // Hydration happens in an effect, not in useState's initializer, so the
  // server render and the first client render agree -- the same reason
  // ConsolePanel restores its height this way. Applied on the next frame
  // rather than synchronously in the effect body for that same reason: a
  // straight setState here trips the cascading-render rule.
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => {
    const id = requestAnimationFrame(() => {
      const stored = read(workflowId);
      setMessages(stored?.messages ?? []);
      setSessionId(stored?.sessionId ?? newSessionId());
      setHydrated(true);
    });
    return () => cancelAnimationFrame(id);
  }, [workflowId]);

  // Persist on every change once hydrated. Guarded on `hydrated` so the
  // initial empty state can't overwrite a stored transcript before it loads.
  useEffect(() => {
    if (!hydrated || !sessionId) return;
    write(workflowId, { sessionId, messages });
  }, [hydrated, workflowId, sessionId, messages]);

  const startTurn = useCallback((text: string): string => {
    const now = new Date().toISOString();
    // Ids are built outside the updater so the assistant turn's id can be
    // returned; deriving them from prev.length kept them inside the closure
    // and left the caller with no handle on the turn it had just created.
    const seq = Math.random().toString(36).slice(2, 8);
    const assistantId = `a-${now}-${seq}`;
    setMessages((prev) => [
      ...prev,
      { id: `u-${now}-${seq}`, sender: "user", text, ts: now },
      {
        id: assistantId,
        sender: "assistant",
        text: "",
        ts: now,
        pending: true,
      },
    ]);
    return assistantId;
  }, []);

  const attachRun = useCallback((runId: string) => {
    setMessages((prev) => attachRunIn(prev, runId));
  }, []);

  // Settling a turn targets it by identity, never by position. Position was
  // wrong in two ways: a recovery result could land on a turn sent while its
  // fetch was in flight, and two runs started before the first settled would
  // resolve in the wrong order. Both showed up as an answer appearing under
  // somebody else's question.
  const settle = useCallback(
    (
      match: (m: ChatMessage) => boolean,
      patch: Omit<ChatMessage, "id" | "sender" | "ts">,
    ) => setMessages((prev) => settleIn(prev, match, patch)),
    [],
  );

  /** Settle the turn bound to this run. Predicate-based, so it still finds
   * the turn after a page reload has replaced this hook instance. */
  const completeTurnForRun = useCallback(
    (runId: string, patch: Omit<ChatMessage, "id" | "sender" | "ts">) =>
      settle((m) => m.runId === runId, patch),
    [settle],
  );

  /** Settle one exact turn — used by recovery, which knows the stranded id. */
  const completeTurnById = useCallback(
    (id: string, patch: Omit<ChatMessage, "id" | "sender" | "ts">) =>
      settle((m) => m.id === id, patch),
    [settle],
  );

  const reopenTurnForRun = useCallback((runId: string) => {
    setMessages((prev) => reopenTurnForRunIn(prev, runId));
  }, []);

  const reset = useCallback(() => {
    setMessages([]);
    setSessionId(newSessionId());
  }, []);

  return {
    messages,
    sessionId,
    startTurn,
    attachRun,
    completeTurnForRun,
    completeTurnById,
    reopenTurnForRun,
    reset,
    hydrated,
  };
}
