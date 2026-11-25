import type { Health } from "../api/health.ts";
import { formatTime } from "./time.ts";

type Outage = { title: string; text: string };

// "Today, 10:42" gives "10:42", "Yesterday, 10:42" gives "yesterday at 10:42", and
// "20 November, 10:42" gives "20 November at 10:42", so the time reads well inside a sentence.
function inSentence(text: string): string {
  return text
    .replace(/^Today, /, "")
    .replace(/^Yesterday, /, "yesterday, ")
    .replace(/, (\d\d:\d\d)$/, " at $1");
}

// The notice of an outage.
export function outage(
  health: Health,
  viewer: "owner" | "client",
  manager: string,
  now: number,
  // Only for tests: the page uses the zone of the browser.
  zone?: string,
): Outage | null {
  const owner = viewer === "owner";
  const since =
    health.lastGoodAt === undefined
      ? ""
      : ` The states below are from ${inSentence(formatTime(health.lastGoodAt, now, zone).text)}.`;
  if (health.state === "core") {
    return owner
      ? {
          title: "The core does not answer.",
          text: "No message can go out and no number can be linked. Check that the core runs, and read its logs.",
        }
      : {
          title: "Calyx cannot reach WhatsApp now.",
          text: `Messages cannot go out. Contact ${manager}. The states below can be old.`,
        };
  }
  if (health.state === "database" && owner) {
    return {
      title: "The database does not answer.",
      text: `Check that PostgreSQL runs. This page tries again by itself.${since}`,
    };
  }
  if (health.state === "backend" || health.state === "database") {
    return {
      title: "Calyx does not answer.",
      text: owner
        ? `Check that the backend runs. This page tries again by itself.${since}`
        : `This page tries again by itself. If it continues, contact ${manager}.${since}`,
    };
  }
  return null;
}
