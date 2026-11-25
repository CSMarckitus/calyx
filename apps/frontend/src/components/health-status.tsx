import { ChevronDown } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { type HealthState, useHealth } from "../api/health.ts";
import { checked, HealthCard, type Viewer } from "./health-card.tsx";
import { Popover, PopoverTrigger } from "./ui/popover.tsx";
import { Words } from "./words.tsx";

const texts: Record<HealthState, { owner: string; client: string }> = {
  backend: { owner: "Calyx does not answer", client: "Calyx does not answer" },
  database: { owner: "Database stopped", client: "Calyx does not answer" },
  core: { owner: "Core stopped", client: "Calyx cannot reach WhatsApp" },
  ok: { owner: "Backend and core fully operational", client: "Calyx is fully operational" },
  checking: { owner: "Checking Calyx", client: "Checking Calyx" },
};

// The text of the header for each state and viewer.
export const headerText = (state: HealthState, viewer: Viewer) => texts[state][viewer];

const pulse: Record<HealthState, string> = {
  ok: "bg-ok",
  core: "bg-bad",
  database: "bg-bad",
  backend: "bg-bad",
  checking: "bg-idle",
};

// Re-renders each second, only while the seconds show.
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

// The live region of the health status. The layout renders it once, outside the sidebar: at the phone
// width the sidebar is in the Sheet, which is not in the page while it is closed.
export function HealthRegion({ viewer }: { viewer: Viewer }) {
  const health = useHealth();
  // A screen reader hears the change of the state, not the seconds.
  return (
    <span role="status" data-slot="health-state" className="sr-only">
      <Words text={headerText(health.state, viewer)} />
    </span>
  );
}

export function HealthStatus({ viewer }: { viewer: Viewer }) {
  const health = useHealth();
  const [open, setOpen] = useState(false);
  // Escape returns the focus to the button. That focus must not open the card again.
  const skipFocus = useRef(false);
  const seconds = health.state === "ok" && health.lastGoodAt !== undefined;
  const now = useNow(seconds || open);
  const bad = health.state === "core" || health.state === "database" || health.state === "backend";

  return (
    <Popover
      open={open}
      onOpenChange={(next, details) => {
        // A press after a hover or a focus must not close the card that they opened.
        if (!next && details.reason === "trigger-press") return;
        if (!next && details.reason === "escape-key") skipFocus.current = true;
        setOpen(next);
      }}
    >
      <PopoverTrigger
        openOnHover
        delay={0}
        onFocus={() => {
          if (skipFocus.current) skipFocus.current = false;
          else setOpen(true);
        }}
        onBlur={() => {
          skipFocus.current = false;
        }}
        // The sidebar is narrow: the state is on the first line, the seconds and "Details" below it.
        className="grid min-h-11 w-full grid-cols-[auto_1fr] items-center gap-x-2 rounded-md px-3 py-1.5 text-left text-sm text-muted-foreground"
      >
        <i className={cn("size-2.5 rounded-full", pulse[health.state])} />
        <b className={cn("font-medium", bad ? "text-bad" : "text-foreground")}>
          <Words text={headerText(health.state, viewer)} />
        </b>
        <span className="col-start-2 flex flex-wrap items-center gap-x-2">
          {seconds && health.lastGoodAt !== undefined && (
            <span aria-hidden="true" className="tabular-nums">
              {checked(health.lastGoodAt, now)}
            </span>
          )}
          <span className="inline-flex items-center gap-1">
            Details
            <ChevronDown
              aria-hidden="true"
              className={cn("size-4 flex-none transition-transform", open && "rotate-180")}
            />
          </span>
        </span>
      </PopoverTrigger>
      <HealthCard health={health} viewer={viewer} now={now} />
    </Popover>
  );
}
