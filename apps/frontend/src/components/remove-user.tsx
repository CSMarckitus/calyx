import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import type { components } from "../api/generated/types.ts";
import { instancesOptions } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { Failure } from "./failure.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "./ui/dialog.tsx";
import { Words } from "./words.tsx";

type Client = components["schemas"]["Client"];

// The four sentences of the dialog. X is the email of the user.
const sentences = (x: string) => [
  `${x} can no longer log in, and Calyx revokes the API keys of ${x}. Programs with these keys get an error.`,
  `Calyx does not change the instances: it keeps calling the webhook URLs below, also the ones that a program set with a key of ${x}.`,
  "Bulk jobs keep sending. Open each instance below to see its bulk jobs.",
  "You can invite this email again. The old API keys stay revoked.",
];

const viewLink = "font-semibold text-primary underline underline-offset-3";

// The body of the dialog mounts when the dialog opens, so each open reads the instances.
// The operation has no filter by client. No bulk read, so the dialog makes no core call.
function Removal({
  client,
  email,
  busy,
  onRemove,
  onCancel,
}: {
  client: Client;
  email: string;
  busy: boolean;
  onRemove: () => void;
  onCancel: () => void;
}) {
  const read = useQuery({ ...instancesOptions(), refetchOnMount: "always" });
  // Only a read of this open counts: an older list can miss a webhook.
  const ready = read.isFetchedAfterMount && read.status === "success";
  const instances =
    ready && read.data
      ? read.data.items.filter((i) => i.client_id === client.id && i.state !== "removing")
      : [];

  return (
    <>
      <DialogTitle>Remove the user?</DialogTitle>
      <DialogDescription render={<div />} className="grid gap-2">
        {sentences(email).map((s) => (
          <p key={s} className="m-0">
            <Words text={s} keep={[email]} />
          </p>
        ))}
      </DialogDescription>
      {read.isError ? (
        <Failure
          failure={read.error as unknown as RequestFailure}
          context={{ operationId: "listInstances", role: "owner" }}
        />
      ) : !ready ? (
        <p role="status" className="m-0">
          Reading the instances.
        </p>
      ) : instances.length === 0 ? (
        <p className="m-0">This client has no instances.</p>
      ) : (
        <ul className="m-0 grid list-none gap-3 p-0">
          {instances.map((i) => (
            <li key={i.id} data-slot="removal-instance" className="grid gap-1">
              <b translate="no" className="font-semibold break-words [overflow-wrap:anywhere]">
                {i.display_name}
              </b>
              {i.webhooks.length === 0 ? (
                <span className="text-muted-foreground">No webhooks</span>
              ) : (
                <ul className="m-0 grid list-none gap-1 p-0 break-all">
                  {[...new Set(i.webhooks.map((w) => w.url))].map((url) => (
                    <li key={url} translate="no">
                      {url}
                    </li>
                  ))}
                </ul>
              )}
              <span className="flex flex-wrap gap-x-4">
                <Link to={`/instances/${i.id}/webhooks`} className={viewLink}>
                  Webhooks
                </Link>
                <Link to={`/instances/${i.id}/bulk-jobs`} className={viewLink}>
                  Bulk jobs
                </Link>
              </span>
            </li>
          ))}
        </ul>
      )}
      <DialogFooter>
        <Button variant="destructive" disabled={!ready || busy} onClick={onRemove}>
          Remove the user
        </Button>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </DialogFooter>
    </>
  );
}

// "Remove the user" of a client. The caller sends the change, so its answer stays on the
// page after the client shows no user.
export function RemoveUser({
  client,
  email,
  busy,
  onRemove,
}: {
  client: Client;
  email: string;
  busy: boolean;
  onRemove: () => void;
}) {
  const [asking, setAsking] = useState(false);
  return (
    <>
      <Button
        variant="destructive"
        className="justify-self-start"
        disabled={busy}
        onClick={() => setAsking(true)}
      >
        Remove the user
      </Button>
      <Dialog open={asking} onOpenChange={setAsking}>
        <DialogContent
          showCloseButton={false}
          className="max-h-[calc(100dvh-2rem)] overflow-y-auto"
        >
          <Removal
            client={client}
            email={email}
            busy={busy}
            onRemove={() => {
              setAsking(false);
              onRemove();
            }}
            onCancel={() => setAsking(false)}
          />
        </DialogContent>
      </Dialog>
    </>
  );
}
