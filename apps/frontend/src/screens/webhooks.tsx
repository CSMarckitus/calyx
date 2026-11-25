import { ChevronDown } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Field } from "@/components/access-page";
import { Choice } from "@/components/connect/use-step";
import { Failure } from "@/components/failure";
import { HookRow, PlainWarning, RowUrl, signedText, wordsOf } from "@/components/hook-row";
import type { Instance } from "@/components/instance-card";
import { Notice } from "@/components/notice";
import { SecretControl } from "@/components/secret-control";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import type { components } from "../api/generated/types.ts";
import { useChange, useMe } from "../api/queries.ts";
import { type Failure as RequestFailure, request } from "../api/request.ts";
import {
  calyxEvents,
  type Effect,
  matchesPlan,
  planWebhooks,
  rawEvents,
} from "../domain/webhooks.ts";
import {
  choiceOf,
  type Draft,
  draftOf,
  fieldErrors,
  formOf,
  keepRefused,
  maxWebhooks,
  type SecretChoice,
  sameList,
  secretsOf,
  unsavedSecret,
  withMode,
} from "../domain/webhooks-draft.ts";

type Webhook = components["schemas"]["Webhook"];

// The views of an instance, after `/instances/:id/`.
export const views = ["webhooks", "bulk-jobs"];

const tooManyText = "An instance can have at most 20 webhooks. Clear some events.";
const unconfirmedText =
  "Calyx did not confirm the save, and saved nothing. The core can already use the new webhooks. Select Save webhooks again, so that Calyx and the core have the same list.";
const changedText = "Somebody changed the webhooks of this instance while you edited.";
export function WebhooksView({
  instance,
  level,
  onUnsaved,
}: {
  instance: Instance;
  level: 1 | 2;
  // The drawer asks before it closes while a generated secret has no "I saved it".
  onUnsaved?: (unsaved: boolean) => void;
}) {
  const Heading = level === 1 ? "h1" : "h2";
  // The drawer stays open when the card changes to the view, so its first focus does not run.
  // The effect also runs on a direct load or a reload of the path.
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  return (
    <section data-slot="webhooks" className="grid gap-4">
      <Heading ref={heading} tabIndex={-1}>
        Webhooks
      </Heading>
      <Link
        to={`/instances/${instance.id}`}
        className="inline-flex min-h-11 items-center justify-self-start font-semibold text-primary underline underline-offset-3"
      >
        Back
      </Link>
      {instance.send_only ? (
        <p className="m-0">
          This instance only sends, so it has no webhooks. To use webhooks, change Use to Send and
          receive.
        </p>
      ) : instance.state !== "active" ? (
        <>
          <p className="m-0">This instance is not ready, so its webhooks cannot change now.</p>
          <ul className="m-0 grid gap-2 pl-5">
            {instance.webhooks.map((w) => (
              <li key={w.event}>
                <b className="font-semibold">{wordsOf(w.event)}</b>
                <span className="block break-all font-mono text-sm" translate="no">
                  {w.url}
                </span>
                <span className="block text-sm">{signedText(w)}</span>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <WebhooksForm instance={instance} level={level} onUnsaved={onUnsaved} />
      )}
    </section>
  );
}

type Outcome =
  | null
  | { kind: "saved" }
  | { kind: "unconfirmed" }
  | { kind: "changed"; fresh: Webhook[] }
  | { kind: "failed"; failure: RequestFailure; op: "getInstance" | "updateInstance" };

function WebhooksForm({
  instance,
  level,
  onUnsaved,
}: {
  instance: Instance;
  level: 1 | 2;
  onUnsaved?: (unsaved: boolean) => void;
}) {
  const { id, provider } = instance;
  const me = useMe().data;
  const update = useChange("updateInstance");
  // The base list: the webhooks read when the view opened, or after the last confirmed save.
  const [base, setBase] = useState<Webhook[]>(instance.webhooks);
  const [draft, setDraft] = useState(() => draftOf(instance.webhooks, provider));
  // The effects of the last save with no confirmation.
  const [pending, setPending] = useState<Effect[] | null>(null);
  const [outcome, setOutcome] = useState<Outcome>(null);
  const [checked, setChecked] = useState(false);
  const [refused, setRefused] = useState(false);
  const [loading, setLoading] = useState(false);
  // One save at a time, from the read before the request to the end of the answer.
  const running = useRef(false);
  // The form waits during a save: an edit or a new secret would be lost when the answer comes.
  const [busy, setBusy] = useState(false);
  // A disabled fieldset drops the focus to the body. The save gives it back to the form.
  const form = useRef<HTMLFormElement>(null);
  const saveButton = useRef<HTMLButtonElement>(null);
  const hadFocus = useRef(false);
  useEffect(() => {
    if (!busy && hadFocus.current) {
      hadFocus.current = false;
      saveButton.current?.focus();
    }
  }, [busy]);

  // A save that stops on a problem moves the focus to the first one (a field error or a
  // refused secret control). The counter starts the effect after the render of the problem.
  const [stops, setStops] = useState(0);
  useEffect(() => {
    if (stops === 0) return;
    form.current
      ?.querySelector<HTMLElement>(
        '[aria-invalid="true"], fieldset[data-refused] input[type="radio"]:not(:disabled)',
      )
      ?.focus();
  }, [stops]);
  const [rawOpen, setRawOpen] = useState(false);

  const unsaved = unsavedSecret(draft);
  useEffect(() => {
    onUnsaved?.(unsaved);
    return () => onUnsaved?.(false);
  }, [unsaved, onUnsaved]);

  const SubHeading = level === 1 ? "h2" : "h3";
  const errors = checked ? fieldErrors(draft) : { url: {}, secret: {} };
  const live = planWebhooks(base, provider, formOf(draft, provider), secretsOf(draft));
  const tooMany = checked && live.ok && live.body.length > maxWebhooks;
  const blocked = refused ? keepRefused(base, provider, draft) : new Set<string>();
  const raw = rawEvents(provider);
  const rawCount = draft.events.filter((e) => raw.includes(e)).length;
  const allCalyx = calyxEvents.every((e) => draft.events.includes(e));
  // A generated secret with no "I saved it" on a raw row keeps the section open: it disables Save.
  const rawUnsaved =
    draft.mode === "each" &&
    raw.some((e) => {
      const c = choiceOf(draft, e);
      return draft.events.includes(e) && c.kind === "generate" && !c.saved;
    });

  // The save stops. A problem on a raw row opens its section, so the person can see it.
  function stop(d: Draft, refusedRows: string[] = []) {
    const found = fieldErrors(d);
    const rows = d.mode === "each" ? refusedRows : [];
    const onRaw = raw.some(
      (e) => d.events.includes(e) && (found.url[e] || found.secret[e] || rows.includes(e)),
    );
    if (onRaw) setRawOpen(true);
    setStops((n) => n + 1);
  }

  // A change of the person. "Saved." describes the form before the change.
  function edit(change: (d: Draft) => Draft) {
    setDraft(change);
    if (outcome?.kind === "saved") setOutcome(null);
  }

  function toggle(event: string) {
    edit((d) => ({
      ...d,
      events: d.events.includes(event) ? d.events.filter((e) => e !== event) : [...d.events, event],
    }));
  }

  const setMode = (mode: Draft["mode"]) => edit((d) => withMode(d, mode));

  function setChoice(scope: string, c: SecretChoice) {
    edit((d) => (scope === "one" ? { ...d, one: c } : { ...d, each: { ...d.each, [scope]: c } }));
  }

  // A new list from the backend: the form starts again from it.
  function restart(list: Webhook[], next: Outcome) {
    setBase(list);
    setDraft(draftOf(list, provider));
    setPending(null);
    setChecked(false);
    setRefused(false);
    setOutcome(next);
  }

  // No confirmation: the form and a generated secret stay, and the next save checks the effects.
  function unconfirmed(effects: Effect[]) {
    setPending(effects);
    setOutcome({ kind: "unconfirmed" });
  }

  async function afterFailure(failure: RequestFailure, effects: Effect[]) {
    if (failure.kind === "problem" && failure.code !== "change_outcome_unknown") {
      setOutcome({ kind: "failed", failure, op: "updateInstance" });
      return;
    }
    if (failure.kind !== "problem") {
      // No answer: one read shows if the change applied.
      const read = await request("getInstance", { path: { id } });
      const fresh = read.ok ? read.data?.webhooks : undefined;
      if (fresh && matchesPlan(fresh, effects)) {
        restart(fresh, { kind: "saved" });
        return;
      }
    }
    unconfirmed(effects);
  }

  // The save. It never sends the change again by itself: each save is one request.
  async function save(e: FormEvent) {
    e.preventDefault();
    if (running.current || unsaved) return;
    const d = draft;
    setChecked(true);
    setOutcome(null);
    const found = fieldErrors(d);
    if (Object.keys(found.url).length > 0 || Object.keys(found.secret).length > 0) return stop(d);
    const first = planWebhooks(base, provider, formOf(d, provider), secretsOf(d));
    if (!first.ok) {
      setRefused(true);
      return stop(d, first.rows);
    }
    if (first.body.length > maxWebhooks) return;

    running.current = true;
    hadFocus.current = form.current?.contains(document.activeElement) ?? false;
    setBusy(true);
    const done = () => {
      running.current = false;
      setBusy(false);
    };
    const read = await request("getInstance", { path: { id } });
    if (!read.ok || !read.data) {
      // An empty answer is not a read: it shows the general failure of no answer.
      const failure: RequestFailure = read.ok ? { kind: "unreachable" } : read.failure;
      setOutcome({ kind: "failed", failure, op: "getInstance" });
      return done();
    }
    const fresh = read.data.webhooks;
    let current = base;
    if (pending && matchesPlan(fresh, pending)) {
      // The last unconfirmed save applied late. The edits of the person stay.
      current = fresh;
      setBase(fresh);
      setPending(null);
    } else if (!sameList(fresh, base)) {
      setOutcome({ kind: "changed", fresh });
      return done();
    }
    const plan = planWebhooks(current, provider, formOf(d, provider), secretsOf(d));
    if (!plan.ok) {
      setRefused(true);
      stop(d, plan.rows);
      return done();
    }
    if (plan.body.length > maxWebhooks) return done();

    update.mutate(
      { path: { id }, body: { webhooks: plan.body } },
      {
        // The secrets leave the mutation cache after the answer. After a 200,
        // the answer confirms the save, also when a new secret went on a signed webhook.
        onSuccess: (answer) => {
          update.reset();
          // An answer with no list confirms nothing.
          if (answer?.webhooks) restart(answer.webhooks, { kind: "saved" });
          else unconfirmed(plan.effects);
          done();
        },
        onError: (failure) => {
          update.reset();
          void afterFailure(failure, plan.effects).finally(done);
        },
      },
    );
  }

  const row = (event: string) => {
    const on = draft.events.includes(event);
    return (
      <HookRow
        key={event}
        event={event}
        checked={on}
        stored={base.find((h) => h.event === event)}
        onToggle={() => toggle(event)}
      >
        {on && draft.mode === "each" && (
          <>
            <RowUrl
              event={event}
              value={draft.urls[event] ?? ""}
              error={errors.url[event]}
              onChange={(url) => edit((d) => ({ ...d, urls: { ...d.urls, [event]: url } }))}
            />
            <SecretControl
              event={event}
              choice={choiceOf(draft, event)}
              keepBlocked={blocked.has(event)}
              error={errors.secret[event]}
              onChange={(c) => setChoice(event, c)}
            />
          </>
        )}
      </HookRow>
    );
  };

  return (
    <form ref={form} onSubmit={(e) => void save(e)} noValidate>
      <fieldset disabled={busy} aria-busy={busy} className="m-0 grid min-w-0 gap-5 border-0 p-0">
        <p className="m-0 text-muted-foreground">
          Calyx calls your URL when something happens with{" "}
          <span translate="no">{instance.display_name}</span>.
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <Choice
            pressed={draft.mode === "one"}
            title="One URL for all"
            text="Every event that you select goes to the same place."
            onClick={() => setMode("one")}
          />
          <Choice
            pressed={draft.mode === "each"}
            title="One URL for each event"
            text="Each event can go to a different place."
            onClick={() => setMode("each")}
          />
        </div>

        {draft.mode === "one" && (
          <div className="grid gap-1.5">
            <Field
              label="URL"
              value={draft.url}
              onChange={(e) => {
                const url = e.currentTarget.value;
                edit((d) => ({ ...d, url }));
              }}
              hint="It starts with https:// or http://. For example, the address of a webhook node of n8n."
              error={errors.url.one}
              inputMode="url"
              translate="no"
              spellCheck={false}
              autoComplete="off"
            />
            <PlainWarning url={draft.url} />
          </div>
        )}

        <div className="grid gap-1">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <SubHeading className="m-0">Events</SubHeading>
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                edit((d) => ({
                  ...d,
                  events: allCalyx
                    ? d.events.filter((e) => !calyxEvents.includes(e))
                    : [...d.events, ...calyxEvents.filter((e) => !d.events.includes(e))],
                }))
              }
            >
              {allCalyx ? "Clear all" : `Select all ${calyxEvents.length}`}
            </Button>
          </div>
          <div>{calyxEvents.map(row)}</div>
        </div>

        <Collapsible
          open={rawOpen || rawUnsaved}
          onOpenChange={setRawOpen}
          className="rounded-md border border-border px-4"
        >
          <CollapsibleTrigger className="group flex min-h-11 w-full cursor-pointer items-center justify-between gap-2 text-left font-semibold">
            {`Raw WhatsApp events${rawCount > 0 ? ` (${rawCount})` : ""}`}
            <ChevronDown
              aria-hidden="true"
              className="size-5 flex-none transition-transform group-data-[panel-open]:rotate-180"
            />
          </CollapsibleTrigger>
          <CollapsibleContent className="pb-2">
            <p className="m-0 text-sm text-muted-foreground">
              The data as WhatsApp sends it, with no changes. Only for developers.
            </p>
            <div>{raw.map(row)}</div>
          </CollapsibleContent>
        </Collapsible>

        {draft.mode === "one" && (
          <SecretControl
            choice={draft.one}
            keepBlocked={blocked.has("one")}
            error={errors.secret.one}
            onChange={(c) => setChoice("one", c)}
          />
        )}

        <div className="grid gap-1.5">
          <p className="m-0">
            With a secret, your program can check that a call comes from Calyx. Your program must do
            this check. Without a secret, anyone who knows the URL can send false events to your
            program.
          </p>
          <Collapsible>
            <CollapsibleTrigger className="min-h-11 cursor-pointer underline underline-offset-3">
              Technical details
            </CollapsibleTrigger>
            <CollapsibleContent className="grid gap-1.5 pt-1">
              <p className="m-0">
                Calyx signs each call with HMAC-SHA256, in the header{" "}
                <code translate="no">X-Calyx-Signature</code>. The SDKs of Calyx have helpers that
                check the signature.
              </p>
              <p className="m-0">
                A new secret starts at once. Calls in the next seconds can carry the old signature.
              </p>
            </CollapsibleContent>
          </Collapsible>
        </div>

        <p className="m-0 rounded-md bg-idle-soft px-4 py-3.5 text-[0.9375rem]">
          Calyx calls this address from its own server. Use only addresses that you trust.
        </p>

        {tooMany && (
          <p role="alert" className="m-0 font-semibold text-bad">
            {tooManyText}
          </p>
        )}
        {outcome?.kind === "unconfirmed" && <Notice tone="wait" title={unconfirmedText} />}
        {outcome?.kind === "changed" && (
          <Notice tone="wait" title={changedText}>
            <Button
              type="button"
              variant="outline"
              className="mt-2"
              onClick={() => setLoading(true)}
            >
              Load the new list
            </Button>
          </Notice>
        )}
        {outcome?.kind === "failed" && me && (
          <Failure
            failure={outcome.failure}
            context={{
              operationId: outcome.op,
              role: me.user.role === "owner" ? "owner" : "client",
              manager: me.organization.name,
            }}
          />
        )}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <Button ref={saveButton} type="submit" disabled={unsaved}>
            Save webhooks
          </Button>
          <p role="status" className="m-0 font-semibold">
            {outcome?.kind === "saved" ? "Saved." : ""}
          </p>
        </div>
      </fieldset>

      <Dialog open={loading} onOpenChange={(open) => !open && setLoading(false)}>
        <DialogContent showCloseButton={false}>
          <DialogTitle>Load the new list?</DialogTitle>
          <DialogDescription>You will lose your edits.</DialogDescription>
          <DialogFooter>
            <Button
              onClick={() => {
                if (outcome?.kind === "changed") restart(outcome.fresh, null);
                setLoading(false);
              }}
            >
              Load the new list
            </Button>
            <Button variant="outline" onClick={() => setLoading(false)}>
              Cancel
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </form>
  );
}
