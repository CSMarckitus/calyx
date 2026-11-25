import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router";
import { useChange } from "../api/queries.ts";
import { Field, fieldFailed } from "./access-page.tsx";
import { Failure } from "./failure.tsx";
import { CanConnect } from "./limits.tsx";
import { Button } from "./ui/button.tsx";
import { Dialog, DialogContent, DialogFooter, DialogTitle } from "./ui/dialog.tsx";

// "Add a client" at the end of the list. "Yes, up to 1 for now" comes first: one number is the usual
// start, and the owner can change it on the page of the client.
export function AddClient() {
  const add = useChange("createClient");
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const yes = data.get("can") === "yes";
    add.mutate(
      {
        body: {
          name: String(data.get("name")).trim(),
          can_create_instances: yes,
          ...(yes ? { max_instances: 1 } : {}),
        },
      },
      {
        onSuccess: (client) => {
          setOpen(false);
          if (client) void navigate(`/clients/${client.id}`);
        },
      },
    );
  }

  return (
    <div>
      <Button
        variant="outline"
        onClick={() => {
          add.reset();
          setOpen(true);
        }}
      >
        Add a client
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent showCloseButton={false}>
          <DialogTitle>Add a client</DialogTitle>
          <form onSubmit={submit} noValidate className="grid gap-4">
            <Field
              label="Name"
              name="name"
              placeholder="Company name"
              maxLength={100}
              translate="no"
              autoComplete="off"
              error={fieldFailed(add.error, "name") ? "Use 1 to 100 characters." : null}
              required
            />
            <CanConnect
              options={[
                ["yes", "Yes, up to 1 for now. You can change it later in Limits."],
                ["no", "No"],
              ]}
              defaultValue="yes"
            />
            {add.isError && (
              <Failure
                failure={add.error}
                context={{ operationId: "createClient", role: "owner" }}
                checking={add.checking}
              />
            )}
            <DialogFooter>
              <Button type="submit" disabled={add.isPending}>
                Add client
              </Button>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
