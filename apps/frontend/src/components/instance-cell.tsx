import { memo } from "react";
import { Link } from "react-router";
import { cn } from "@/lib/utils";
import { endDraftNamed } from "../domain/draft.ts";
import type { Described } from "../domain/labels.ts";
import { AccessSwitch, useAccess } from "./access-box.tsx";
import {
  Dot,
  type Instance,
  InstanceAction,
  roleOf,
  StateWord,
  useRemove,
  type Viewing,
} from "./instance-card.tsx";
import { Words } from "./words.tsx";

// A text with the part that matches the search marked. `at` is -1 when nothing matches.
export function Marked({ text, at, length }: { text: string; at: number; length: number }) {
  if (at < 0 || length === 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, at)}
      <mark className="bg-wait-soft px-px text-inherit">{text.slice(at, at + length)}</mark>
      {text.slice(at + length)}
    </>
  );
}

// The short reason under the word of a state that needs attention. The card has the long one.
function why(d: Described): string | null {
  if (d.label === "Disconnected")
    return d.filter === "off"
      ? "Access off, and the phone is unlinked."
      : "The phone must be linked again.";
  if (d.label === "Problem") return "WhatsApp refused the session.";
  if (d.label === "Reconnecting") return "Calyx is trying again. Nothing to do.";
  if (d.label === "Not found") return "Calyx cannot find this number.";
  if (d.label === "New credentials needed") return "Change the credentials with the API.";
  return null;
}

const tones = {
  person: "bg-bad-soft hover:shadow-[inset_0_0_0_2px_var(--bad)]",
  wait: "bg-wait-soft hover:shadow-[inset_0_0_0_2px_var(--wait)]",
  plain: "bg-card hover:bg-accent focus-within:bg-accent",
};
const names = { person: "text-bad", wait: "text-wait", plain: "text-primary" };

// The action, the switch and their questions. They do not depend on the search, so a letter
// does not draw them again: only the name with its mark changes.
const Controls = memo(function Controls({
  instance,
  described: d,
  viewing,
}: {
  instance: Instance;
  described: Described;
  viewing: Viewing;
}) {
  const role = roleOf(viewing);
  const access = useAccess(instance, role, viewing.manager);
  const remove = useRemove(instance, viewing);

  return (
    <>
      <InstanceAction
        d={d}
        instance={instance}
        viewing={viewing}
        access={access}
        remove={remove}
        className="relative z-1 col-start-1 mt-1 mb-2 justify-self-start"
        compact
      />
      {(access.failure || remove.failure) && (
        <div className="relative z-1 col-start-1 mb-2 grid gap-2">
          {access.failure}
          {remove.failure}
        </div>
      )}
      {instance.state === "active" && (
        <div className="col-start-2 row-start-1 pt-1">
          <AccessSwitch instance={instance} access={access} down={viewing.down} />
        </div>
      )}
      {access.question}
      {remove.question}
    </>
  );
});

// One instance in the list. The whole cell opens the instance. The action and the switch stand
// above that link.
export const InstanceCell = memo(function InstanceCell({
  instance,
  described: d,
  viewing,
  at,
  length,
}: {
  instance: Instance;
  described: Described;
  viewing: Viewing;
  at: number;
  length: number;
}) {
  const creating = instance.state === "creating";
  // The words come from describe(), so "Unknown" wins over the state of the data.
  const linking = d.action === "continue";
  const tone =
    d.filter === "person" ? "person" : d.filter === "wait" && !linking ? "wait" : "plain";
  const reason = why(d);

  return (
    <div
      data-slot="instance"
      className={cn(
        "relative grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 border-r border-b border-border px-4.5 py-2 max-sm:border-r-0 sm:px-5",
        tones[tone],
      )}
    >
      <Link
        to={`/instances/${instance.id}`}
        onClick={() => {
          // "Continue" of a number with the name of a draft of its client ends that draft.
          if (creating) endDraftNamed(instance.client_id, instance.display_name, viewing.userId);
        }}
        className="group col-start-1 row-start-1 grid min-w-0 gap-0.5 py-2.5 text-left text-foreground before:absolute before:inset-0 before:content-['']"
      >
        <span
          translate="no"
          className={cn(
            "font-semibold [overflow-wrap:anywhere] group-hover:underline group-hover:underline-offset-3",
            names[tone],
          )}
        >
          <Marked text={instance.display_name} at={at} length={length} />
        </span>
        {linking ? (
          <span className="flex items-center gap-2 text-[0.9375rem] font-medium text-wait">
            <Dot color="wait" />
            <span>Linking. Continue</span>
          </span>
        ) : (
          <StateWord color={d.color} className="text-[0.9375rem] font-medium">
            {d.label}
          </StateWord>
        )}
        {reason && (
          <span className="text-[0.9375rem]">
            <Words text={reason} />
          </span>
        )}
      </Link>
      <Controls instance={instance} described={d} viewing={viewing} />
    </div>
  );
});
