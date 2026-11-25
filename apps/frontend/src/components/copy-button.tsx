import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "./ui/button.tsx";

const failedText = "Copy did not work. Select the text and copy it.";

// Copies a text. The word "Copied" shows for 2 seconds and a screen reader hears it. A failed
// copy stays visible until the next attempt: no permission, or no clipboard on plain http.
// `liveRegion` uses `aria-live="polite"` and not `role="status"`, for a place that already has a
// status, such as the card of an instance. Its tests find the state of the connection by that role.
export function CopyButton({
  text,
  label = "Copy",
  liveRegion = false,
}: {
  text: string;
  label?: string;
  liveRegion?: boolean;
}) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    clearTimeout(timer.current);
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      setState("failed");
      return;
    }
    setState("copied");
    timer.current = setTimeout(() => setState("idle"), 2000);
  }

  return (
    <>
      <Button type="button" variant="outline" onClick={copy}>
        {state === "copied" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
        {label}
      </Button>
      <span
        {...(liveRegion ? { "aria-live": "polite" } : { role: "status" })}
        className={state === "failed" ? "block pt-1.5 font-semibold text-bad" : "sr-only"}
      >
        {state === "failed" ? failedText : state === "copied" ? "Copied" : ""}
      </span>
    </>
  );
}
