// Times for people: local ("Today, 10:30"), with the full date for the `title`. `zone` is only for
// tests: the page uses the zone of the browser.
const dayKey = (t: number, zone?: string) =>
  new Intl.DateTimeFormat("en-CA", { timeZone: zone }).format(t);

// The key of the day before, from the key itself, because a day can have 23 or 25 hours.
function dayBefore(key: string): string {
  const [y = 0, m = 1, d = 1] = key.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d - 1)).toISOString().slice(0, 10);
}

const clock = (t: number, zone?: string) =>
  new Intl.DateTimeFormat("en-GB", {
    timeZone: zone,
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(t);

const date = (t: number, zone: string | undefined, year: boolean) =>
  new Intl.DateTimeFormat("en-GB", {
    timeZone: zone,
    day: "numeric",
    month: "long",
    ...(year ? { year: "numeric" } : {}),
  }).format(t);

export function formatTime(t: number, now: number, zone?: string): { text: string; title: string } {
  const full = new Intl.DateTimeFormat("en-GB", {
    timeZone: zone,
    day: "numeric",
    month: "long",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  })
    .format(t)
    .replace(" at ", ", ");
  const title = full.replace(/^(.*\d{4}),? /, "$1, ");
  const day = dayKey(t, zone);
  let text: string;
  if (day === dayKey(now, zone)) text = `Today, ${clock(t, zone)}`;
  else if (day === dayBefore(dayKey(now, zone))) text = `Yesterday, ${clock(t, zone)}`;
  else {
    const sameYear = day.slice(0, 4) === dayKey(now, zone).slice(0, 4);
    text = `${date(t, zone, !sameYear)}, ${clock(t, zone)}`;
  }
  return { text, title };
}

const units: [string, number][] = [
  ["day", 86_400],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

export function ago(t: number, now: number): string {
  const seconds = Math.max(0, Math.floor((now - t) / 1000));
  const [name, size] = units.find(([, s]) => seconds >= s) ?? ["second", 1];
  const n = Math.floor(seconds / size);
  return `${n} ${name}${n === 1 ? "" : "s"} ago`;
}

export function formatWithSeconds(t: number, zone?: string): string {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: zone,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(t);
}

// A time of the day, with the date only when it is not today (for "About HH:MM").
export function formatClock(t: number, now: number, zone?: string): string {
  const day = dayKey(t, zone);
  const today = dayKey(now, zone);
  if (day === today) return clock(t, zone);
  return `${date(t, zone, day.slice(0, 4) !== today.slice(0, 4))}, ${clock(t, zone)}`;
}
