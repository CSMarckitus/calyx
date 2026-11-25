import { describe, expect, it } from "vitest";
import { ago, formatClock, formatTime, formatWithSeconds } from "./time.ts";

const utc = "UTC";
const now = Date.UTC(2026, 10, 23, 12, 0, 0);
const at = (d: number, h: number, m: number) => Date.UTC(2026, 10, d, h, m);

describe("formatTime", () => {
  it("says Today for the same day", () => {
    expect(formatTime(at(23, 10, 30), now, utc).text).toBe("Today, 10:30");
  });
  it("says Yesterday for the day before", () => {
    expect(formatTime(at(22, 18, 5), now, utc).text).toBe("Yesterday, 18:05");
  });
  it("shows the day and month for an older time, and the year for another year", () => {
    expect(formatTime(at(3, 9, 12), now, utc).text).toBe("3 November, 09:12");
    expect(formatTime(Date.UTC(2025, 11, 31, 23, 59), now, utc).text).toBe(
      "31 December 2025, 23:59",
    );
  });
  it("gives the full date in the title", () => {
    expect(formatTime(at(23, 10, 30), now, utc).title).toBe("23 November 2026, 10:30:00");
  });
  it("uses the day of the time zone", () => {
    const late = Date.UTC(2026, 10, 22, 23, 30);
    expect(formatTime(late, now, "Europe/Madrid").text).toBe("Today, 00:30");
  });
});

describe("ago", () => {
  it("counts seconds, minutes, hours and days", () => {
    expect(ago(now - 5_000, now)).toBe("5 seconds ago");
    expect(ago(now - 1_000, now)).toBe("1 second ago");
    expect(ago(now - 12 * 60_000, now)).toBe("12 minutes ago");
    expect(ago(now - 60_000, now)).toBe("1 minute ago");
    expect(ago(now - 3 * 3_600_000, now)).toBe("3 hours ago");
    expect(ago(now - 2 * 86_400_000, now)).toBe("2 days ago");
  });
  it("never gives a negative time", () => {
    expect(ago(now + 5_000, now)).toBe("0 seconds ago");
  });
});

describe("formatWithSeconds", () => {
  it("gives HH:MM:SS in the zone", () => {
    expect(formatWithSeconds(Date.UTC(2026, 10, 23, 9, 5, 7), utc)).toBe("09:05:07");
    expect(formatWithSeconds(Date.UTC(2026, 10, 23, 23, 59, 59), "Europe/Madrid")).toBe("00:59:59");
  });
});

describe("formatClock", () => {
  it("gives only HH:MM for today", () => {
    expect(formatClock(at(23, 18, 30), now, utc)).toBe("18:30");
  });
  it("adds the date for another day, and the year for another year", () => {
    expect(formatClock(at(24, 1, 5), now, utc)).toBe("24 November, 01:05");
    expect(formatClock(Date.UTC(2027, 0, 2, 8, 0), now, utc)).toBe("2 January 2027, 08:00");
  });
  it("uses the day of the zone", () => {
    expect(formatClock(Date.UTC(2026, 10, 23, 23, 30), now, "Europe/Madrid")).toBe(
      "24 November, 00:30",
    );
  });
});
