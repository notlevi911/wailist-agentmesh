// Is this client a handheld -- a phone or a tablet -- rather than a computer?
//
// The question the app actually needs answered is "does this person have a
// keyboard, a pointer, and room to author a workflow", and no single browser
// API answers it. So this is a decision table over several signals, kept pure
// and signal-driven (rather than reading `navigator` itself) so every rung can
// be tested without a DOM. The same shape panelSizing.ts and viewport.ts use.
//
// Explicitly NOT viewport width. A laptop window dragged narrow is still a
// laptop: same keyboard, same mouse, same person. Width decides layout; it does
// not decide capability.
//
// The planned native Android app is a Capacitor WebView, and it classifies as
// a handheld here on purpose, not by accident: it is a viewer and a trigger
// client by design, never a workflow editor, so read-only is the outcome that
// was wanted. Nothing overrides this, and nothing should -- operating a
// workflow (run, stop, chat) is not withheld from a viewer, which is all that
// app needs. See lib/readonly.ts for what a viewer may still do.

export interface DeviceSignals {
  /** navigator.userAgentData?.mobile -- undefined outside Chromium. */
  uaDataMobile?: boolean;
  /** navigator.userAgentData?.platform -- "Android", "iOS", "Windows", … */
  uaDataPlatform?: string;
  /** matchMedia("(pointer: coarse)") -- the PRIMARY pointer is imprecise. */
  pointerCoarse: boolean;
  /** matchMedia("(hover: none)") -- the primary pointer cannot hover. */
  hoverNone: boolean;
  /** navigator.maxTouchPoints */
  maxTouchPoints: number;
}

// Platforms that are a handheld regardless of what `mobile` claims.
const HANDHELD_PLATFORMS = new Set(["android", "ios", "ipados"]);

export function isHandheld(s: DeviceSignals): boolean {
  const hasUAData =
    s.uaDataMobile !== undefined || s.uaDataPlatform !== undefined;

  if (hasUAData) {
    // 1. Chromium saying "mobile" is definitive: it is a phone.
    if (s.uaDataMobile === true) return true;

    // 2. Android TABLETS report mobile:false -- the hint means "phone-shaped",
    //    not "handheld". The platform is what distinguishes a Galaxy Tab from
    //    a Windows laptop, and it is the only thing that can.
    const platform = (s.uaDataPlatform ?? "").trim().toLowerCase();
    if (HANDHELD_PLATFORMS.has(platform)) return true;

    // 3. Any other Chromium platform -- Windows, macOS, Linux, Chrome OS -- is
    //    a computer, whatever its touchscreen reports. This rung is the whole
    //    point of preferring UA-CH over a `pointer: coarse` media query: a
    //    touchscreen Windows laptop matches `coarse`, and demoting one to a
    //    viewer is exactly the bug this table exists to avoid.
    return false;
  }

  // 4. Safari and Firefox ship no userAgentData, so fall back to what the
  //    input devices say. This rung carries the iPad, which is otherwise
  //    undetectable: iPadOS Safari sends a byte-identical Mac user agent, so
  //    no UA string -- and therefore nothing server-side -- can ever spot one.
  //
  //    All three signals are required together. A desktop Safari on a Mac has
  //    no touch points; a touch-capable desktop browser still reports a fine,
  //    hovering primary pointer. Only a device whose *primary* input is a
  //    finger satisfies all three.
  return s.pointerCoarse && s.hoverNone && s.maxTouchPoints > 0;
}

// The media queries whose changes must re-run the decision. Both are live: a
// tablet that pairs a Bluetooth mouse flips them, and the app should follow.
export const POINTER_COARSE_QUERY = "(pointer: coarse)";
export const HOVER_NONE_QUERY = "(hover: none)";

interface UADataLike {
  mobile?: boolean;
  platform?: string;
}

// Reads the live signals off `navigator`. Returns a desktop-shaped answer when
// there is no window at all (SSR), which is what keeps the server snapshot and
// the first client render in agreement.
//
// `mediaMatches` lets a caller that already holds its own MediaQueryList
// objects (useReadOnly.ts caches them to avoid a fresh matchMedia() call on
// every render) pass their current .matches values in directly, rather than
// this function calling matchMedia() a second time for the same two queries.
// Omitted, it calls matchMedia() itself -- the one-shot callers (isHandheldNow)
// have no cached MediaQueryList to reuse, so there is nothing to pass.
export function readDeviceSignals(mediaMatches?: {
  pointerCoarse: boolean;
  hoverNone: boolean;
}): DeviceSignals {
  if (typeof window === "undefined" || !window.matchMedia) {
    return { pointerCoarse: false, hoverNone: false, maxTouchPoints: 0 };
  }
  const uaData = (navigator as Navigator & { userAgentData?: UADataLike })
    .userAgentData;
  return {
    uaDataMobile: uaData?.mobile,
    uaDataPlatform: uaData?.platform,
    pointerCoarse:
      mediaMatches?.pointerCoarse ??
      window.matchMedia(POINTER_COARSE_QUERY).matches,
    hoverNone:
      mediaMatches?.hoverNone ?? window.matchMedia(HOVER_NONE_QUERY).matches,
    maxTouchPoints: navigator.maxTouchPoints ?? 0,
  };
}

export function isHandheldNow(): boolean {
  return isHandheld(readDeviceSignals());
}
