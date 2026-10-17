// Shared wording for run history, so a run reads the same in a workflow's
// summary, the Activity list and the run sheet.

// What started the run, in the words someone checking their phone would use.
export function triggerLabel(triggeredBy: string): string {
  switch (triggeredBy) {
    case "manual":
      return "Manual";
    case "schedule":
      return "Schedule";
    case "geofence":
      return "Location";
    case "webhook":
      return "Webhook";
    default:
      // tendril-console, prism-console, helixbox-console, prism-repo-review.
      return triggeredBy.includes("console") || triggeredBy.startsWith("prism")
        ? "Console"
        : triggeredBy;
  }
}

// USD micros as dollars. Small charges keep three decimals so a $0.065 call
// does not round to $0.07; anything under a tenth of a cent shows as "<$0.001"
// rather than a misleading $0.000.
export function formatSpend(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "$0";
  if (micros < 1_000) return "<$0.001";
  const dollars = micros / 1e6;
  return dollars < 1 ? `$${dollars.toFixed(3)}` : `$${dollars.toFixed(2)}`;
}

// How long a run took, or has been going: "45s", "3m 20s", "1h 5m".
export function formatDuration(
  startedAt: string,
  finishedAt?: string,
  now: number = Date.now(),
): string {
  const start = Date.parse(startedAt);
  const end = finishedAt ? Date.parse(finishedAt) : now;
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) return "-";
  const total = Math.round((end - start) / 1000);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m ${total % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// How far off a scheduled run is: "in 4 min", "in 3 h", "in 2 days",
// "in 3 wks". A time already reached reads "now": the scheduler checks once a
// minute, so a due run can sit a few seconds past its time before it starts.
export function formatUntil(
  at: string | undefined | null,
  now: number = Date.now(),
): string {
  if (!at) return "-";
  const due = Date.parse(at);
  if (Number.isNaN(due)) return "-";
  const ms = due - now;
  if (ms <= 0) return "now";
  // Rounded up, so a run 20 seconds away is "in 1 min", never "in 0 min";
  // anything that rounds up to the hour reads as the hour.
  const minutes = Math.ceil(ms / 60_000);
  if (minutes < 60) return `in ${minutes} min`;
  const hours = Math.max(1, Math.floor(ms / 3_600_000));
  if (hours < 24) return `in ${hours} h`;
  const days = Math.floor(ms / 86_400_000);
  if (days < 14) return `in ${days} ${days === 1 ? "day" : "days"}`;
  return `in ${Math.floor(days / 7)} wks`;
}

// The time of day a run started, in the device's locale ("14:05", "2:05 PM").
export function formatRunTime(startedAt: string, locale?: string): string {
  const d = new Date(startedAt);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(locale, {
    hour: "numeric",
    minute: "2-digit",
  }).format(d);
}
