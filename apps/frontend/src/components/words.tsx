const literal = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// The translator of the browser keeps the word "Calyx" and the given names (of a person,
// a client or an organization) as they are. Each part is the only child of its element, so a
// translator that replaces text nodes does not stop React from writing a new text.
export function Words({ text, keep = [] }: { text: string; keep?: string[] }) {
  // The longest first, so a name that holds "Calyx" or a shorter name stays whole.
  const kept = ["Calyx", ...keep.filter((k) => k !== "")].sort((a, b) => b.length - a.length);
  return text.split(new RegExp(`(${kept.map(literal).join("|")})`)).map((part, i) =>
    part === "" ? null : (
      <span key={i} translate={kept.includes(part) ? "no" : undefined}>
        {part}
      </span>
    ),
  );
}
