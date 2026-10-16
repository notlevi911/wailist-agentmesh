export function scheduleErrorText(
  error: unknown,
  operation: "save" | "remove" | "load" = "save",
): string {
  const message = error instanceof Error ? error.message : "";
  if (message && !/cron|(?:^|\s)[\d*/,-]+(?: [\d*/,-]+){4}(?:\s|$)/i.test(message)) {
    return message;
  }
  const action = { save: "saved", remove: "removed", load: "loaded" }[operation];
  return `The schedule couldn't be ${action}. Please try again.`;
}
