// Dates as the Mac app's SwiftUI formats say them, in the user's language.

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 86400], ["month", 30 * 86400], ["week", 7 * 86400], ["day", 86400], ["hour", 3600], ["minute", 60], ["second", 1],
];

/** "2 days ago", "1 hr. ago": .relative(presentation: .numeric, unitsStyle: .abbreviated). */
export function relativeShort(ms: number, now = Date.now()): string {
  const seconds = Math.max(0, Math.floor((now - ms) / 1000));
  const format = new Intl.RelativeTimeFormat("en", { style: "short", numeric: "always" });
  for (const [unit, size] of UNITS) {
    if (seconds >= size || unit === "second") return format.format(-Math.floor(seconds / size), unit);
  }
  return "";
}

/** "31 Oct 2026 at 15:45": .formatted(date: .abbreviated, time: .shortened). */
export function dateAndTime(ms: number): string {
  const d = new Date(ms);
  const date = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric" }).format(d);
  const time = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(d);
  return `${date} at ${time}`;
}

/** "31/10/2026, 15:45": .formatted(). */
export function dateNumeric(ms: number): string {
  return new Intl.DateTimeFormat(undefined, { day: "2-digit", month: "2-digit", year: "numeric", hour: "numeric", minute: "2-digit" }).format(new Date(ms));
}
