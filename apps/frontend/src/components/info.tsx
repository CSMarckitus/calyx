import { Info as InfoIcon } from "lucide-react";
import { type ReactNode, useRef, useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover.tsx";

// The small "i" next to a label. It opens on hover, focus and touch, and closes with Escape.
// The target has 24 px, because it stands inside a line of text.
export function Info({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  // Escape returns the focus to the button. That focus must not open the popover again.
  const skipFocus = useRef(false);
  return (
    <Popover
      open={open}
      onOpenChange={(next, details) => {
        if (!next && details.reason === "trigger-press") return;
        if (!next && details.reason === "escape-key") skipFocus.current = true;
        setOpen(next);
      }}
    >
      <PopoverTrigger
        openOnHover
        delay={0}
        aria-label={label}
        onFocus={() => {
          if (skipFocus.current) skipFocus.current = false;
          else setOpen(true);
        }}
        onBlur={() => {
          skipFocus.current = false;
        }}
        className="inline-grid size-6 place-items-center rounded-full align-middle text-muted-foreground hover:text-foreground"
      >
        <InfoIcon aria-hidden="true" className="size-4" />
      </PopoverTrigger>
      <PopoverContent className="w-[min(310px,78vw)] bg-foreground p-3 text-background">
        {children}
      </PopoverContent>
    </Popover>
  );
}
