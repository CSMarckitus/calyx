// Adapted from the shadcn Trace, by hand. Do not write over it with the CLI.
// What changed, and why:
// - An ordered list with aria-current="step", so a screen reader gives the position.
// - The state "done" has a check, and each state has a word for people who do not see color.
// - A line joins the points, and long titles wrap.
// - No shimmer. The motion of a fold is in the screens, and reduced motion removes it.
// - A complete trace keeps the children of the step `current`: the result of the last step.
import { Check } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type TraceStep = {
  id: string;
  title: string;
  // Shown after the title of a done step.
  value?: ReactNode;
  // A control after the value, for example "Change".
  action?: ReactNode;
  // Shown under the current step only. When the trace is complete, under the step `current`:
  // the result stays visible.
  children?: ReactNode;
};

type State = "done" | "current" | "next" | "failed";

const word: Record<State, string> = {
  done: "Done",
  current: "Current step",
  next: "Next step",
  failed: "Step failed",
};

export function Trace({
  steps,
  current,
  complete = false,
  failed = false,
  className,
}: {
  steps: TraceStep[];
  current: number;
  complete?: boolean;
  failed?: boolean;
  className?: string;
}) {
  return (
    <ol className={cn("m-0 list-none p-0", className)}>
      {steps.map((step, i) => {
        const state: State =
          complete || i < current
            ? "done"
            : i === current
              ? failed
                ? "failed"
                : "current"
              : "next";
        const here = !complete && i === current;
        const last = i === steps.length - 1;
        return (
          <li
            key={step.id}
            data-state={state}
            aria-current={here ? "step" : undefined}
            className={cn("relative grid grid-cols-[26px_minmax(0,1fr)] gap-x-4", !last && "pb-5")}
          >
            {!last && (
              <span
                aria-hidden="true"
                className={cn(
                  "absolute top-[30px] bottom-0.5 left-3 w-0.5",
                  state === "done" ? "bg-primary" : "bg-border",
                )}
              />
            )}
            <span
              aria-hidden="true"
              className={cn(
                "grid size-[26px] place-items-center rounded-full border-2 bg-card text-primary-foreground",
                state === "done" && "border-primary bg-primary",
                state === "current" && "border-primary",
                state === "failed" && "border-destructive",
                state === "next" && "border-border",
              )}
            >
              {state === "done" && <Check className="size-3.5" />}
              {state === "current" && <span className="size-2.5 rounded-full bg-primary" />}
              {state === "failed" && <span className="size-2.5 rounded-full bg-destructive" />}
            </span>
            <div className="flex min-h-[26px] flex-wrap items-baseline gap-x-3">
              <b
                className={cn(
                  "font-semibold",
                  state === "next" && "font-medium text-muted-foreground",
                )}
              >
                {step.title}
              </b>
              {state === "done" && step.value != null && (
                <span className="break-words text-muted-foreground">{step.value}</span>
              )}
              {state === "done" && step.action != null && (
                <span className="ml-auto">{step.action}</span>
              )}
              <span className="sr-only">{word[state]}</span>
            </div>
            {i === current && step.children != null && (
              <div className="col-start-2 mt-3.5">{step.children}</div>
            )}
          </li>
        );
      })}
    </ol>
  );
}
