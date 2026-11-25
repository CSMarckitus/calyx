import { Fragment } from "react";
import { risk } from "../domain/risk.ts";

// The risk of WhatsApp Web, one paragraph each. The translator of the browser keeps the word
// "Calyx".
export function Risk({ className }: { className?: string }) {
  return risk.map((text) => (
    <p key={text} className={className}>
      {text.split("Calyx").map((part, i) => (
        <Fragment key={part}>
          {i > 0 && <span translate="no">Calyx</span>}
          {part}
        </Fragment>
      ))}
    </p>
  ));
}
