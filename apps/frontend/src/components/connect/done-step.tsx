import type { Health } from "../../api/health.ts";
import type { Color } from "../../domain/labels.ts";
import { StateWord } from "../instance-card.tsx";
import { Notice } from "../notice.tsx";
import { Button } from "../ui/button.tsx";
import { Words } from "../words.tsx";

export type MailTo = { viewer: "owner" | "client"; email: string; manager: string };

// Who gets the mail of a disconnection, from `mail` of /healthz and the role.
// With no answer of /healthz the page does not know, so it says nothing.
export function MailNote({ mail, to }: { mail: Health["mail"]; to: MailTo }) {
  if (mail === null) return null;
  if (mail === "off")
    return (
      <Notice tone="idle" title="This server sends no mail">
        Look at this page to see if the number loses its connection.
      </Notice>
    );
  return (
    <Notice tone="idle" title="If this number loses its connection" keep={[to.email, to.manager]}>
      {to.viewer === "owner"
        ? `Calyx sends a mail to you, at ${to.email}. A short cut sends no mail: Calyx waits 5 minutes.`
        : `Calyx sends a mail to the organization admin, ${to.manager}, so that they can tell you. A short cut sends no mail.`}
    </Notice>
  );
}

// "Connected", or "The phone is linked" with the word of the state when the page saw the end only
// by a read.
export function DoneStep({
  name,
  linked,
  mail,
  to,
  onDone,
}: {
  name: string;
  linked?: { label: string; color: Color };
  mail: Health["mail"];
  to: MailTo;
  onDone: () => void;
}) {
  return (
    <div className="grid justify-items-start gap-3.5">
      {linked ? (
        <div className="grid gap-2">
          <h3>The phone is linked</h3>
          <div role="status">
            <StateWord color={linked.color}>{linked.label}</StateWord>
          </div>
        </div>
      ) : (
        <div role="status">
          <h3>
            <span translate="no">{name}</span> <span>is connected</span>
          </h3>
        </div>
      )}
      <p className="m-0 text-muted-foreground">
        <Words text="Keep WhatsApp installed on the phone. If it unlinks Calyx, you can reconnect here." />
      </p>
      <MailNote mail={mail} to={to} />
      <Button onClick={onDone}>Done</Button>
    </div>
  );
}
