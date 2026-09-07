"use client";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

function getRecognitionCtor(): (new () => SpeechRecognition) | undefined {
  if (typeof window === "undefined") return undefined;
  return window.SpeechRecognition ?? window.webkitSpeechRecognition;
}

// Support never changes after mount, so it needs no subscription -- just the
// same false-until-mounted server snapshot useSpeechPlayback uses, so the
// mic button's presence can't ever mismatch between the SSR pass (no
// `window`) and the client's first render (which does have `window` and
// would otherwise disagree with what the server sent down).
const noSubscription = () => () => {};

export interface UseSpeechRecognitionResult {
  /** False when this browser has no SpeechRecognition implementation at all (e.g. Firefox). */
  supported: boolean;
  listening: boolean;
  /** Set on a permission/service error; cleared on the next start(). */
  error: string | null;
  start: () => void;
  /**
   * Stops the current session and guarantees no further onText calls fire
   * for it, even if the browser's own end/error events arrive afterward.
   * Load-bearing for ChatPane's submit(), which calls this before clearing
   * the draft so a stray final transcript can't land in an already-sent box.
   */
  stop: () => void;
}

/**
 * Wraps SpeechRecognition for one-shot dictation. `onText` is called with the
 * combined transcript for the current session on every interim update, and
 * once more, final, when the session ends -- the caller decides how to fold
 * that into its own state (see ChatPane, which merges it into `draft`).
 */
export function useSpeechRecognition(
  onText: (text: string, isFinal: boolean) => void,
): UseSpeechRecognitionResult {
  const supported = useSyncExternalStore(
    noSubscription,
    () => getRecognitionCtor() !== undefined,
    () => false,
  );
  const [listening, setListening] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const recognitionRef = useRef<SpeechRecognition | null>(null);
  const onTextRef = useRef(onText);
  // Ref writes must happen outside render (react-hooks/refs) -- this keeps
  // the closure `recognition.onresult` captured in start() calling the
  // latest `onText` without start()/teardown() needing onText as a dep.
  useEffect(() => {
    onTextRef.current = onText;
  });

  // Detaches the browser's own event handlers (not just our bookkeeping) so
  // a stop() call is immediately final: any 'result'/'end'/'error' the
  // recognition object fires afterward finds no handler attached and is a
  // no-op, regardless of how the async timing lines up.
  const teardown = useCallback(() => {
    const recognition = recognitionRef.current;
    if (!recognition) return;
    recognition.onresult = null;
    recognition.onerror = null;
    recognition.onend = null;
    recognitionRef.current = null;
    setListening(false);
  }, []);

  const start = useCallback(() => {
    const Ctor = getRecognitionCtor();
    if (!Ctor || recognitionRef.current) return;
    setError(null);
    const recognition = new Ctor();
    recognition.continuous = false;
    recognition.interimResults = true;
    recognition.lang =
      typeof navigator !== "undefined" ? navigator.language : "en-US";
    recognition.onresult = (event) => {
      let combined = "";
      let isFinal = false;
      for (let i = event.resultIndex; i < event.results.length; i++) {
        const result = event.results[i];
        combined += (combined ? " " : "") + result[0].transcript;
        isFinal = result.isFinal;
      }
      onTextRef.current(combined.trim(), isFinal);
    };
    recognition.onerror = (event) => {
      // "no-speech" (the user tapped mic and said nothing) and "aborted"
      // (we stopped it ourselves) are expected outcomes, not failures --
      // only surface the ones the user needs to act on.
      if (event.error === "not-allowed" || event.error === "service-not-allowed") {
        setError(
          "Microphone access was blocked — allow it in your browser's site settings.",
        );
      } else if (event.error !== "no-speech" && event.error !== "aborted") {
        setError("Voice input failed. Try again.");
      }
      teardown();
    };
    recognition.onend = teardown;
    recognitionRef.current = recognition;
    setListening(true);
    recognition.start();
  }, [teardown]);

  const stop = useCallback(() => {
    const recognition = recognitionRef.current;
    teardown();
    recognition?.stop();
  }, [teardown]);

  // Safety net for an unmount mid-session (e.g. leaving the workflow page
  // while dictating) -- one-shot sessions self-terminate on their own, but
  // this guarantees no callback fires into an unmounted ChatPane.
  useEffect(() => stop, [stop]);

  return { supported, listening, error, start, stop };
}
