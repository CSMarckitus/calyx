import { type ReactNode, useId } from "react";
import type { components } from "../api/generated/types.ts";
import { Input } from "./ui/input.tsx";

// The parts of the webhooks view: one event of the form, and its URL field.

type Webhook = components["schemas"]["Webhook"];

const calyxWords: Record<string, [string, string]> = {
  "connection.update": [
    "The connection changes",
    "For example, this number connects or disconnects.",
  ],
  "message.received": ["A message arrives", "Somebody writes to this number."],
  "message.status": ["A sent message changes", "It was delivered, read, or it failed."],
  "bulk.finished": ["A bulk send ends", "All its messages were tried."],
};

// A raw event shows its name after `whatsmeow.`, or its id (`meta.webhook`).
export const wordsOf = (event: string) =>
  calyxWords[event]?.[0] ?? event.replace(/^whatsmeow\./, "");

export const signedText = (h: Webhook) => (h.has_secret ? "Signed" : "Not signed");

const plain = (url: string) => /^http:\/\//i.test(url.trim());

// One event: its checkbox and words, its id, its stored state, and the fields of its row.
export function HookRow({
  event,
  checked,
  stored,
  onToggle,
  children,
}: {
  event: string;
  checked: boolean;
  stored?: Webhook;
  onToggle: () => void;
  children?: ReactNode;
}) {
  const id = useId();
  const [words, about] = calyxWords[event] ?? [wordsOf(event), ""];
  return (
    <div
      data-slot="hook"
      className="grid grid-cols-[24px_minmax(0,1fr)] gap-x-3 gap-y-0.5 border-t border-border py-3.5 first:border-t-0"
    >
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={onToggle}
        className="mt-0.5 size-5 accent-primary"
      />
      <div className="grid gap-0.5">
        <label htmlFor={id} className="font-semibold">
          {words}
        </label>
        {about && <span className="text-sm text-muted-foreground">{about}</span>}
        {words !== event && (
          <span
            translate="no"
            className="font-mono text-[0.8125rem] text-muted-foreground [overflow-wrap:anywhere]"
          >
            {event}
          </span>
        )}
        {stored && <span className="text-sm">{signedText(stored)}</span>}
      </div>
      {children && <div className="col-start-2 mt-2 grid gap-3">{children}</div>}
    </div>
  );
}

export function PlainWarning({ url }: { url: string }) {
  return plain(url) ? (
    <p className="m-0 text-sm font-semibold text-wait">The calls go without encryption.</p>
  ) : null;
}

// The URL field of a row. Its name has the event id, so each field has its own label.
export function RowUrl({
  event,
  value,
  error,
  onChange,
}: {
  event: string;
  value: string;
  error?: string;
  onChange: (url: string) => void;
}) {
  const id = useId();
  return (
    <div className="grid gap-1.5">
      <Input
        aria-label={`URL for ${event}`}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-note` : undefined}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        inputMode="url"
        translate="no"
        spellCheck={false}
        autoComplete="off"
      />
      {error && (
        <span id={`${id}-note`} className="text-sm font-semibold text-bad">
          {error}
        </span>
      )}
      <PlainWarning url={value} />
    </div>
  );
}
