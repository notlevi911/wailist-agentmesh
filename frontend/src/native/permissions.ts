// Asking for background location, and what to do when the answer is no.
//
// Android's "Always allow" is the most-refused permission on the platform, and
// apps that ask cold get refused far more often than apps that explain first.
// The system dialog is also a one-shot in practice: a user who picks "Don't
// allow" cannot be asked again from inside the app, only sent to Settings. So
// the order here is deliberate -- explain, then ask -- and it is not just
// politeness, it is the difference between the feature working and not.
//
// Google Play reviews background-location use specifically, and asks to see
// the disclosure. The copy below is written to be shown, not to be a comment.
import { Geofence } from "./nativeGeofence";

export type PermissionState = "granted" | "denied" | "denied-permanently";

// What the user is told BEFORE Android's dialog appears. Plain about what is
// collected, when, and what is kept -- the server stores only the derived
// enter/leave event, never a location history, and saying so is both true and
// the strongest argument for granting.
export const DISCLOSURE = {
  title: "Run workflows when you arrive or leave",
  body:
    "AgentMesh can start a workflow when you cross the edge of a place you " +
    "choose. To do that, Android needs to let it check your location even " +
    "when the app is closed.\n\n" +
    "Your location is used only to work out whether you crossed that edge. " +
    "On our servers we keep just that -- you entered, or you left -- and " +
    "never the coordinates themselves. There is no record of where you have " +
    "been for us to look through.\n\n" +
    "On this phone, a crossing waits in a queue until it can be sent, so one " +
    "that happens with no signal is not lost. Those waiting readings do " +
    "include your position. They never leave the device except to report " +
    "that one crossing, and each is deleted as soon as it is sent -- or " +
    "within a day if it never can be.\n\n" +
    "You can turn this off at any time, and everything else in the app keeps " +
    "working without it.",
  grant: "Choose location access",
  decline: "Not now",
} as const;

// Requests background location. Call ONLY after the disclosure above has been
// shown and the user has chosen to continue -- asking first and explaining
// afterwards is the pattern that gets refused.
export async function requestBackgroundLocation(): Promise<PermissionState> {
  const { granted, reason } = await Geofence.requestPermission();
  if (granted) return "granted";
  // Refused at the foreground step means the user dismissed the first dialog
  // outright; refused at background means they chose "While using the app",
  // which is not enough for a fence that has to hold with the phone pocketed.
  // Both are refusals, but only the second is worth a second conversation.
  return reason === "background" ? "denied" : "denied-permanently";
}

// What the app says once permission is refused.
//
// It does not nag. A refusal is an answer, and re-prompting on every launch is
// how an app gets uninstalled. The geofence feature simply shows as off, with
// one honest route to Settings for anyone who changes their mind.
export const DENIED_COPY = {
  title: "Location triggers are off",
  body:
    "AgentMesh will not start workflows when you arrive or leave. Everything " +
    "else -- viewing, running and chatting with your workflows -- works as " +
    "normal.",
  action: "Open Settings",
} as const;

// Sending someone to Settings is the only route back after a refusal --
// Android will not show the permission dialog again once it has been declined.
export async function openSettings(): Promise<void> {
  await Geofence.openSettings();
}
