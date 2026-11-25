import { ChevronDown, Container, Terminal } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { Link } from "react-router";
import { Field, fieldFailed } from "@/components/access-page";
import { CopyButton } from "@/components/copy-button";
import { Failure } from "@/components/failure";
import { Section } from "@/components/section";
import { Button, buttonVariants } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import type { components } from "../api/generated/types.ts";
import { useChange, useMe } from "../api/queries.ts";
import { people } from "../domain/people.ts";

type Organization = components["schemas"]["MeOrganization"];

function Command({ value, label }: { value: string; label: string }) {
  return (
    <>
      <code
        translate="no"
        className="block rounded-md bg-muted px-3 py-2 font-mono text-sm break-all"
      >
        {value}
      </code>
      <div>
        <CopyButton text={value} label={label} />
      </div>
    </>
  );
}

function LimitHelp({ id, instances, max }: { id: string; instances: number; max: number }) {
  const [open, setOpen] = useState(false);
  const command = `backend org set --id ${id} --max-instances <number>`;
  return (
    <div className="flex flex-wrap items-center gap-x-4">
      <p className="m-0">
        Instances: {instances} of {max}.{" "}
        <span className="text-muted-foreground">Only the server admin can change this limit.</span>
      </p>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger className="inline-flex min-h-11 cursor-pointer items-center gap-2 font-medium text-primary underline-offset-4 hover:underline">
          How do I change the limit?
          <ChevronDown
            aria-hidden="true"
            className={cn("size-4 flex-none transition-transform", open && "rotate-180")}
          />
        </PopoverTrigger>
        <PopoverContent
          align="start"
          aria-label="How do I change the limit?"
          className="w-[min(28rem,calc(100vw-2rem))] p-3"
        >
          <Tabs defaultValue="docker">
            <TabsList aria-label="How do you run Calyx?" activateOnFocus>
              <TabsTrigger value="docker">
                <Container aria-hidden="true" />
                Docker
              </TabsTrigger>
              <TabsTrigger value="manual">
                <Terminal aria-hidden="true" />
                Manual
              </TabsTrigger>
            </TabsList>
            <TabsContent value="docker">
              <Command
                value={`docker compose exec backend ${command}`}
                label="Copy the Docker command"
              />
            </TabsContent>
            <TabsContent value="manual">
              <Command value={command} label="Copy the command" />
            </TabsContent>
          </Tabs>
        </PopoverContent>
      </Popover>
    </div>
  );
}

export function OrganizationSection({ org }: { org: Organization }) {
  const rename = useChange("updateOrganization");

  function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    rename.mutate({ body: { name: String(new FormData(e.currentTarget).get("name")).trim() } });
  }

  const web =
    org.whatsapp_web_allowed === false
      ? "Blocked"
      : org.whatsapp_web_acknowledged_at
        ? "Accepted"
        : "Not accepted yet";
  const accepted = Boolean(org.whatsapp_web_acknowledged_at);
  const terms = accepted && (
    <Link to="/organization/whatsapp-web" className="font-semibold text-primary underline">
      Read the WhatsApp Web terms
    </Link>
  );
  const facts: [string, ReactNode][] = [
    ["Mail", org.smtp_configured ? "Configured" : "Not configured"],
    [
      "WhatsApp Web",
      <span className="flex flex-wrap gap-x-4">
        <span>{web}</span>
        {terms}
      </span>,
    ],
  ];
  return (
    <Section title="Organization">
      <form onSubmit={save} noValidate className="grid gap-4">
        <Field
          label="Name"
          name="name"
          defaultValue={org.name}
          maxLength={100}
          translate="no"
          error={fieldFailed(rename.error, "name") ? "Use 1 to 100 characters." : null}
          required
        />
        {rename.isError && (
          <Failure
            failure={rename.error}
            context={{ operationId: "updateOrganization", role: "owner" }}
            checking={rename.checking}
          />
        )}
        <Button type="submit" className="justify-self-start" disabled={rename.isPending}>
          Save
        </Button>
      </form>
      <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-6 gap-y-2">
        {facts.map(([name, value]) => (
          <div key={name} className="contents">
            <dt className="text-muted-foreground">{name}</dt>
            <dd className="m-0">{value}</dd>
          </div>
        ))}
      </dl>
      {!accepted && (
        <Link
          to="/organization/whatsapp-web"
          className={buttonVariants({ className: "justify-self-start" })}
        >
          Read and accept the terms
        </Link>
      )}
      <LimitHelp id={org.id} instances={org.instance_count ?? 0} max={org.max_instances ?? 0} />
      <h3 className="m-0">Who is who</h3>
      <dl className="m-0 grid gap-3">
        {people.map((person) => (
          <div key={person.name}>
            <dt className="font-semibold">{person.name}</dt>
            <dd className="m-0">{person.text}</dd>
          </div>
        ))}
      </dl>
    </Section>
  );
}

export function OrganizationScreen() {
  const data = useMe().data;
  if (!data) return null;
  return (
    <>
      <h1>Organization</h1>
      <OrganizationSection org={data.organization} />
    </>
  );
}
