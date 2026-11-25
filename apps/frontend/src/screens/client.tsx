import { ArrowLeft } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { Failure } from "@/components/failure";
import { Invitation } from "@/components/invitation";
import { Limits } from "@/components/limits";
import { Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { Words } from "@/components/words";
import type { components } from "../api/generated/types.ts";
import { useChange, useClient, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { NotFound } from "./not-found.tsx";

type Client = components["schemas"]["Client"];

const gone = "Calyx deletes the client, its user and its invitation. You cannot undo this.";

// The deletion of a client, only with no instance. The backend deletes its user and its
// invitation with it.
function Deletion({ client }: { client: Client }) {
  const remove = useChange("deleteClient");
  const navigate = useNavigate();
  const [asking, setAsking] = useState(false);

  if (client.instance_count > 0)
    return (
      <p className="m-0 text-muted-foreground">To delete this client, remove its numbers first.</p>
    );
  return (
    <>
      <p className="m-0 text-muted-foreground">
        <Words text={gone} />
      </p>
      {remove.isError && (
        <Failure
          failure={remove.error}
          context={{ operationId: "deleteClient", role: "owner" }}
          checking={remove.checking}
        />
      )}
      <Button
        variant="destructive"
        className="justify-self-start"
        disabled={remove.isPending}
        onClick={() => setAsking(true)}
      >
        Delete this client
      </Button>
      <Dialog open={asking} onOpenChange={setAsking}>
        <DialogContent showCloseButton={false}>
          <DialogTitle>
            <span>Delete</span> <span translate="no">{client.name}</span>
            <span>?</span>
          </DialogTitle>
          <DialogDescription>
            <Words text={gone} />
          </DialogDescription>
          <DialogFooter>
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => {
                setAsking(false);
                remove.mutate(
                  { path: { id: client.id } },
                  { onSuccess: () => void navigate("/", { replace: true }) },
                );
              }}
            >
              Delete
            </Button>
            <Button variant="outline" onClick={() => setAsking(false)}>
              Cancel
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// `/clients/:id`: the access, the limits and the deletion of a client. The default client has
// only its limit.
export function ClientScreen() {
  const { id = "" } = useParams();
  const me = useMe().data;
  const owner = me?.user.role === "owner";
  const read = useClient(id, owner);
  if (!me) return null;
  if (!owner) return <NotFound />;
  // A read throws its failure (queries.ts).
  const failed = read.error as unknown as RequestFailure | null;
  if (failed) {
    if (failed.kind === "problem" && failed.status === 404) return <NotFound />;
    return <Failure failure={failed} context={{ operationId: "getClient", role: "owner" }} />;
  }
  const client = read.data;
  if (!client) return null;
  const org = me.organization;

  return (
    <>
      <div className="grid gap-1.5">
        <Link
          to="/"
          className="inline-flex min-h-11 items-center gap-1.5 justify-self-start font-semibold text-primary"
        >
          <ArrowLeft aria-hidden="true" className="size-4" />
          All instances
        </Link>
        <h1
          translate={client.is_default ? undefined : "no"}
          className="break-words [overflow-wrap:anywhere]"
        >
          {client.is_default ? "My numbers" : client.name}
        </h1>
      </div>
      {!client.is_default && (
        <Section title="Access">
          <Invitation
            client={client}
            mail={org.smtp_configured === true}
            acknowledged={org.whatsapp_web_acknowledged_at != null}
          />
        </Section>
      )}
      <Section title="Limits">
        <Limits client={client} orgMax={org.max_instances ?? 0} />
      </Section>
      {!client.is_default && (
        <Section title="Delete this client">
          <Deletion client={client} />
        </Section>
      )}
    </>
  );
}
