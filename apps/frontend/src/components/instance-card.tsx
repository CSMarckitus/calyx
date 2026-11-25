import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { cn } from "@/lib/utils";
import type { components } from "../api/generated/types.ts";
import { useChange } from "../api/queries.ts";
import { request } from "../api/request.ts";
import { explain, type Role } from "../domain/errors.ts";
import type { Color, Described } from "../domain/labels.ts";
import { formatTime } from "../domain/time.ts";
import { AccessBox, useAccess } from "./access-box.tsx";
import { CopyButton } from "./copy-button.tsx";
import { Failure } from "./failure.tsx";
import type { Viewer } from "./health-card.tsx";
import { Info } from "./info.tsx";
import { Rename } from "./rename.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./ui/dialog.tsx";
import { UseChoice } from "./use-choice.tsx";
import { Words } from "./words.tsx";

export type Instance = components["schemas"]["Instance"];

// What a cell and a card need to know about the person and the page.
export type Viewing = {
  viewer: Viewer;
  manager: string;
  userId: string;
  // The state of Calyx is `core`, `database` or `backend`: the changes that need the core wait.
  down: boolean;
  // WhatsApp Web is blocked for the organization, so "Reconnect" does not show.
  blocked: boolean;
  clientName: (id: string) => string | undefined;
};

export const roleOf = (v: Viewing): Role => (v.viewer === "owner" ? "owner" : "client");
export const noun = (v: Viewing) => (v.viewer === "owner" ? "instance" : "WhatsApp number");

const dots: Record<Color, string> = {
  ok: "bg-ok",
  wait: "bg-wait",
  bad: "bg-bad",
  idle: "bg-idle",
};
const words: Record<Color, string> = {
  ok: "text-ok",
  wait: "text-wait",
  bad: "text-bad",
  idle: "text-idle",
};

export function Dot({ color }: { color: Color }) {
  return <i aria-hidden="true" className={cn("size-2.5 shrink-0 rounded-full", dots[color])} />;
}

// The word of the state, with its color.
export function StateWord({
  color,
  children,
  className,
}: {
  color: Color;
  children: string;
  className?: string;
}) {
  return (
    <span className={cn("flex items-center gap-2 font-semibold", words[color], className)}>
      <Dot color={color} />
      <span>{children}</span>
    </span>
  );
}

const lowerDay = (text: string) => text.replace(/^(Today|Yesterday)/, (d) => d.toLowerCase());

// The removal of an instance. The question names what is lost. After the answer the page reads
// again: a 204 ends the removal, a 202 shows "Removing".
export function useRemove(instance: Instance, viewing: Viewing, onRemoved?: () => void) {
  const change = useChange("removeInstance");
  const [asking, setAsking] = useState(false);
  const role = roleOf(viewing);

  function send() {
    change.mutate(
      { path: { id: instance.id } },
      {
        // A 204 and a 202 have no body. A read of the instance tells which one it was.
        onSuccess: async () => {
          const read = await request("getInstance", { path: { id: instance.id } });
          if (!read.ok && read.failure.kind === "problem" && read.failure.status === 404)
            onRemoved?.();
        },
      },
    );
  }

  const question = (
    <Dialog open={asking} onOpenChange={setAsking}>
      <DialogContent showCloseButton={false}>
        <DialogTitle>
          <span>Remove</span> <span translate="no">{instance.display_name}</span>
          <span>?</span>
        </DialogTitle>
        <DialogDescription>
          <Words
            text={`Calyx unlinks the phone and deletes its settings, its webhooks and its bulk jobs. You cannot undo this.${role === "client" ? " To connect it again, you need permission to connect numbers." : ""}`}
          />
        </DialogDescription>
        <DialogFooter>
          <Button
            variant="destructive"
            disabled={change.isPending}
            onClick={() => {
              setAsking(false);
              send();
            }}
          >
            Remove
          </Button>
          <Button variant="outline" onClick={() => setAsking(false)}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );

  const failure = change.isError ? (
    <Failure
      failure={change.error}
      context={{ operationId: "removeInstance", role, manager: viewing.manager }}
      checking={change.checking}
    />
  ) : null;

  return { ask: () => setAsking(true), pending: change.isPending, question, failure };
}

// The one action of a state, in its box, for the card and the cell. The cell is compact,
// and it has no "Continue", because the whole cell opens the instance.
export function InstanceAction({
  d,
  instance,
  viewing,
  access,
  remove,
  className,
  compact = false,
}: {
  d: Described;
  instance: Instance;
  viewing: Viewing;
  access: ReturnType<typeof useAccess>;
  remove: ReturnType<typeof useRemove>;
  className: string;
  compact?: boolean;
}) {
  const navigate = useNavigate();
  const size = compact ? ({ size: "sm", className: "min-h-11 px-3.5 sm:min-h-9.5" } as const) : {};

  let action = null;
  if (d.action === "reconnect" && !viewing.blocked) {
    action = (
      <Button
        {...size}
        disabled={viewing.down}
        onClick={() => void navigate(`/instances/${instance.id}`, { state: { reconnect: true } })}
      >
        Reconnect
      </Button>
    );
  } else if (d.action === "remove") {
    action = (
      <Button {...size} variant="destructive" disabled={remove.pending} onClick={remove.ask}>
        Remove
      </Button>
    );
  } else if (d.action === "enable") {
    action = (
      <Button
        {...size}
        variant={compact ? "outline" : "default"}
        disabled={viewing.down || access.pending}
        onClick={() => access.set(true)}
      >
        <Words text="Turn on Calyx access" />
      </Button>
    );
  } else if (d.action === "continue" && !compact) {
    action = (
      <Button render={<Link to={`/instances/${instance.id}`} />} nativeButton={false}>
        Continue
      </Button>
    );
  }
  return action && <div className={className}>{action}</div>;
}

const devices: Record<string, string> = {
  CHROME_WINDOWS: "Chrome (Windows)",
  EDGE_WINDOWS: "Edge (Windows)",
  FIREFOX_WINDOWS: "Firefox (Windows)",
  FIREFOX_LINUX: "Firefox (Linux)",
  SAFARI_MAC_OS: "Safari (macOS)",
};

const legend: [string, Color, string][] = [
  ["Connected", "ok", "Calyx can send and receive."],
  ["Reconnecting", "wait", "A short cut. Calyx connects again by itself."],
  ["Disconnected", "bad", "The phone unlinked Calyx. Link it again."],
  ["Problem", "bad", "WhatsApp refused the session."],
  ["Not in use", "idle", "Calyx access is off."],
  ["Unknown", "idle", "Calyx cannot check it now."],
];

// One instance with all its facts: in the drawer of the list, or as the screen of a client user
// with one number.
export function InstanceCard({
  instance,
  described: d,
  viewing,
  now,
  level,
  onRemoved,
}: {
  instance: Instance;
  described: Described;
  viewing: Viewing;
  now: number;
  level: 1 | 2;
  onRemoved?: () => void;
}) {
  const role = roleOf(viewing);
  const active = instance.state === "active";
  const meta = instance.provider === "META_CLOUD";
  const remove = useRemove(instance, viewing, onRemoved);
  const access = useAccess(instance, role, viewing.manager);
  const since = d.since !== undefined ? formatTime(d.since, now) : undefined;
  const lastAt = d.last?.at !== undefined ? formatTime(d.last.at, now) : undefined;
  const blockedText =
    d.action === "reconnect" && viewing.blocked
      ? explain(
          {
            kind: "problem",
            status: 403,
            code: "whatsapp_web_not_allowed",
            detail: "",
            fields: [],
            requestId: "",
          },
          { operationId: "pairInstance", role, manager: viewing.manager },
        )
      : null;
  const device = instance.device_identity ? devices[instance.device_identity] : undefined;

  return (
    <article
      data-slot="instance"
      className={cn(
        "grid gap-5",
        level === 1 && "rounded-lg border border-border bg-card px-4.5 py-5 sm:px-7 sm:py-6",
      )}
    >
      <header className="flex flex-wrap items-start justify-between gap-x-5 gap-y-3">
        <div className="min-w-0 flex-1 pt-1">
          <Rename
            instance={instance}
            level={level}
            canRename={active}
            role={role}
            manager={viewing.manager}
          />
        </div>
        {active && (
          <AccessBox
            instance={instance}
            access={access}
            down={viewing.down}
            withFailure={d.action !== "enable"}
          />
        )}
      </header>

      <div className="grid gap-2">
        <span className="flex items-center gap-1 text-[0.9375rem] font-semibold text-muted-foreground">
          WhatsApp connection
          <Info label="What are the states of the connection?">
            <ul className="m-0 grid list-none gap-2 p-0">
              {legend.map(([label, color, text]) => (
                <li
                  key={label}
                  className={cn(
                    "grid grid-cols-[12px_1fr] gap-2.5",
                    label === d.label && "font-semibold",
                  )}
                >
                  <span className="pt-1.5">
                    <Dot color={color} />
                  </span>
                  <span>
                    <b>{`${label}:`}</b> <Words text={text} />
                  </span>
                </li>
              ))}
            </ul>
          </Info>
        </span>
        <div role="status">
          <StateWord color={d.color} className="text-[1.375rem]">
            {d.label}
          </StateWord>
        </div>
        <p className="m-0">
          <Words text={d.cause} />
        </p>
        {d.last && (
          <p className="m-0 text-muted-foreground">
            <span>{`Last known state: ${d.last.label}`}</span>
            {lastAt && <span title={lastAt.title}>{`, at ${lowerDay(lastAt.text)}`}</span>}
            <span>.</span>
          </p>
        )}
        {since && !d.last && (
          <p className="m-0 text-muted-foreground" title={since.title}>
            {`Since ${lowerDay(since.text)}`}
          </p>
        )}
        {blockedText && (
          <p className="m-0">
            <Words text={blockedText.cause} keep={[viewing.manager]} />{" "}
            <Words text={blockedText.nextStep} keep={[viewing.manager]} />
          </p>
        )}
        <InstanceAction
          d={d}
          instance={instance}
          viewing={viewing}
          access={access}
          remove={remove}
          className="mt-2 flex flex-wrap gap-2"
        />
        {d.action === "enable" && access.failure}
      </div>

      <dl className="m-0 grid gap-x-6 gap-y-1 border-t border-border pt-4 sm:grid-cols-[auto_1fr]">
        {viewing.viewer === "owner" && (
          <>
            <dt className="text-muted-foreground">Client</dt>
            <dd className="m-0 mb-2.5 sm:mb-0" translate="no">
              {viewing.clientName(instance.client_id) ?? ""}
            </dd>
          </>
        )}
        {meta ? (
          <>
            <dt className="text-muted-foreground">Provider</dt>
            <dd className="m-0 mb-2.5 sm:mb-0">
              Meta Cloud API. The API manages this number and its credentials.
            </dd>
            {instance.phone_number_id && (
              <>
                <dt className="text-muted-foreground">Phone number ID</dt>
                <dd className="m-0 mb-2.5 font-mono sm:mb-0" translate="no">
                  {instance.phone_number_id}
                </dd>
              </>
            )}
          </>
        ) : (
          <>
            {device && (
              <>
                <dt className="text-muted-foreground">On your phone</dt>
                <dd className="m-0 mb-2.5 sm:mb-0">{`Linked devices, as ${device}`}</dd>
              </>
            )}
            <dt className="self-center text-muted-foreground">Use</dt>
            <dd className="m-0">
              {active ? (
                <UseChoice
                  instance={instance}
                  down={viewing.down}
                  role={role}
                  manager={viewing.manager}
                />
              ) : instance.send_only ? (
                "Only send"
              ) : (
                "Send and receive"
              )}
            </dd>
          </>
        )}
        <dt className="text-muted-foreground">Webhooks</dt>
        <dd className="m-0 mb-2.5 sm:mb-0">
          {instance.send_only ? (
            "None: this number only sends"
          ) : (
            <span className="flex flex-wrap items-center gap-x-3">
              <span>{`${instance.webhooks.length} ${instance.webhooks.length === 1 ? "event" : "events"}`}</span>
              {active && (
                <Link
                  to={`/instances/${instance.id}/webhooks`}
                  className="font-semibold text-primary underline underline-offset-3"
                >
                  Edit
                </Link>
              )}
            </span>
          )}
        </dd>
        {/* No count: the card reads no bulk jobs. A job can run in every state. */}
        <dt className="text-muted-foreground">Bulk jobs</dt>
        <dd className="m-0 mb-2.5 sm:mb-0">
          <Link
            to={`/instances/${instance.id}/bulk-jobs`}
            className="font-semibold text-primary underline underline-offset-3"
          >
            View
          </Link>
        </dd>
        <dt className="self-center text-muted-foreground">Instance ID</dt>
        <dd className="m-0 flex flex-wrap items-center gap-2">
          <span className="font-mono" translate="no">
            {instance.id}
          </span>
          <CopyButton text={instance.id} liveRegion />
        </dd>
      </dl>

      {remove.failure}
      {active && d.action !== "remove" && (
        <footer className="flex justify-end border-t border-border pt-2">
          <Button
            variant="link"
            className="px-0 font-medium text-bad"
            disabled={remove.pending}
            onClick={remove.ask}
          >
            {`Remove this ${noun(viewing)}`}
          </Button>
        </footer>
      )}
      {remove.question}
      {access.question}
    </article>
  );
}
