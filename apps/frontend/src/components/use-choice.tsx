import { useState } from "react";
import type { components } from "../api/generated/types.ts";
import { useChange } from "../api/queries.ts";
import type { Role } from "../domain/errors.ts";
import { Failure } from "./failure.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./ui/dialog.tsx";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select.tsx";

type Instance = components["schemas"]["Instance"];
type Use = "both" | "send";

const items: Record<Use, string> = { both: "Send and receive", send: "Only send" };

// "Use" of a WhatsApp Web instance: a choice, because the person can change it later.
// One selection sends one change. "Only send" on an instance with webhooks
// opens a dialog. The dialog reads the live instance, so its list follows the stream.
export function UseChoice({
  instance,
  down,
  role,
  manager,
}: {
  instance: Instance;
  down: boolean;
  role: Role;
  manager: string;
}) {
  const change = useChange("updateInstance");
  const [asking, setAsking] = useState(false);
  const value: Use = instance.send_only ? "send" : "both";

  function choose(next: Use) {
    if (next === value) return;
    if (next === "send" && instance.webhooks.length > 0) {
      setAsking(true);
      return;
    }
    change.mutate({ path: { id: instance.id }, body: { send_only: next === "send" } });
  }

  function removeAndSend() {
    setAsking(false);
    change.mutate({ path: { id: instance.id }, body: { send_only: true, webhooks: [] } });
  }

  return (
    <div className="grid gap-2">
      <Select
        value={value}
        items={items}
        onValueChange={(next) => choose(next as Use)}
        disabled={down || change.isPending}
      >
        <SelectTrigger aria-label="Use" className="min-w-48">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {(Object.keys(items) as Use[]).map((k) => (
            <SelectItem key={k} value={k}>
              {items[k]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Dialog open={asking} onOpenChange={setAsking}>
        <DialogContent showCloseButton={false}>
          <DialogTitle>Only send?</DialogTitle>
          <DialogDescription>Calyx stops calling these addresses.</DialogDescription>
          <ul className="grid gap-1 break-all">
            {instance.webhooks.map((w) => (
              <li key={`${w.event} ${w.url}`} translate="no">
                {w.url}
              </li>
            ))}
          </ul>
          <DialogFooter>
            <Button disabled={change.isPending} onClick={removeAndSend}>
              Remove the webhooks and only send
            </Button>
            <Button variant="outline" onClick={() => setAsking(false)}>
              Cancel
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {change.isError && (
        <Failure
          failure={change.error}
          context={{ operationId: "updateInstance", role, manager }}
          checking={change.checking}
        />
      )}
    </div>
  );
}
