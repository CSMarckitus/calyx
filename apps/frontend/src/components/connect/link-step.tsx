import { Fragment, type ReactNode, useState } from "react";
import { cn } from "@/lib/utils";
import type { PairingView } from "../../api/pairing.ts";
import { Button } from "../ui/button.tsx";
import { Words } from "../words.tsx";
import { guessPhone, type Phone, phoneNames, phoneSteps } from "./phone-steps.ts";
import { Qr } from "./qr.tsx";

// WhatsApp shows a code of 8 characters as two groups of 4.
export const showCode = (code: string) =>
  code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;

// A name between asterisks is in bold, with no HTML parsing.
function Marked({ text }: { text: string }) {
  return text
    .split("*")
    .map((part, i) => (i % 2 === 1 ? <b key={i}>{part}</b> : <Fragment key={i}>{part}</Fragment>));
}

function PhoneSteps({ method }: { method: "code" | "qr" }) {
  const [phone, setPhone] = useState<Phone>(guessPhone);
  return (
    <div className="grid gap-3">
      <fieldset aria-label="Your phone" className="m-0 min-w-0 border-0 p-0 flex flex-wrap gap-2">
        {(Object.keys(phoneNames) as Phone[]).map((p) => (
          <Button
            key={p}
            variant={p === phone ? "default" : "outline"}
            aria-pressed={p === phone}
            onClick={() => setPhone(p)}
          >
            {phoneNames[p]}
          </Button>
        ))}
      </fieldset>
      <ol className="m-0 grid gap-2 pl-5">
        {phoneSteps(phone, method).map((s) => (
          <li key={s}>
            <Marked text={s} />
          </li>
        ))}
      </ol>
    </div>
  );
}

function Spinner() {
  return (
    <i
      aria-hidden="true"
      className="size-[18px] shrink-0 animate-spin rounded-full border-3 border-border border-t-primary motion-reduce:animate-none"
    />
  );
}

// A text that waits: the spinner, a title, and an explanation.
export function Waiting({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div role="status" className="grid gap-1.5 rounded-md bg-accent px-4 py-3.5 text-[0.9375rem]">
      <span className="flex items-center gap-2.5">
        <Spinner />
        <b className="font-semibold">
          <Words text={title} />
        </b>
      </span>
      {children && (
        <span>{typeof children === "string" ? <Words text={children} /> : children}</span>
      )}
    </div>
  );
}

// "Link the phone" while an attempt runs: the code or the QR code, the steps in WhatsApp, the other
// method and Cancel.
export function LinkStep({
  view,
  method,
  number,
  onOther,
  onCancel,
}: {
  view: Extract<
    PairingView,
    { step: "starting" | "waiting" | "code" | "qr" | "checking" | "cancelling" }
  >;
  method: "code" | "qr";
  // The complete phone number of this tab. A second tab has none: it shows the code alone.
  number: string | null;
  onOther: () => void;
  onCancel: () => void;
}) {
  const busy = view.step === "cancelling" || view.step === "starting";
  const shownMethod = view.step === "code" ? "code" : view.step === "qr" ? "qr" : method;
  return (
    <div className="grid gap-4">
      {view.step === "code" && (
        <>
          {number && (
            <p className="m-0">
              Code for <b translate="no">{number}</b>
            </p>
          )}
          <div
            data-slot="pairing-code"
            translate="no"
            className="rounded-md bg-muted px-2.5 py-6 text-center font-mono text-[clamp(2rem,11vw,3.25rem)] leading-none font-semibold tracking-[0.06em] tabular-nums"
          >
            {showCode(view.code)}
          </div>
        </>
      )}
      {view.step === "qr" && <Qr text={view.qr} />}
      {(view.step === "code" || view.step === "qr") && (
        <>
          <PhoneSteps method={shownMethod} />
          <Waiting title="Waiting for your phone">
            You have about 2 minutes. You can close this panel and continue later. After you{" "}
            {view.step === "code" ? "type the code" : "scan"}, wait here: it can take a minute.
          </Waiting>
        </>
      )}
      {view.step === "starting" && <Waiting title="Asking WhatsApp for the code" />}
      {view.step === "waiting" && <Waiting title="Waiting for WhatsApp" />}
      {view.step === "checking" && (
        <Waiting title="Checking the link">Calyx asks again in some seconds.</Waiting>
      )}
      {view.step === "cancelling" && <Waiting title="Cancelling" />}
      <div className={cn("flex flex-wrap gap-2")}>
        {(view.step === "code" || view.step === "qr" || view.step === "waiting") && (
          <Button variant="outline" disabled={busy} onClick={onOther}>
            {shownMethod === "code" ? "Use a QR code" : "Use a code"}
          </Button>
        )}
        <Button variant="outline" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// The end of "Link the phone" with no link: a failure, or what a read of the instance says.
export function LinkResult({
  title,
  text,
  failure,
  onRetry,
  onCancel,
}: {
  title?: string;
  text?: string;
  // The failure of the pairing, from `Failure`.
  failure?: ReactNode;
  onRetry?: () => void;
  onCancel?: () => void;
}) {
  return (
    <div className="grid gap-4">
      {failure}
      {title && (
        <div
          role="alert"
          className="grid gap-1.5 rounded-md bg-bad-soft px-4 py-3.5 text-[0.9375rem]"
        >
          <b className="font-semibold">{title}</b>
          {text && <span>{text}</span>}
        </div>
      )}
      {(onRetry || onCancel) && (
        <div className="flex flex-wrap gap-2">
          {onRetry && <Button onClick={onRetry}>Try again</Button>}
          {onCancel && (
            <Button variant="outline" onClick={onCancel}>
              Cancel
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
