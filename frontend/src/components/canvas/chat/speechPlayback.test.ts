import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// speechPlayback keeps speakingId/pendingId as module-level state, so each
// test needs a fresh module instance -- see credits/store.test.ts for the
// same pattern. window.speechSynthesis/SpeechSynthesisUtterance don't exist
// in jsdom, so both are faked here just enough to drive the cancel()/speak()
// race this file exists to cover; nothing else about speechPlayback gets a
// test (see its own module comment for why).

class FakeUtterance {
  onstart: (() => void) | null = null;
  onend: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public text: string) {}
}

function installFakeSpeechSynthesis() {
  const speak = vi.fn();
  const cancel = vi.fn();
  const pause = vi.fn();
  const resume = vi.fn();
  Object.defineProperty(window, "speechSynthesis", {
    value: { speak, cancel, pause, resume },
    configurable: true,
    writable: true,
  });
  // speak() constructs via the bare `SpeechSynthesisUtterance` identifier
  // (not `window.SpeechSynthesisUtterance`), which under jsdom resolves off
  // globalThis -- itself `window` in this test environment, but set through
  // globalThis directly since that is the identifier actually being resolved.
  // @ts-expect-error -- test-only global, real constructor is jsdom-absent
  globalThis.SpeechSynthesisUtterance = FakeUtterance;
  return { speak, cancel, pause, resume };
}

beforeEach(() => {
  vi.resetModules();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

async function freshPlayback() {
  return await import("./speechPlayback");
}

describe("speak", () => {
  it("defers the real speechSynthesis.speak() call by a tick", async () => {
    const fake = installFakeSpeechSynthesis();
    const { speak } = await freshPlayback();

    speak("msg-1", "hello");
    expect(fake.speak).not.toHaveBeenCalled();

    await vi.runAllTimersAsync();
    expect(fake.speak).toHaveBeenCalledTimes(1);
  });

  it("drops the deferred speak() when a second call supersedes it before the tick fires", async () => {
    // The Chrome bug this guards against: cancel() tears the engine down
    // asynchronously, so a speak() issued in the same synchronous block as a
    // preceding cancel() can silently no-op. Switching the speaker button
    // from one message to another hits exactly this pattern -- msg-1's
    // deferred call must not fire once msg-2 has superseded it.
    const fake = installFakeSpeechSynthesis();
    const { speak } = await freshPlayback();

    speak("msg-1", "first");
    speak("msg-2", "second");
    await vi.runAllTimersAsync();

    expect(fake.speak).toHaveBeenCalledTimes(1);
    const spoken = fake.speak.mock.calls[0][0] as FakeUtterance;
    expect(spoken.text).toBe("second");
  });

  it("drops the deferred speak() when stop() runs before the tick fires", async () => {
    const fake = installFakeSpeechSynthesis();
    const { speak, stop } = await freshPlayback();

    speak("msg-1", "hello");
    stop();
    await vi.runAllTimersAsync();

    expect(fake.speak).not.toHaveBeenCalled();
  });

  it("reports the utterance's own id as speaking once its onstart fires", async () => {
    const fake = installFakeSpeechSynthesis();
    const { speak, isSpeaking } = await freshPlayback();

    speak("msg-1", "hello");
    await vi.runAllTimersAsync();
    const spoken = fake.speak.mock.calls[0][0] as FakeUtterance;
    spoken.onstart?.();

    expect(isSpeaking("msg-1")).toBe(true);
  });
});
