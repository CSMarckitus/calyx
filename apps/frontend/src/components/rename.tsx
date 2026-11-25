import { type FormEvent, useState } from "react";
import type { components } from "../api/generated/types.ts";
import { useChange } from "../api/queries.ts";
import type { Role } from "../domain/errors.ts";
import { Failure } from "./failure.tsx";
import { Button } from "./ui/button.tsx";
import { Input } from "./ui/input.tsx";

type Instance = components["schemas"]["Instance"];

// The name of the instance as the title of its screen, with "Rename" when the state allows it.
export function Rename({
  instance,
  level,
  canRename,
  role,
  manager,
}: {
  instance: Instance;
  level: 1 | 2;
  canRename: boolean;
  role: Role;
  manager: string;
}) {
  const change = useChange("updateInstance");
  const [editing, setEditing] = useState(false);
  const Heading = level === 1 ? "h1" : "h2";

  function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const name = String(new FormData(e.currentTarget).get("name")).trim();
    if (name === "" || name === instance.display_name) {
      setEditing(false);
      return;
    }
    change.mutate(
      { path: { id: instance.id }, body: { display_name: name } },
      { onSuccess: () => setEditing(false) },
    );
  }

  return (
    <div className="grid gap-2">
      {editing ? (
        <form
          onSubmit={save}
          onKeyDown={(e) => {
            if (e.key === "Escape") setEditing(false);
          }}
          className="flex flex-wrap items-center gap-2"
        >
          <Input
            name="name"
            aria-label="Name"
            maxLength={100}
            required
            defaultValue={instance.display_name}
            translate="no"
            autoFocus
            onFocus={(e) => e.currentTarget.select()}
            className="min-h-11 min-w-0 flex-1 font-display text-xl font-bold"
          />
          <Button type="submit" disabled={change.isPending}>
            Save
          </Button>
          <Button type="button" variant="outline" onClick={() => setEditing(false)}>
            Cancel
          </Button>
        </form>
      ) : (
        <div className="flex flex-wrap items-baseline gap-x-3.5 gap-y-0.5">
          {/* A drawer moves the focus to this title when it opens (Accessibility). */}
          <Heading translate="no" tabIndex={-1} className="break-words [overflow-wrap:anywhere]">
            {instance.display_name}
          </Heading>
          {canRename && (
            <Button variant="link" className="px-0 font-medium" onClick={() => setEditing(true)}>
              Rename
            </Button>
          )}
        </div>
      )}
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
