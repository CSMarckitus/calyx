import { type FormEvent, useId, useState } from "react";
import type { components } from "../api/generated/types.ts";
import { useChange } from "../api/queries.ts";
import { Field, fieldFailed } from "./access-page.tsx";
import { selectClass } from "./connect/options-step.tsx";
import { Failure } from "./failure.tsx";
import { Notice } from "./notice.tsx";
import { Button } from "./ui/button.tsx";
import { Label } from "./ui/label.tsx";

type Client = components["schemas"]["Client"];

// The choice of `can_create_instances`, in "Add a client" and in the limits. Its value is
// "yes" or "no".
export function CanConnect({
  options,
  defaultValue,
}: {
  options: [string, string][];
  defaultValue: string;
}) {
  const id = useId();
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id} className="font-semibold">
        Can this client connect numbers?
      </Label>
      <select id={id} name="can" defaultValue={defaultValue} className={selectClass}>
        {options.map(([value, label]) => (
          <option key={value} value={value}>
            {label}
          </option>
        ))}
      </select>
    </div>
  );
}

const tooMany = "Use a number from 0 to 10000.";

// The limits of a client. The default client has no user, so it has only its number.
export function Limits({ client, orgMax }: { client: Client; orgMax: number }) {
  const save = useChange("updateClient");
  const [wrong, setWrong] = useState(false);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const raw = String(data.get("max")).trim();
    const max = raw === "" ? Number.NaN : Number(raw);
    const bad = !Number.isInteger(max) || max < 0 || max > 10000;
    setWrong(bad);
    if (bad) return;
    save.mutate({
      path: { id: client.id },
      body: {
        max_instances: max,
        ...(client.is_default ? {} : { can_create_instances: data.get("can") === "yes" }),
      },
    });
  }

  return (
    // A new value from the server gives a new form, with the stored values.
    <form
      key={`${client.can_create_instances}-${client.max_instances}`}
      onSubmit={submit}
      noValidate
      className="grid gap-4"
    >
      {!client.is_default && (
        <CanConnect
          options={[
            ["yes", "Yes"],
            ["no", "No"],
          ]}
          defaultValue={client.can_create_instances ? "yes" : "no"}
        />
      )}
      <Field
        label="Number of instances"
        name="max"
        type="number"
        inputMode="numeric"
        min={0}
        max={10000}
        defaultValue={client.max_instances}
        hint={`0 means no limit of its own. The organization allows ${orgMax} in all.`}
        error={wrong || fieldFailed(save.error, "max_instances") ? tooMany : null}
      />
      {save.isError && (
        <Failure
          failure={save.error}
          context={{ operationId: "updateClient", role: "owner" }}
          checking={save.checking}
        />
      )}
      {save.isSuccess && !wrong && <Notice tone="ok" title="The limits are saved." />}
      <Button type="submit" className="justify-self-start" disabled={save.isPending}>
        Save limits
      </Button>
    </form>
  );
}
