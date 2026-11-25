import { type FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import type { components } from "../api/generated/types.ts";
import { useChange, useCheckAgain } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { formatTime } from "../domain/time.ts";
import { Field, fieldFailed } from "./access-page.tsx";
import { CopyButton } from "./copy-button.tsx";
import { Failure } from "./failure.tsx";
import { Notice } from "./notice.tsx";
import { RemoveUser } from "./remove-user.tsx";
import { Button } from "./ui/button.tsx";

type Client = components["schemas"]["Client"];

function expiry(at: string, now: number) {
  const t = Date.parse(at);
  const { text, title } = formatTime(t, now);
  const when = /^(Today|Yesterday)/.test(text)
    ? text.replace(/^\w/, (c) => c.toLowerCase())
    : `on ${text}`;
  return { text: `${t > now ? "It expires" : "It expired"} ${when}.`, title };
}

// The access of a client: its user, or the pending invitation, or the form of a new one.
// The API gives the link only in the answer that makes it, so the page shows it once.
export function Invitation({
  client,
  mail,
  acknowledged,
}: {
  client: Client;
  // The server has a mail server.
  mail: boolean;
  // The owner accepted the terms of WhatsApp Web.
  acknowledged: boolean;
}) {
  const invite = useChange("inviteClientUser");
  const cancel = useChange("cancelInvitation");
  // Here and not in RemoveUser: after the removal the client has no user, and the answer stays.
  const remove = useChange("removeClientAccess");
  const again = useCheckAgain();
  const [link, setLink] = useState<string | null>(null);
  const pending = client.pending_invitation;
  // After a removal, the button is gone and the focus would fall to the body. The email field
  // of the new invitation takes it.
  const emailField = useRef<HTMLInputElement>(null);
  const removing = useRef(false);
  useEffect(() => {
    if (!removing.current || client.user_email !== null) return;
    removing.current = false;
    emailField.current?.focus();
  }, [client.user_email]);

  function send(email: string, form?: HTMLFormElement) {
    setLink(null);
    cancel.reset();
    remove.reset();
    invite.mutate(
      { path: { id: client.id }, body: { email } },
      {
        onSuccess: (answer) => {
          setLink(answer?.invite_link ?? null);
          form?.reset();
        },
      },
    );
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    send(String(new FormData(e.currentTarget).get("email")).trim(), e.currentTarget);
  }

  const removeFailure = remove.isError && (
    <Failure
      failure={remove.error}
      context={{ operationId: "removeClientAccess", role: "owner" }}
      checking={remove.checking}
    />
  );

  if (client.user_email !== null) {
    return (
      <>
        <p className="m-0">
          <span translate="no">{client.user_email}</span> <span>is the user of this client.</span>
        </p>
        {removeFailure}
        <RemoveUser
          client={client}
          email={client.user_email}
          busy={remove.isPending}
          onRemove={() => {
            removing.current = true;
            remove.mutate(
              { path: { id: client.id } },
              {
                // A 404 means the user is gone, and the focus moves. Any other failure keeps it.
                onError: (error) => {
                  const failure = error as unknown as RequestFailure;
                  if (failure.kind !== "problem" || failure.status !== 404)
                    removing.current = false;
                },
              },
            );
          }}
        />
      </>
    );
  }

  const failure = removeFailure ? (
    removeFailure
  ) : invite.isError ? (
    <Failure
      failure={invite.error}
      context={{ operationId: "inviteClientUser", role: "owner" }}
      checking={invite.checking}
    />
  ) : cancel.isError ? (
    <Failure
      failure={cancel.error}
      context={{ operationId: "cancelInvitation", role: "owner" }}
      checking={cancel.checking}
    />
  ) : null;

  const warning = !acknowledged && (
    <Notice tone="wait" title="This client cannot connect a number yet.">
      <span>The WhatsApp Web terms are not accepted.</span>{" "}
      <Link to="/organization/whatsapp-web" className="font-semibold text-primary underline">
        Read the WhatsApp Web terms
      </Link>
    </Notice>
  );

  if (pending === null) {
    return (
      <>
        {warning}
        <p className="m-0">No invitation is open.</p>
        <form onSubmit={submit} noValidate className="grid gap-4">
          <Field
            label="Email"
            name="email"
            type="email"
            placeholder="name@company.example"
            autoComplete="off"
            translate="no"
            ref={emailField}
            error={fieldFailed(invite.error, "email") ? "Write an email address." : null}
            required
          />
          {failure}
          <Button type="submit" className="justify-self-start" disabled={invite.isPending}>
            Send invitation
          </Button>
        </form>
      </>
    );
  }

  const ends = expiry(pending.expires_at, Date.now());
  // One change of the invitation at a time.
  const busy = invite.isPending || cancel.isPending;
  return (
    <>
      {warning}
      <p className="m-0">
        <span>Invitation sent to</span> <b translate="no">{pending.email}</b>
        <span>.</span> <span title={ends.title}>{ends.text}</span>
      </p>
      {link ? (
        <div className="grid gap-2 rounded-md bg-accent px-4 py-3.5 text-[0.9375rem]">
          <b className="font-semibold">
            {mail ? "The mail was not sent." : "This server sends no mail."}
          </b>
          <span>Copy this link and send it to the person. It shows only once.</span>
          <code data-slot="invite-link" translate="no" className="font-mono break-all">
            {link}
          </code>
          <div>
            <CopyButton text={link} label="Copy the link" />
          </div>
        </div>
      ) : (
        mail && (
          <>
            {invite.isSuccess && <Notice tone="ok" title="The mail is sent." />}
            <p className="m-0 text-sm text-muted-foreground">
              If the mail did not arrive, ask them to check spam.
            </p>
          </>
        )
      )}
      {failure}
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" disabled={busy} onClick={() => send(pending.email)}>
          {mail ? "Send again" : "Make a new link"}
        </Button>
        {/* No event comes when the person accepts. */}
        <Button variant="outline" disabled={again.pending} onClick={() => void again.check()}>
          Check again
        </Button>
        <Button
          variant="destructive"
          disabled={busy}
          onClick={() => {
            invite.reset();
            remove.reset();
            cancel.mutate({ path: { id: client.id } }, { onSuccess: () => setLink(null) });
          }}
        >
          Cancel invitation
        </Button>
      </div>
    </>
  );
}
