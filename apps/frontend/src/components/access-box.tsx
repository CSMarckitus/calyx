import { useState } from "react";
import { cn } from "@/lib/utils";
import type { components } from "../api/generated/types.ts";
import { useChange } from "../api/queries.ts";
import type { Role } from "../domain/errors.ts";
import { Failure } from "./failure.tsx";
import { Info } from "./info.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./ui/dialog.tsx";
import { Switch } from "./ui/switch.tsx";
import { Words } from "./words.tsx";

type Instance = components["schemas"]["Instance"];

// Calyx access of one instance (`enabled`). Off asks first, because programs stop using the
// number. On needs no question.
export function useAccess(instance: Instance, role: Role, manager: string) {
  const change = useChange("updateInstance");
  const [asking, setAsking] = useState(false);

  function send(enabled: boolean) {
    change.mutate({ path: { id: instance.id }, body: { enabled } });
  }

  function set(enabled: boolean) {
    if (enabled) send(true);
    else setAsking(true);
  }

  const question = (
    <Dialog open={asking} onOpenChange={setAsking}>
      <DialogContent showCloseButton={false}>
        <DialogTitle>
          <Words text="Turn off Calyx access?" />
        </DialogTitle>
        <DialogDescription>
          <Words
            text={`Calyx and your programs stop using ${instance.display_name}. The phone works with WhatsApp as always, and the link is kept.`}
            keep={[instance.display_name]}
          />
        </DialogDescription>
        <DialogFooter>
          <Button
            disabled={change.isPending}
            onClick={() => {
              setAsking(false);
              send(false);
            }}
          >
            Turn off
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
      context={{ operationId: "updateInstance", role, manager }}
      checking={change.checking}
    />
  ) : null;

  return { set, pending: change.isPending, question, failure };
}

// The switch. The position is the data: it moves when the page reads the new value. While the
// core is down it keeps its position and is disabled.
export function AccessSwitch({
  instance,
  access,
  down,
  withLabel = false,
}: {
  instance: Instance;
  access: ReturnType<typeof useAccess>;
  down: boolean;
  withLabel?: boolean;
}) {
  return (
    <div
      className={cn(
        "relative z-1 inline-flex min-h-11 shrink-0 items-center gap-2.5 font-semibold",
        withLabel && "w-full justify-between",
      )}
    >
      {withLabel && <span>{instance.enabled ? "On" : "Off"}</span>}
      <Switch
        checked={instance.enabled}
        disabled={down || access.pending}
        onCheckedChange={(next) => access.set(next)}
        // The second click of a double click does nothing, so it sends one change.
        onClick={(e) => {
          if (e.detail > 1) e.preventBaseUIHandler();
        }}
        aria-label={`Calyx access of ${instance.display_name}`}
        title={`Calyx access: ${instance.enabled ? "On" : "Off"}`}
      />
    </div>
  );
}

// The box of the instance screen: its title, what it means, and the switch. It takes the access
// of its card, so the switch and "Turn on Calyx access" are one change at a time. The card
// shows the question, and shows the failure under its action when that action is "enable".
export function AccessBox({
  instance,
  access,
  down,
  withFailure,
}: {
  instance: Instance;
  access: ReturnType<typeof useAccess>;
  down: boolean;
  withFailure: boolean;
}) {
  return (
    <div className="grid gap-2">
      <div className="grid min-w-44 rounded-lg border border-border bg-background px-3.5 pt-2 pb-1.5">
        <span className="flex items-center gap-1 text-[0.9375rem] font-semibold text-muted-foreground">
          <Words text="Calyx access" />
          <Info label="What is Calyx access?">
            <p>
              <b>On:</b> <Words text="Calyx and your programs can use this number." />
            </p>
            <p className="mt-2">
              <b>Off:</b> they cannot. The phone works with WhatsApp as always, and the link is
              kept.
            </p>
          </Info>
        </span>
        <AccessSwitch instance={instance} access={access} down={down} withLabel />
      </div>
      {withFailure && access.failure}
    </div>
  );
}
