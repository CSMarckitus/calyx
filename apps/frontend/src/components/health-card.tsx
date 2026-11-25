import { cn } from "@/lib/utils";
import type { Health } from "../api/health.ts";
import { formatTime } from "../domain/time.ts";
import { meta } from "./new-version.tsx";
import { PopoverContent } from "./ui/popover.tsx";
import { Words } from "./words.tsx";

export type Viewer = "owner" | "client";
type Tone = "ok" | "bad" | "idle";
export type ServiceRow = { name: string; value: string; note: string; tone: Tone };

const row = (name: string, note: string, value: string, tone: Tone): ServiceRow => ({
  name,
  note,
  value,
  tone,
});

// One row for each service, from what the last answer said.
export function serviceRows(h: Health, viewer: Viewer): ServiceRow[] {
  const none = h.state === "backend";
  const waiting = h.state === "checking" && h.database === null;
  const unknown = (name: string, note: string) =>
    waiting ? row(name, note, "Checking", "idle") : row(name, note, "Unknown", "idle");

  const backend = (name: string, note: string) => {
    if (none) return row(name, note, "No answer", "bad");
    if (waiting) return row(name, note, "Checking", "idle");
    // A client user sees a stopped database as a Calyx that does not answer.
    if (h.database === "unavailable" && viewer === "client")
      return row(name, note, "No answer", "bad");
    return row(name, note, "Operational", "ok");
  };
  const core = (name: string, note: string) => {
    if (none || h.core === null || h.core === "unknown") return unknown(name, note);
    return h.core === "ok"
      ? row(name, note, "Operational", "ok")
      : row(name, note, "Stopped", "bad");
  };
  const database = (note: string) => {
    if (none || h.database === null) return unknown("Database", note);
    return h.database === "ok"
      ? row("Database", note, "Operational", "ok")
      : row("Database", note, "Stopped", "bad");
  };
  const mail = (note: string) => {
    if (none || h.mail === null) return unknown("Mail", note);
    return h.mail === "configured"
      ? row("Mail", note, "Configured", "ok")
      : row("Mail", note, "Off", "idle");
  };

  if (viewer === "client")
    return [
      backend("Calyx", "This page and your account"),
      core("WhatsApp service", "It links your number and sends the messages"),
    ];
  return [
    backend("Backend", "Users, clients and the API"),
    core("Core", "The connections to WhatsApp"),
    database("Where the backend keeps its data"),
    mail("Invitations, resets and alerts"),
  ];
}

const tones: Record<Tone, string> = {
  ok: "bg-ok text-ok",
  bad: "bg-bad text-bad",
  idle: "bg-idle text-idle",
};

const orUnknown = (v: string | undefined) => (v ? v : "unknown");

// The versions of the three parts. Both roles see them.
export function versionRows(h: Health): { name: string; value: string }[] {
  return [
    { name: "Panel", value: orUnknown(meta("calyx-version")) },
    { name: "Backend", value: orUnknown(h.version) },
    { name: "Core", value: orUnknown(h.coreVersion) },
  ];
}

export function checked(t: number, now: number): string {
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 2) return "checked just now";
  return `checked ${s} seconds ago`;
}

// One text, so it is the only child of its element.
function footer(health: Health, now: number): string | null {
  if (health.state === "ok" && health.lastGoodAt !== undefined)
    return checked(health.lastGoodAt, now);
  // With no time, the page shows no time.
  if (health.state === "ok") return null;
  if (health.state === "checking") return "First check in progress.";
  if (health.lastGoodAt === undefined) return "Trying again by itself.";
  const when = formatTime(health.lastGoodAt, now).text.replace(/^Today, /, "");
  return `Trying again by itself. Last good check at ${when}.`;
}

export function HealthCard({
  health,
  viewer,
  now,
}: {
  health: Health;
  viewer: Viewer;
  now: number;
}) {
  return (
    <PopoverContent
      data-slot="health-card"
      align="end"
      className="w-[min(330px,88vw)] gap-0 p-0 py-2"
    >
      {serviceRows(health, viewer).map((r) => (
        <div
          key={r.name}
          data-slot="health-service"
          className="grid grid-cols-[14px_1fr_auto] items-center gap-x-3 border-t border-border px-4 py-2 first:border-t-0"
        >
          <i className={cn("size-2.5 rounded-full", tones[r.tone].split(" ")[0])} />
          <b className="font-semibold">
            <Words text={r.name} />
          </b>
          <span className={cn("text-[0.9375rem] font-semibold", tones[r.tone].split(" ")[1])}>
            {r.value}
          </span>
          <small className="col-span-2 col-start-2 text-sm text-muted-foreground">{r.note}</small>
        </div>
      ))}
      <div data-slot="health-versions" className="border-t border-border px-4 pt-2">
        <b className="font-semibold">Versions</b>
        {versionRows(health).map((v) => (
          <div
            key={v.name}
            data-slot="health-version"
            className="flex justify-between text-sm text-muted-foreground"
          >
            <span>{v.name}</span>
            <span>{v.value}</span>
          </div>
        ))}
      </div>
      <p className="m-0 border-t border-border px-4 pt-2 pb-1 text-sm text-muted-foreground">
        {footer(health, now)}
      </p>
    </PopoverContent>
  );
}
