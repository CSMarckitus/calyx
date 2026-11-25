import { type FormEvent, useId, useState } from "react";
import { Risk } from "../risk.tsx";
import { Button } from "../ui/button.tsx";
import { Input } from "../ui/input.tsx";
import { Label } from "../ui/label.tsx";

// The API takes 1 to 100 characters, with no spaces at the start and the end.
const maxName = 100;

// "Name", with the risk of WhatsApp Web before the person continues.
export function NameStep({
  name,
  onContinue,
  onCancel,
}: {
  name: string;
  onContinue: (name: string) => void;
  onCancel: () => void;
}) {
  const [text, setText] = useState(name);
  const [error, setError] = useState<string | null>(null);
  const field = useId();
  const hint = useId();

  function submit(e: FormEvent) {
    e.preventDefault();
    const value = text.trim();
    if (value === "") setError("Enter a name.");
    else if (value.length > maxName) setError(`Use ${maxName} characters or less.`);
    else onContinue(value);
  }

  return (
    <form className="grid gap-4" onSubmit={submit} noValidate>
      <div className="grid gap-2">
        <Label htmlFor={field} className="text-base font-semibold">
          Name
        </Label>
        <Input
          id={field}
          value={text}
          autoComplete="off"
          aria-describedby={hint}
          aria-invalid={error !== null}
          onChange={(e) => {
            setText(e.target.value);
            setError(null);
          }}
        />
        <span
          id={hint}
          className={error ? "text-[0.9375rem] text-bad" : "text-[0.9375rem] text-muted-foreground"}
        >
          {error ?? "Only you see it. For example: Shop."}
        </span>
      </div>
      <div className="grid gap-1.5 rounded-md bg-wait-soft px-4 py-3.5 text-[0.9375rem]">
        <b className="font-semibold">Before you continue</b>
        <Risk className="m-0" />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="submit">Continue</Button>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
