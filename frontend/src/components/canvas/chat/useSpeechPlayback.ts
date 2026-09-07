"use client";
import { useCallback, useSyncExternalStore } from "react";
import { isSpeaking, speak, stop, subscribe, ttsSupported } from "./speechPlayback";

// speechSynthesis doesn't exist during SSR, and "is this browser's TTS
// support fixed for the lifetime of the tab" never changes after mount, so
// it needs no subscription of its own -- just a snapshot read guarded by a
// false-until-mounted server value, the same way `speaking` is.
const noSubscription = () => () => {};

/**
 * One message's view onto the shared speechPlayback singleton: whether this
 * particular id is the one currently speaking, and a toggle that starts or
 * stops it. Built on useSyncExternalStore rather than an effect + setState,
 * both because that's the correct primitive for a module-scope external
 * store and because it also gives the SSR-safe "false until mounted" value
 * for free via getServerSnapshot.
 */
export function useSpeechPlayback(id: string) {
  const speaking = useSyncExternalStore(
    subscribe,
    () => isSpeaking(id),
    () => false,
  );
  const supported = useSyncExternalStore(noSubscription, ttsSupported, () => false);

  const toggle = useCallback(
    (text: string) => {
      if (isSpeaking(id)) {
        stop();
      } else {
        speak(id, text);
      }
    },
    [id],
  );

  return { supported, speaking, toggle };
}
