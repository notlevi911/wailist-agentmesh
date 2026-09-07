"use client";

// Singleton coordinator over the browser's speechSynthesis, which can only
// speak one utterance at a time. Every ChatMessage's speaker button
// subscribes here instead of holding its own "am I speaking" state, so
// starting one message's playback automatically flips every other button
// back to idle -- no prop-drilling through ChatPane/ChatRail/CanvasPage.
//
// Coordination glue over a browser singleton, not pure logic -- same
// boundary the codebase already draws around ChatMessage/ChatPane
// themselves, so this has no test file beyond the cancel/speak race covered
// below (see speechText.ts for the pure, tested half of TTS).

type Listener = () => void;

let speakingId: string | null = null;
const listeners = new Set<Listener>();
let keepAliveTimer: ReturnType<typeof setInterval> | null = null;
// The id of the speak() call still owed a real speechSynthesis.speak() --
// see the comment in speak() for why this is deferred a tick rather than
// called inline.
let pendingId: string | null = null;

function notify() {
  for (const l of listeners) l();
}

function clearKeepAlive() {
  if (keepAliveTimer !== null) {
    clearInterval(keepAliveTimer);
    keepAliveTimer = null;
  }
}

/**
 * Chrome silently stops a speechSynthesis utterance partway through past
 * roughly 15s on some platforms unless paused/resumed periodically. Only
 * runs while something is actually speaking, cleared on end/error/stop.
 */
function startKeepAlive() {
  clearKeepAlive();
  keepAliveTimer = setInterval(() => {
    window.speechSynthesis.pause();
    window.speechSynthesis.resume();
  }, 10_000);
}

function setSpeaking(id: string | null) {
  if (speakingId === id) return;
  speakingId = id;
  notify();
}

export function ttsSupported(): boolean {
  return typeof window !== "undefined" && "speechSynthesis" in window;
}

export function isSpeaking(id: string): boolean {
  return speakingId === id;
}

/** Registers a listener fired whenever the speaking id changes. Returns an unsubscribe. */
export function subscribe(cb: Listener): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** Stops whatever is currently speaking, if anything. */
export function stop(): void {
  pendingId = null;
  if (!ttsSupported()) return;
  clearKeepAlive();
  window.speechSynthesis.cancel();
  setSpeaking(null);
}

export function speak(id: string, text: string): void {
  if (!ttsSupported() || text.trim() === "") return;

  // The API only ever plays one utterance at a time -- cancel whatever is
  // speaking before starting the next one. The canceled utterance's own
  // onend/onerror may still fire asynchronously after this; both handlers
  // below are guarded to only clear state for the utterance that owns it,
  // so a stale event from the canceled one can't wipe out the new one.
  window.speechSynthesis.cancel();
  clearKeepAlive();

  const utterance = new SpeechSynthesisUtterance(text);
  utterance.onstart = () => {
    setSpeaking(id);
    startKeepAlive();
  };
  const clearIfCurrent = () => {
    if (speakingId !== id) return;
    clearKeepAlive();
    setSpeaking(null);
  };
  utterance.onend = clearIfCurrent;
  utterance.onerror = clearIfCurrent;

  // Chrome has a documented cancel()/speak() same-tick bug: calling speak()
  // in the same synchronous block as a preceding cancel() can silently
  // no-op -- cancel() tears the engine down asynchronously, and a speak()
  // that lands before that finishes is dropped with no error, no onerror,
  // nothing. Deferring one tick lets the cancel() above actually settle
  // first. pendingId is a supersede guard: if stop() or another speak()
  // runs before this fires, it either clears pendingId (stop) or moves it
  // to a different id (a newer speak()), and this callback becomes a no-op
  // either way rather than starting an utterance nothing asked for anymore.
  pendingId = id;
  setTimeout(() => {
    if (pendingId !== id) return;
    window.speechSynthesis.speak(utterance);
  }, 0);
}
