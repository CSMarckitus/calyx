import { ChevronDown } from "lucide-react";
import { type ReactNode, useId, useMemo, useState } from "react";
import { Link, Outlet, useMatch, useNavigate } from "react-router";
import { AddClient } from "@/components/add-client";
import { Failure } from "@/components/failure";
import { type Instance, InstanceCard, type Viewing } from "@/components/instance-card";
import { InstanceCell, Marked } from "@/components/instance-cell";
import { TermsNotice } from "@/components/terms-notice";
import { Button, buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { components } from "../api/generated/types.ts";
import { type Health, useHealth } from "../api/health.ts";
import { useCheckAgain, useClients, useInstances, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { creationProblem } from "../domain/creation.ts";
import { endDraftNamed } from "../domain/draft.ts";
import { explain, type Role, unknownManager } from "../domain/errors.ts";
import { type Described, describe, type Filter } from "../domain/labels.ts";
import { BulkJobsView } from "./bulk-jobs.tsx";
import { WebhooksView } from "./webhooks.tsx";

type Me = components["schemas"]["Me"];
type Client = components["schemas"]["Client"];
type Choice = "all" | NonNullable<Filter>;

const filters: [Choice, string][] = [
  ["all", "All"],
  ["person", "Need a person"],
  ["wait", "Reconnecting"],
  ["ok", "Connected"],
  ["off", "Access off"],
];

const isDown = (h: Health) => h.state === "core" || h.state === "database" || h.state === "backend";

// Why a person cannot connect a number, the person to ask, and "Check again". No event
// comes for a change of the owner, so the person asks for the read.
export function CreationReason({
  code,
  who: role,
  manager,
}: {
  code: string;
  who: Role;
  manager: string;
}) {
  const again = useCheckAgain();
  const { cause, nextStep } = explain(
    { kind: "problem", status: 403, code, detail: "", fields: [], requestId: "" },
    { operationId: "createInstance", role, manager },
  );
  return (
    <div
      data-slot="creation-problem"
      className="grid gap-1.5 rounded-md bg-wait-soft px-4 py-3.5 text-[0.9375rem]"
    >
      <b className="font-semibold">{cause}</b>
      <span>{nextStep}</span>
      <div className="flex flex-wrap items-center gap-x-5">
        <Button
          variant="link"
          className="px-0 font-semibold"
          disabled={again.pending}
          onClick={() => void again.check()}
        >
          Check again
        </Button>
      </div>
    </div>
  );
}

// The search and the box of filters at the side. At a narrow width the filters fold.
function Side({
  owner,
  query,
  setQuery,
  filter,
  setFilter,
  counts,
  clients,
  jump,
}: {
  owner: boolean;
  query: string;
  setQuery: (q: string) => void;
  filter: Choice;
  setFilter: (f: Choice) => void;
  counts: Record<Choice, number>;
  clients: { id: string; name: string; count: number; person: boolean }[];
  jump: (id: string) => void;
}) {
  const [fold, setFold] = useState(false);
  const shown = filters.find(([k]) => k === filter)?.[1];
  const option =
    "flex min-h-11 w-full cursor-pointer items-center justify-between gap-2.5 rounded-md px-3 text-left hover:bg-accent hover:text-primary aria-pressed:bg-primary aria-pressed:text-primary-foreground lg:min-h-10";
  return (
    <aside
      aria-label="Find and filter"
      className="grid gap-4 rounded-lg border border-border bg-card p-4 lg:sticky lg:top-5 lg:max-h-[calc(100dvh-40px)] lg:overflow-auto"
    >
      <input
        type="search"
        aria-label="Find a number"
        placeholder={owner ? "Name or client" : "Name"}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="min-h-11 w-full rounded-md border border-border bg-background px-3 text-base"
      />
      <Button
        variant="outline"
        className="w-full lg:hidden"
        aria-expanded={fold}
        onClick={() => setFold(!fold)}
      >
        {fold ? "Hide filters" : filter === "all" ? "Filters" : `Filters: ${shown}`}
      </Button>
      <div className={cn("gap-3.5 max-lg:hidden lg:grid", fold && "max-lg:grid")}>
        <details open className="group/state">
          <summary className="flex min-h-10 cursor-pointer items-center justify-between font-semibold">
            State
            <ChevronDown
              aria-hidden="true"
              className="size-4 flex-none transition-transform group-open/state:rotate-180"
            />
          </summary>
          <fieldset aria-label="State" className="m-0 mt-0.5 grid gap-0.5 border-0 p-0">
            {filters.map(([k, label]) => (
              <button
                key={k}
                type="button"
                aria-pressed={filter === k}
                className={option}
                onClick={() => setFilter(k)}
              >
                <span>{label}</span>
                <b
                  className={cn(
                    "font-medium tabular-nums",
                    k === "person" && counts.person > 0 && filter !== k && "font-semibold text-bad",
                  )}
                >
                  {counts[k]}
                </b>
              </button>
            ))}
          </fieldset>
        </details>
        {owner && (
          <details className="group/clients border-t border-border pt-1.5">
            <summary className="flex min-h-10 cursor-pointer items-center justify-between font-semibold">
              {`Clients (${clients.length})`}
              <ChevronDown
                aria-hidden="true"
                className="size-4 flex-none transition-transform group-open/clients:rotate-180"
              />
            </summary>
            <div className="mt-0.5 grid gap-0.5">
              {clients.map((c) => (
                <button key={c.id} type="button" className={option} onClick={() => jump(c.id)}>
                  <span translate="no" className="[overflow-wrap:anywhere]">
                    {c.name}
                  </span>
                  <b
                    className={cn("font-medium tabular-nums", c.person && "font-semibold text-bad")}
                  >
                    {c.count}
                  </b>
                </button>
              ))}
            </div>
          </details>
        )}
      </div>
    </aside>
  );
}

type Row = { instance: Instance; d: Described; at: number };

function Cells({
  rows,
  viewing,
  length,
  last,
}: {
  rows: Row[];
  viewing: Viewing;
  length: number;
  last?: ReactNode;
}) {
  return (
    <div className="grid grid-cols-1 overflow-hidden sm:grid-cols-[repeat(auto-fill,minmax(250px,1fr))]">
      {rows.map((r) => (
        <InstanceCell
          key={r.instance.id}
          instance={r.instance}
          described={r.d}
          viewing={viewing}
          at={r.at}
          length={length}
        />
      ))}
      {last}
    </div>
  );
}

// One client of the owner: its name, "Manage client" and its numbers, with "+ Connect a number" last.
function Group({
  client,
  rows,
  viewing,
  query,
  orgBlocked,
  problem,
  plain,
}: {
  client: Client;
  rows: Row[];
  viewing: Viewing;
  query: string;
  orgBlocked: boolean;
  problem: string | null;
  plain: boolean;
}) {
  const navigate = useNavigate();
  const title = useId();
  const name = client.is_default ? "My numbers" : client.name;
  const at = query ? name.toLowerCase().indexOf(query) : -1;
  // The last cell of the grid. With a search, a filter or a block of the organization: no card.
  const card =
    plain && !orgBlocked ? (
      <div data-slot="connect-card" className="grid content-center justify-items-start p-4.5">
        {problem === null ? (
          <Button
            variant="outline"
            disabled={viewing.down}
            onClick={() => void navigate(`/instances/new?client=${client.id}`)}
          >
            + Connect a number
          </Button>
        ) : (
          <CreationReason code={problem} who="owner" manager={viewing.manager} />
        )}
      </div>
    ) : null;
  return (
    <section
      id={`client-${client.id}`}
      aria-labelledby={title}
      className="scroll-mt-5 overflow-hidden rounded-lg border border-border bg-card"
    >
      <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-1 border-b border-border px-4.5 py-3 sm:px-5">
        <h2 id={title} translate={client.is_default ? undefined : "no"}>
          <Marked text={name} at={at} length={query.length} />
        </h2>
        <div className="flex flex-wrap gap-x-5">
          <Link
            to={`/clients/${client.id}`}
            className={buttonVariants({ variant: "link", className: "px-0 font-semibold" })}
          >
            Manage client
          </Link>
        </div>
      </div>
      {rows.length > 0 || card ? (
        <Cells rows={rows} viewing={viewing} length={query.length} last={card} />
      ) : (
        <p className="m-0 px-4.5 py-4 text-muted-foreground sm:px-5">No numbers yet.</p>
      )}
    </section>
  );
}

function NothingMatches({ clear }: { clear: () => void }) {
  return (
    <div className="grid justify-items-start gap-3">
      <p className="m-0">Nothing matches.</p>
      <Button variant="outline" onClick={clear}>
        Show all
      </Button>
    </div>
  );
}

// The page of the list: the search and the filters, and the results. The drawer of an instance
// is the outlet, so the list stays mounted below it and keeps its position.
function ListPage({
  owner,
  rows,
  viewing,
  clients,
  orgProblem,
  clientProblem,
  connect,
}: {
  owner: boolean;
  rows: Row[];
  viewing: Viewing;
  clients: Client[];
  orgProblem: string | null;
  clientProblem: (c: Client) => string | null;
  connect: ReactNode;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Choice>("all");
  const q = query.trim().toLowerCase();
  const plain = q === "" && filter === "all";

  const counts = useMemo(() => {
    const c: Record<Choice, number> = { all: rows.length, person: 0, wait: 0, ok: 0, off: 0 };
    for (const r of rows) if (r.d.filter) c[r.d.filter]++;
    return c;
  }, [rows]);

  const shown = useMemo(() => {
    const out: Row[] = [];
    for (const r of rows) {
      if (filter !== "all" && r.d.filter !== filter) continue;
      const name = r.instance.display_name.toLowerCase();
      if (q) {
        const client = owner ? (viewing.clientName(r.instance.client_id) ?? "").toLowerCase() : "";
        if (!`${name} ${client}`.includes(q)) continue;
      }
      out.push({ ...r, at: q ? name.indexOf(q) : -1 });
    }
    return out;
  }, [rows, filter, q, owner, viewing]);

  const sideClients = useMemo(
    () =>
      clients.map((c) => {
        const mine = rows.filter((r) => r.instance.client_id === c.id);
        return {
          id: c.id,
          name: c.is_default ? "My numbers" : c.name,
          count: mine.length,
          person: mine.some((r) => r.d.filter === "person"),
        };
      }),
    [clients, rows],
  );

  function clear() {
    setQuery("");
    setFilter("all");
  }

  function jump(id: string) {
    clear();
    // The group shows after the next render.
    requestAnimationFrame(() =>
      document.getElementById(`client-${id}`)?.scrollIntoView({ block: "start" }),
    );
  }

  let results: ReactNode;
  if (shown.length === 0 && !(owner && plain)) {
    results = <NothingMatches clear={clear} />;
  } else if (owner) {
    const byClient = new Map<string, Row[]>();
    for (const r of shown) {
      const list = byClient.get(r.instance.client_id) ?? [];
      list.push(r);
      byClient.set(r.instance.client_id, list);
    }
    results = (
      <div className="grid gap-6.5">
        {clients
          .filter((c) => plain || byClient.has(c.id))
          .map((c) => (
            <Group
              key={c.id}
              client={c}
              rows={byClient.get(c.id) ?? []}
              viewing={viewing}
              query={q}
              orgBlocked={orgProblem !== null}
              problem={clientProblem(c)}
              plain={plain}
            />
          ))}
      </div>
    );
  } else {
    results = (
      <div className="overflow-hidden rounded-lg border border-border bg-card">
        <Cells rows={shown} viewing={viewing} length={q.length} />
      </div>
    );
  }

  const person = counts.person;
  return (
    <>
      <h1>{owner ? "Instances" : "My WhatsApp numbers"}</h1>
      <div className="grid items-start gap-4.5 lg:grid-cols-[236px_minmax(0,1fr)] lg:gap-9">
        <Side
          owner={owner}
          query={query}
          setQuery={setQuery}
          filter={filter}
          setFilter={setFilter}
          counts={counts}
          clients={sideClients}
          jump={jump}
        />
        <div className="grid gap-5">
          {owner && orgProblem !== null && (
            <CreationReason code={orgProblem} who="owner" manager={viewing.manager} />
          )}
          {person > 0 && !viewing.down && plain && (
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 rounded-md bg-bad-soft px-4.5 py-2.5">
              <b className="font-semibold">
                {`${person} ${person > 1 ? "numbers need" : "number needs"} a person.`}
              </b>
              <Button
                variant="link"
                className="min-h-11 px-0 text-bad underline underline-offset-3"
                onClick={() => setFilter("person")}
              >
                Show only these
              </Button>
            </div>
          )}
          {results}
          {plain && connect}
        </div>
      </div>
    </>
  );
}

// What a client user sees when it can connect a number, or why it cannot.
function ClientConnect({
  me,
  viewing,
  label,
  primary,
}: {
  me: Me;
  viewing: Viewing;
  label: string;
  primary?: boolean;
}) {
  const navigate = useNavigate();
  if (me.create_instance_problem !== null)
    return (
      <CreationReason code={me.create_instance_problem} who="client" manager={viewing.manager} />
    );
  return (
    <div>
      <Button
        variant={primary ? "default" : "outline"}
        disabled={viewing.down}
        onClick={() => void navigate(`/instances/new?client=${me.user.client_id ?? ""}`)}
      >
        {label}
      </Button>
    </div>
  );
}

// "Continue linking" of each number that a client user started to link.
function ContinueLinking({ linking, viewing }: { linking: Instance[]; viewing: Viewing }) {
  return linking.map((i) => (
    <div key={i.id}>
      <Button
        render={<Link to={`/instances/${i.id}`} />}
        nativeButton={false}
        onClick={() => endDraftNamed(i.client_id, i.display_name, viewing.userId)}
      >
        <span>Continue linking</span> <span translate="no">{i.display_name}</span>
      </Button>
    </div>
  ));
}

// The screen of `/` and the frame of `/instances/...`: the list of the owner, or the screen of a
// client user for none, one or several numbers.
export function InstancesScreen() {
  const me = useMe();
  const data = me.data;
  const owner = data?.user.role === "owner";
  const instances = useInstances();
  const clients = useClients(owner);
  const health = useHealth();
  const navigate = useNavigate();
  const viewMatch = useMatch("/instances/:id/:view");
  const [mountedAt] = useState(() => Date.now());
  // `now` is the time of the last health answer, so a label changes with no event.
  const now = health.lastAnswerAt ?? mountedAt;
  const down = isDown(health);
  const manager = data?.organization.name ?? unknownManager;

  const items = instances.data?.items;
  const fresh = health.dataIsFresh;
  const state = health.state;
  const lastGoodAt = health.lastGoodAt;
  const rows = useMemo<Row[]>(
    () =>
      (items ?? []).map((instance) => ({
        instance,
        d: describe(instance, { state, dataIsFresh: fresh, lastGoodAt }, now, {
          viewer: owner ? "owner" : "client",
          manager,
        }),
        at: -1,
      })),
    [items, state, fresh, lastGoodAt, now, owner, manager],
  );

  const clientList = clients.data?.items;
  const sorted = useMemo(
    () =>
      [...(clientList ?? [])].sort((a, b) =>
        a.is_default === b.is_default ? a.name.localeCompare(b.name) : a.is_default ? -1 : 1,
      ),
    [clientList],
  );

  const org = data?.organization;
  const orgLimits = {
    whatsapp_web_allowed: org?.whatsapp_web_allowed ?? true,
    whatsapp_web_acknowledged_at: org?.whatsapp_web_acknowledged_at ?? null,
    instance_count: org?.instance_count ?? 0,
    max_instances: org?.max_instances ?? 0,
  };
  const userId = data?.user.id ?? "";
  const blocked = owner
    ? org?.whatsapp_web_allowed === false
    : data?.create_instance_problem === "whatsapp_web_not_allowed";
  const viewing = useMemo<Viewing>(() => {
    const names = new Map((clientList ?? []).map((c) => [c.id, c.name]));
    return {
      viewer: owner ? "owner" : "client",
      manager,
      userId,
      down,
      blocked,
      clientName: (id) => names.get(id),
    };
  }, [owner, manager, userId, down, blocked, clientList]);

  // The drawer waits for the list, because it shows a row of it.
  if (!data) return null;
  // The terms gate of the owner, for every provider. A client user is never blocked.
  if (owner && org?.whatsapp_web_acknowledged_at === null) {
    return (
      <div data-slot="terms-notice">
        <TermsNotice blocked={org.whatsapp_web_allowed === false} />
      </div>
    );
  }
  if (!instances.data || (owner && !clients.data)) {
    const failed = instances.error ?? clients.error;
    return (
      <>
        {failed && (
          <Failure
            failure={failed as unknown as RequestFailure}
            context={{
              operationId: instances.error ? "listInstances" : "listClients",
              role: owner ? "owner" : "client",
              manager,
            }}
          />
        )}
      </>
    );
  }

  let page: ReactNode;
  // The one number of a client user: its screen is the page, also at its own path.
  let single: string | undefined;
  if (owner) {
    // The rules of the organization, alone: a client with no limit of its own.
    const orgProblem = creationProblem(orgLimits, { instance_count: 0, max_instances: 0 });
    page = (
      <ListPage
        owner
        rows={rows}
        viewing={viewing}
        clients={sorted}
        orgProblem={orgProblem}
        clientProblem={(c) => creationProblem(orgLimits, c)}
        connect={<AddClient />}
      />
    );
  } else {
    // The count leaves out `creating`.
    const linking = rows.filter((r) => r.instance.state === "creating").map((r) => r.instance);
    const counted = rows.filter((r) => r.instance.state !== "creating");
    if (counted.length === 0) {
      page = (
        <div className="grid max-w-[760px] gap-4">
          <h1>Connect your WhatsApp</h1>
          <p className="m-0 text-muted-foreground">
            Link your phone once. After that, the programs of your company can send messages from
            your number.
          </p>
          {linking.length > 0 ? (
            <ContinueLinking linking={linking} viewing={viewing} />
          ) : (
            <ClientConnect me={data} viewing={viewing} label="Connect a WhatsApp number" primary />
          )}
        </div>
      );
    } else if (counted.length === 1) {
      const one = counted[0] as Row;
      single = one.instance.id;
      const shownView =
        viewMatch?.params.id === one.instance.id ? viewMatch.params.view : undefined;
      page = (
        <div className="grid max-w-[760px] gap-4">
          {shownView === "webhooks" ? (
            <WebhooksView instance={one.instance} level={1} />
          ) : shownView === "bulk-jobs" ? (
            <BulkJobsView instance={one.instance} level={1} />
          ) : (
            <InstanceCard
              instance={one.instance}
              described={one.d}
              viewing={viewing}
              now={now}
              level={1}
              onRemoved={() => void navigate("/", { replace: true })}
            />
          )}
          <ContinueLinking linking={linking} viewing={viewing} />
          <ClientConnect me={data} viewing={viewing} label="Connect another number" />
        </div>
      );
    } else {
      page = (
        <ListPage
          owner={false}
          rows={rows}
          viewing={viewing}
          clients={[]}
          orgProblem={null}
          clientProblem={() => null}
          connect={<ClientConnect me={data} viewing={viewing} label="Connect a number" />}
        />
      );
    }
  }

  // The client of "Connect", its name for an owner, and why the person cannot connect a number
  // in it. undefined: the page does not know this client.
  const connectTo = (id: string | null): ConnectTo | undefined => {
    if (!owner) {
      const own = data.user.client_id;
      if (id !== null && id !== own) return undefined;
      return own === null ? undefined : { id: own, problem: data.create_instance_problem };
    }
    const client = sorted.find((c) => (id === null ? c.is_default : c.id === id));
    if (!client) return undefined;
    return {
      id: client.id,
      name: client.is_default ? "My numbers" : client.name,
      problem: creationProblem(orgLimits, client),
    };
  };

  return (
    <>
      {page}
      <Outlet context={{ rows, viewing, now, single, connectTo } satisfies ListOutlet} />
    </>
  );
}

export type ConnectTo = { id: string; name?: string; problem: string | null };

// What the drawer of an instance gets from the list.
export type ListOutlet = {
  rows: Row[];
  viewing: Viewing;
  now: number;
  single?: string;
  connectTo: (client: string | null) => ConnectTo | undefined;
};
