import { useId } from "react";
import { cn } from "@/lib/utils";
import { generateSecret } from "../domain/webhooks.ts";
import type { SecretChoice } from "../domain/webhooks-draft.ts";
import { Field } from "./access-page.tsx";
import { ShownOnce } from "./shown-once.tsx";

export const refusalText =
  "Calyx cannot show the stored secret. Type it again, generate a new one, or select No signature.";

const actions = [
  ["keep", "Keep the secret"],
  ["type", "Type a secret"],
  ["generate", "Generate a secret"],
  ["none", "No signature"],
] as const;

type Kind = (typeof actions)[number][0];

// One group of four actions. `event` names the row in "One URL for each event".
export function SecretControl({
  event,
  choice,
  keepBlocked,
  error,
  onChange,
}: {
  event?: string;
  choice: SecretChoice;
  // After a refused save, "Keep the secret" would be refused again.
  keepBlocked: boolean;
  error?: string | null;
  onChange: (next: SecretChoice) => void;
}) {
  const name = useId();

  // Another action discards a typed or generated value. A new "Generate" makes a new value.
  function pick(kind: Kind) {
    if (kind === "generate") onChange({ kind, value: generateSecret(), saved: false });
    else if (kind === "type") onChange({ kind, value: "" });
    else onChange({ kind });
  }

  return (
    <fieldset
      data-refused={keepBlocked || undefined}
      className="m-0 grid min-w-0 gap-2 border-0 p-0"
    >
      <legend className="mb-1 p-0 font-semibold">
        Secret
        {event && (
          <span className="sr-only" translate="no">
            {` for ${event}`}
          </span>
        )}
      </legend>
      {keepBlocked && (
        <p role="alert" className="m-0 font-semibold text-bad">
          {refusalText}
        </p>
      )}
      <div className="flex flex-wrap gap-x-5">
        {actions.map(([kind, label]) => {
          const blocked = kind === "keep" && keepBlocked;
          return (
            <label
              key={kind}
              className={cn(
                "inline-flex min-h-11 cursor-pointer items-center gap-2",
                blocked && "cursor-not-allowed text-muted-foreground",
              )}
            >
              <input
                type="radio"
                name={name}
                className="size-4 accent-primary"
                checked={choice.kind === kind}
                disabled={blocked}
                onChange={() => pick(kind)}
                // A click on the checked radio fires no change, but must make a new value.
                onClick={
                  kind === "generate" && choice.kind === "generate"
                    ? () => pick("generate")
                    : undefined
                }
              />
              {label}
            </label>
          );
        })}
      </div>
      {choice.kind === "type" && (
        <Field
          label="Your secret"
          value={choice.value}
          onChange={(e) => onChange({ kind: "type", value: e.currentTarget.value })}
          translate="no"
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
          error={error}
        />
      )}
      {choice.kind === "generate" && (
        <div className="grid gap-2">
          <p className="m-0 font-semibold">Put it in your program first.</p>
          <ShownOnce
            key={choice.value}
            label="Generated secret"
            value={choice.value}
            saved={choice.saved}
            onSaved={() => onChange({ ...choice, saved: true })}
          />
        </div>
      )}
    </fieldset>
  );
}
