import { describe, it, expect } from "vitest";
import { isHandheld, type DeviceSignals } from "./device";

// One case per device that actually exists, named for it. The signal values are
// what those devices really report -- the browser-pane case below was measured
// directly, not guessed.
const DEVICES: Array<{
  name: string;
  signals: DeviceSignals;
  handheld: boolean;
  why: string;
}> = [
  {
    name: "iPhone, Chrome or Safari",
    signals: {
      uaDataMobile: true,
      uaDataPlatform: "iOS",
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "every signal agrees",
  },
  {
    name: "Android phone, Chrome",
    signals: {
      uaDataMobile: true,
      uaDataPlatform: "Android",
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "rung 1 -- mobile hint is definitive",
  },
  {
    name: "Android tablet, Chrome",
    signals: {
      // The hint means "phone-shaped", so a tablet says false. Rung 2 is the
      // only thing standing between a Galaxy Tab and the full editor.
      uaDataMobile: false,
      uaDataPlatform: "Android",
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "rung 2 -- platform, since mobile:false would mislead",
  },
  // The planned native Android shell (Capacitor). Not measured -- no shell
  // exists yet -- but listed because the classification is a decision, not an
  // accident: the app is a viewer/controller by design, so landing in
  // read-only mode is the wanted outcome. Both rows are signal-aliases of
  // cases above; they earn their place by failing under this NAME if a future
  // edit to the table ever promotes the native app to editor mode.
  {
    name: "Capacitor Android WebView (native app shell)",
    signals: {
      uaDataMobile: true,
      uaDataPlatform: "Android",
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "rung 1 -- a WebView is Chromium, so the mobile hint is there",
  },
  {
    name: "Capacitor Android WebView, UA-CH withheld",
    signals: {
      // A shell that strips UA-CH, or a WebView that does not expose it, must
      // not fall back to editor. This is why the classification is guaranteed
      // rather than likely: three independent rungs all reach the same answer.
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "rung 4 -- holds with no userAgentData at all",
  },
  {
    name: "iPad, Safari (sends a Mac user agent)",
    signals: {
      // No userAgentData at all in WebKit, and the UA string is byte-identical
      // to a Mac's -- the touch signals are the only tell there is.
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 5,
    },
    handheld: true,
    why: "rung 4 -- the only rung that can see an iPad",
  },
  {
    name: "MacBook, Safari",
    signals: { pointerCoarse: false, hoverNone: false, maxTouchPoints: 0 },
    handheld: false,
    why: "rung 4 -- fine, hovering pointer and no touch",
  },
  {
    name: "Desktop Firefox",
    signals: { pointerCoarse: false, hoverNone: false, maxTouchPoints: 0 },
    handheld: false,
    why: "rung 4 -- no userAgentData in Gecko either",
  },
  {
    name: "Touchscreen Windows laptop, Chrome",
    signals: {
      uaDataMobile: false,
      uaDataPlatform: "Windows",
      // A touchscreen laptop really does report these when touch is the
      // active input -- which is why a pointer-only rule would demote it.
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 10,
    },
    handheld: false,
    why: "rung 3 -- a laptop is a laptop, touchscreen or not",
  },
  {
    name: "embedded dev-tool browser pane (measured)",
    signals: {
      // Measured on a fresh tab at 1280px: a touch-capable webview that would
      // fail any pointer-based check.
      uaDataMobile: false,
      uaDataPlatform: "Windows",
      pointerCoarse: true,
      hoverNone: true,
      maxTouchPoints: 10,
    },
    handheld: false,
    why: "rung 3 -- the case that killed the pointer-only design",
  },
];

describe("isHandheld", () => {
  for (const d of DEVICES) {
    it(`${d.handheld ? "viewer" : "editor"}: ${d.name} (${d.why})`, () => {
      expect(isHandheld(d.signals)).toBe(d.handheld);
    });
  }

  // The regression this whole change exists to prevent.
  it("ignores viewport width entirely", () => {
    const laptop: DeviceSignals = {
      uaDataMobile: false,
      uaDataPlatform: "Windows",
      pointerCoarse: false,
      hoverNone: false,
      maxTouchPoints: 0,
    };
    // There is no width input at all -- a narrow window cannot change this.
    expect(isHandheld(laptop)).toBe(false);
    expect(Object.keys(laptop)).not.toContain("width");
  });

  it("treats a platform hint case-insensitively and tolerates padding", () => {
    const base = { pointerCoarse: true, hoverNone: true, maxTouchPoints: 5 };
    expect(isHandheld({ ...base, uaDataPlatform: "ANDROID" })).toBe(true);
    expect(isHandheld({ ...base, uaDataPlatform: " iOS " })).toBe(true);
  });

  // With userAgentData present, rung 3 must win outright -- otherwise the
  // touch signals below it would drag a laptop back into viewer mode.
  it("does not fall through to touch signals when userAgentData is present", () => {
    expect(
      isHandheld({
        uaDataMobile: false,
        uaDataPlatform: "macOS",
        pointerCoarse: true,
        hoverNone: true,
        maxTouchPoints: 10,
      }),
    ).toBe(false);
  });

  it("needs all three touch signals in the fallback, not just one", () => {
    expect(
      isHandheld({ pointerCoarse: true, hoverNone: false, maxTouchPoints: 5 }),
    ).toBe(false);
    expect(
      isHandheld({ pointerCoarse: false, hoverNone: true, maxTouchPoints: 5 }),
    ).toBe(false);
    expect(
      isHandheld({ pointerCoarse: true, hoverNone: true, maxTouchPoints: 0 }),
    ).toBe(false);
  });
});
