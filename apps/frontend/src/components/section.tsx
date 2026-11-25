import { type ReactNode, useId } from "react";

// A section with a title is a region with that name.
export function Section({ title, children }: { title?: string; children: ReactNode }) {
  const id = useId();
  return (
    <section
      aria-labelledby={title ? id : undefined}
      className="grid max-w-[760px] gap-4 rounded-lg border border-border bg-card px-4.5 py-5 sm:px-6"
    >
      {title && <h2 id={id}>{title}</h2>}
      {children}
    </section>
  );
}
