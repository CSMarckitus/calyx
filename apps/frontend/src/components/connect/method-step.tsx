import { type ReactNode, useId, useMemo, useState } from "react";
import { dialCode } from "../../domain/countries.ts";
import { countryChoices } from "../../domain/locale.ts";
import { fullNumber, isPhoneValid } from "../../domain/phone.ts";
import { Button } from "../ui/button.tsx";
import { Input } from "../ui/input.tsx";
import { Label } from "../ui/label.tsx";
import { selectClass } from "./options-step.tsx";
import { Choice } from "./use-step.tsx";

export type Method = "code" | "qr";
export type PhoneDraft = { country: string; text: string };

// "Where is WhatsApp?": a code for this device, or a QR code for another phone. A code needs the
// phone number: the country gives its dialling code.
export function MethodStep({
  method,
  phone,
  disabled,
  notice,
  onChange,
  onStart,
}: {
  method: Method;
  phone: PhoneDraft;
  // Calyx cannot reach the core, or a start runs.
  disabled: boolean;
  // What the last start said: a refusal, or an answer that did not come.
  notice: ReactNode;
  onChange: (method: Method, phone: PhoneDraft) => void;
  onStart: () => void;
}) {
  const countries = useMemo(() => countryChoices().filter((c) => c.dial !== ""), []);
  const [error, setError] = useState(false);
  const ids = { country: useId(), phone: useId(), hint: useId() };

  function start() {
    if (method === "code" && !isPhoneValid(fullNumber(phone.text, dialCode(phone.country)))) {
      setError(true);
      return;
    }
    onStart();
  }

  return (
    <div className="grid gap-4">
      <fieldset
        aria-label="Is WhatsApp on this device?"
        className="m-0 min-w-0 border-0 p-0 grid gap-3 sm:grid-cols-2"
      >
        <Choice
          pressed={method === "code"}
          title="Yes, on this device"
          text="You get a code to type in WhatsApp."
          onClick={() => onChange("code", phone)}
        />
        <Choice
          pressed={method === "qr"}
          title="No, on another phone"
          text="You scan a QR code with that phone."
          onClick={() => onChange("qr", phone)}
        />
      </fieldset>
      {method === "code" && (
        <div className="grid gap-2">
          <Label htmlFor={ids.phone} className="text-base font-semibold">
            Phone number
          </Label>
          <div className="grid grid-cols-[minmax(0,9rem)_minmax(0,1fr)] gap-2">
            <select
              id={ids.country}
              aria-label="Country code"
              className={selectClass}
              value={phone.country}
              onChange={(e) => {
                setError(false);
                onChange(method, { ...phone, country: e.target.value });
              }}
            >
              <option value="">Country</option>
              {countries.map((c) => (
                <option key={c.code} value={c.code}>
                  {`${c.name} +${c.dial}`}
                </option>
              ))}
            </select>
            <Input
              id={ids.phone}
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              value={phone.text}
              aria-describedby={ids.hint}
              aria-invalid={error}
              onChange={(e) => {
                setError(false);
                onChange(method, { ...phone, text: e.target.value });
              }}
            />
          </div>
          <span
            id={ids.hint}
            className={
              error ? "text-[0.9375rem] text-bad" : "text-[0.9375rem] text-muted-foreground"
            }
          >
            {error
              ? "Enter the phone number with its country. It has 7 to 15 digits."
              : "In most countries, leave out the first 0."}
          </span>
        </div>
      )}
      {notice}
      <div className="flex flex-wrap gap-2">
        <Button disabled={disabled} onClick={start}>
          {method === "code" ? "Get the code" : "Show the QR code"}
        </Button>
      </div>
    </div>
  );
}
