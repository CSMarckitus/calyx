import { type FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Field, fieldFailed } from "@/components/access-page";
import { Failure } from "@/components/failure";
import { ConnectMcp, ConnectProgram } from "@/components/program-help";
import { Section } from "@/components/section";
import { ShownOnceDialog } from "@/components/shown-once";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import type { components } from "../api/generated/types.ts";
import { isLost, useApiKeys, useChange, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import type { Role } from "../domain/errors.ts";
import { formatTime } from "../domain/time.ts";

type ApiKey = components["schemas"]["ApiKey"];
type Context = { role: Role; manager: string };

const revokeText =
  "Programs that use this key stop working. Webhooks that a program set with this key keep working: check the webhooks of each instance. You cannot undo this.";

function Time({ label, at, now }: { label: string; at: string; now: number }) {
  const t = formatTime(Date.parse(at), now);
  return (
    <span title={t.title}>
      {label} {t.text}
    </span>
  );
}

function KeyLine({ apiKey, now }: { apiKey: ApiKey; now: number }) {
  return (
    <div className="grid min-w-0 gap-1">
      <div className="flex flex-wrap items-baseline gap-x-3">
        <b className="font-semibold break-words [overflow-wrap:anywhere]" translate="no">
          {apiKey.name}
        </b>
        <code translate="no">cx_{apiKey.prefix}...</code>
      </div>
      <div className="flex flex-wrap gap-x-4 text-sm text-muted-foreground">
        <Time label="Created" at={apiKey.created_at} now={now} />
        {apiKey.last_used_at ? (
          <Time label="Last used" at={apiKey.last_used_at} now={now} />
        ) : (
          <span>Never used</span>
        )}
      </div>
    </div>
  );
}

function RevokeButton({ onClick }: { onClick: () => void }) {
  return (
    <Button variant="destructive" className="justify-self-start" onClick={onClick}>
      Revoke
    </Button>
  );
}

// `/developers/api-keys`: the keys of the person, the create form and the help for programs.
export function ApiKeysScreen() {
  const me = useMe().data;
  const read = useApiKeys();
  const create = useChange("createAPIKey");
  const revoke = useChange("revokeAPIKey");
  // The new key lives only here. After reset() the mutation cache can still keep
  // the answer for its gcTime (5 minutes), which is accepted.
  const [shown, setShown] = useState<string | null>(null);
  // The name and the keys before the request: after a lost answer, a key with that name that
  // is not in this set is from the request.
  const [sent, setSent] = useState<{ name: string; before: Set<string> } | null>(null);
  const [asking, setAsking] = useState<ApiKey | null>(null);
  // One create at a time, from the read before the request to the answer. Cancel is off for
  // that time: a reset would lose the answer, and the new key with it.
  const [saving, setSaving] = useState(false);
  const busy = useRef(false);
  // After a revoke or a saved key, the focused element goes away, so the focus goes to
  // "Create API key" and not to the body.
  const createButton = useRef<HTMLButtonElement>(null);
  // The form opens in the section of the keys. Cancel and a saved key close it.
  const [creating, setCreating] = useState(false);
  // The button is not there while the form is open, so the focus returns when it renders again.
  const wasCreating = useRef(false);
  useEffect(() => {
    if (!creating && wasCreating.current) createButton.current?.focus();
    wasCreating.current = creating;
  }, [creating]);
  const confirmed = useRef(false);
  if (!me) return null;
  const context: Context = {
    role: me.user.role === "owner" ? "owner" : "client",
    manager: me.organization.name,
  };
  const items = read.data?.items ?? [];
  const now = Date.now();

  const lost = create.isError && isLost(create.error);
  const found =
    lost && sent ? items.filter((k) => k.name === sent.name && !sent.before.has(k.id)) : [];

  // The page reads the list just before the request. No event tells of a key from another tab,
  // so only a fresh list shows which keys are older than the request.
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy.current) return;
    busy.current = true;
    setSaving(true);
    const form = e.currentTarget;
    const name = String(new FormData(form).get("name")).trim();
    const fresh = await read.refetch();
    const done = () => {
      busy.current = false;
      setSaving(false);
    };
    // A failed read shows its failure above the list.
    if (!fresh.isSuccess || !fresh.data) return done();
    setSent({ name, before: new Set(fresh.data.items.map((k) => k.id)) });
    create.mutate(
      { body: { name } },
      {
        onSuccess: (answer) => {
          setShown(answer?.token ?? null);
          create.reset();
          setCreating(false);
        },
        onSettled: done,
      },
    );
  }

  function cancel() {
    setCreating(false);
    create.reset();
  }

  function ask(key: ApiKey) {
    confirmed.current = false;
    setAsking(key);
  }

  function revokeNow(key: ApiKey) {
    confirmed.current = true;
    setAsking(null);
    revoke.mutate(
      { path: { id: key.id } },
      {
        // The notice of a lost answer ends with its last key.
        onSuccess: () => {
          if (found.length > 0 && found.every((k) => k.id === key.id)) create.reset();
        },
      },
    );
  }

  // A read throws its failure (queries.ts).
  const readFailure = read.error as unknown as RequestFailure | null;

  return (
    <>
      <h1>API keys</h1>

      <Section title="Your API keys">
        <p className="m-0 text-muted-foreground">"Last used" changes at most once a minute.</p>
        {revoke.isError && (
          <Failure
            failure={revoke.error}
            context={{ operationId: "revokeAPIKey", ...context }}
            checking={revoke.checking}
          />
        )}
        {readFailure && (
          <Failure failure={readFailure} context={{ operationId: "listAPIKeys", ...context }} />
        )}
        {/* A failed read keeps the last list (Shape). */}
        {read.data && items.length > 0 && (
          <ul className="m-0 grid list-none p-0">
            {items.map((k) => (
              <li
                key={k.id}
                data-slot="api-key"
                className="grid gap-2 border-t border-border py-3 first:border-t-0 first:pt-0 sm:grid-cols-[1fr_auto] sm:items-center"
              >
                <KeyLine apiKey={k} now={now} />
                <RevokeButton onClick={() => ask(k)} />
              </li>
            ))}
          </ul>
        )}
        {read.data && (
          <p className="m-0">
            {items.length === 0 && "You have no API keys. "}A key lets your software use Calyx. To
            connect your program, read{" "}
            <Link to="/developers/connect" className="font-semibold text-primary underline">
              Connect a program
            </Link>
            .
          </p>
        )}
        {creating && (
          <form onSubmit={(e) => void submit(e)} noValidate className="grid gap-4">
            <Field
              autoFocus
              label="Name"
              name="name"
              maxLength={100}
              autoComplete="off"
              translate="no"
              error={fieldFailed(create.error, "name") ? "Use 1 to 100 characters." : null}
              required
            />
            {lost && found.length > 0 ? (
              <div
                data-slot="lost-keys"
                role="alert"
                className="grid gap-3 rounded-md bg-wait-soft px-4 py-3.5 text-[0.9375rem]"
              >
                <b className="font-semibold">
                  Calyx did not answer. Revoke these keys and create a new one.
                </b>
                <ul className="m-0 grid gap-3 p-0">
                  {found.map((k) => (
                    <li key={k.id} className="grid gap-2 sm:grid-cols-[1fr_auto] sm:items-center">
                      <KeyLine apiKey={k} now={now} />
                      <RevokeButton onClick={() => ask(k)} />
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              create.isError && (
                <Failure
                  failure={create.error}
                  context={{ operationId: "createAPIKey", ...context }}
                  checking={create.checking}
                />
              )
            )}
            <div className="flex flex-wrap gap-2">
              <Button type="submit" disabled={!read.data || saving}>
                Save
              </Button>
              <Button type="button" variant="outline" disabled={saving} onClick={cancel}>
                Cancel
              </Button>
            </div>
          </form>
        )}
        {!creating && (
          <Button
            ref={createButton}
            className="justify-self-start"
            onClick={() => setCreating(true)}
          >
            Create API key
          </Button>
        )}
      </Section>

      {shown && (
        <ShownOnceDialog
          title="Your new API key"
          description="Copy the key now. Calyx shows it only once."
          label="Key"
          value={shown}
          onSaved={() => setShown(null)}
          finalFocus={createButton}
        >
          <ConnectProgram level={3} apiKey={shown} />
          <ConnectMcp level={3} owner={context.role === "owner"} />
        </ShownOnceDialog>
      )}

      <Dialog open={asking !== null} onOpenChange={(open) => !open && setAsking(null)}>
        <DialogContent
          showCloseButton={false}
          finalFocus={() => (confirmed.current ? createButton.current : true)}
        >
          <DialogTitle>
            <span>Revoke</span> <span translate="no">{asking?.name}</span>
            <span>?</span>
          </DialogTitle>
          <DialogDescription>{revokeText}</DialogDescription>
          <DialogFooter>
            <Button
              variant="destructive"
              disabled={revoke.isPending}
              onClick={() => asking && revokeNow(asking)}
            >
              Revoke
            </Button>
            <Button variant="outline" onClick={() => setAsking(null)}>
              Cancel
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
