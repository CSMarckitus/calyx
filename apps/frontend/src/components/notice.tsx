import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Words } from "./words.tsx";

export type NoticeTone = "bad" | "wait" | "ok" | "brand" | "idle";

const tones: Record<NoticeTone, string> = {
  bad: "bg-bad-soft",
  wait: "bg-wait-soft",
  ok: "bg-ok-soft",
  brand: "bg-accent",
  idle: "bg-idle-soft",
};

// A box with a title and a text. A problem or a warning is an alert. The rest is a status.
export function Notice({
  tone = "idle",
  title,
  children,
  className,
  keep,
}: {
  tone?: NoticeTone;
  title: ReactNode;
  children?: ReactNode;
  className?: string;
  // The names in a text title or text children.
  keep?: string[];
}) {
  const words = (n: ReactNode) => (typeof n === "string" ? <Words text={n} keep={keep} /> : n);
  return (
    <div
      data-slot="notice"
      role={tone === "bad" || tone === "wait" ? "alert" : "status"}
      className={cn("grid gap-1.5 rounded-md px-4 py-3.5 text-[0.9375rem]", tones[tone], className)}
    >
      <b className="font-semibold">{words(title)}</b>
      {children && <span>{words(children)}</span>}
    </div>
  );
}
