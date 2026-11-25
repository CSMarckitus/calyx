import { Check } from "lucide-react";
import { type ReactNode, type RefObject, useEffect, useRef, useState } from "react";
import { Field } from "./access-page.tsx";
import { CopyButton } from "./copy-button.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./ui/dialog.tsx";

export const savedQuestion = "Did you save it? You cannot see it again.";

// The value, selectable and with no help of the browser. The page cannot read it again, so it
// lives only in the state of the component that shows it.
function Value({
  label,
  value,
  field,
}: {
  label: string;
  value: string;
  field: RefObject<HTMLInputElement | null>;
}) {
  return (
    <div className="grid gap-2">
      <Field
        ref={field}
        label={label}
        value={value}
        readOnly
        translate="no"
        spellCheck={false}
        autoComplete="off"
        className="font-mono"
        onFocus={(e) => e.currentTarget.select()}
      />
      <div>
        <CopyButton text={value} />
      </div>
    </div>
  );
}

// The inline form, inside a form of the page. The form asks its own question before it closes.
// The value stays after "I saved it" until the form closes it; the button then stays pressed.
export function ShownOnce({
  label,
  value,
  saved = false,
  onSaved,
}: {
  label: string;
  value: string;
  saved?: boolean;
  onSaved: () => void;
}) {
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  return (
    <div className="grid gap-2">
      <Value label={label} value={value} field={field} />
      <Button
        type="button"
        variant={saved ? "outline" : "default"}
        aria-pressed={saved}
        className="justify-self-start"
        onClick={onSaved}
      >
        {saved && <Check aria-hidden="true" />}I saved it
      </Button>
    </div>
  );
}

// The dialog form. Escape, a click outside and "Close" ask the question first.
export function ShownOnceDialog({
  title,
  description,
  label,
  value,
  onSaved,
  finalFocus,
  children,
}: {
  title: string;
  description: string;
  label: string;
  value: string;
  onSaved: () => void;
  // The element that gets the focus when the dialog closes, if the opener is gone.
  finalFocus?: RefObject<HTMLElement | null>;
  children?: ReactNode;
}) {
  const field = useRef<HTMLInputElement>(null);
  const back = useRef<HTMLButtonElement>(null);
  const done = useRef<HTMLButtonElement>(null);
  const asked = useRef(false);
  const [asking, setAsking] = useState(false);
  // The button that had the focus unmounts, so the focus moves by hand and never falls to the body.
  useEffect(() => {
    if (asking) {
      asked.current = true;
      back.current?.focus();
    } else if (asked.current) done.current?.focus();
  }, [asking]);

  return (
    <Dialog open onOpenChange={(open) => !open && setAsking(true)}>
      <DialogContent
        initialFocus={field}
        finalFocus={finalFocus}
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl"
      >
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        <Value label={label} value={value} field={field} />
        {children}
        <DialogFooter>
          {asking ? (
            <div role="alert" className="grid w-full gap-3">
              <b className="font-semibold">{savedQuestion}</b>
              <div className="flex flex-wrap gap-2">
                <Button onClick={onSaved}>Yes, close</Button>
                <Button ref={back} variant="outline" onClick={() => setAsking(false)}>
                  Go back
                </Button>
              </div>
            </div>
          ) : (
            <Button ref={done} onClick={onSaved}>
              I saved it
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
