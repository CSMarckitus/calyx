import { ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
import { CopyButton } from "./copy-button.tsx";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible.tsx";

// The help for programs. The dialog of a new key gives the real key and
// headings of level 3. The screen gives no key, so the texts show a placeholder.

type Level = 2 | 3;

const tools = [
  "list_instances",
  "get_instance",
  "send_message",
  "create_bulk_job",
  "get_bulk_job",
  "cancel_bulk_job",
];

// The official SDK packages, for the panel and the docs page. The packages are not published
// yet, so neither gives an install command. `kind` says what the package covers. A core client
// is for the private network, so the panel lists only the packages that cover the backend.
export type SdkKind = "Backend and core client" | "Backend client" | "Core client";
export const sdks: { language: string; name: string; kind: SdkKind }[] = [
  { language: "TypeScript", name: "@calyx/client-sdk", kind: "Backend and core client" },
  {
    language: "Go",
    name: "github.com/csmarckitus/calyx/packages/client-sdk/go",
    kind: "Backend and core client",
  },
  { language: "PHP", name: "calyx/client-sdk", kind: "Backend client" },
  { language: "PHP", name: "calyx/client-sdk-core", kind: "Core client" },
  { language: "Python", name: "calyx-client-sdk", kind: "Backend client" },
  { language: "Python", name: "calyx-client-sdk-core", kind: "Core client" },
  { language: "VB6", name: "packages/client-sdk/vb6", kind: "Backend client" },
];

function Heading({ level, children }: { level: Level; children: ReactNode }) {
  const H = level === 2 ? "h2" : "h3";
  return <H className="m-0">{children}</H>;
}

function Part({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <b className="font-semibold">{title}</b>
      {children}
    </div>
  );
}

export function Address({ value, label }: { value: string; label: string }) {
  return (
    <>
      <code translate="no" className="font-mono break-all">
        {value}
      </code>
      <div>
        <CopyButton text={value} label={label} />
      </div>
    </>
  );
}

export const MCP_OFF_TEXT = "If the address answers 404, the server admin turned MCP off.";

const muted = "m-0 text-muted-foreground";

export function ConnectProgram({ level, apiKey }: { level: Level; apiKey?: string }) {
  return (
    <div className="grid gap-4">
      <Heading level={level}>Connect a program</Heading>
      <Part title="The address of Calyx">
        <Address value={window.location.origin} label="Copy the address" />
        <p className={muted}>A program on another network can need another address.</p>
      </Part>
      <Part title="The header">
        <code translate="no" className="font-mono break-all">
          Authorization: Bearer {apiKey ?? "cx_..."}
        </code>
        <p className={muted}>
          Some programs call the key a token or a Bearer token. Paste the whole key.
        </p>
      </Part>
      <Part title="The instance IDs">
        <p className={muted}>Each instance has its ID on its card.</p>
      </Part>
      <p className="m-0">
        The key is for the HTTP API and for MCP. Core clients use the token of the core, which the
        server admin holds.
      </p>
      <Part title="The SDKs">
        <p className={muted}>The SDK packages are not published yet.</p>
        <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          {sdks
            .filter(({ kind }) => kind !== "Core client")
            .map(({ language, name }) => (
              <div key={name} className="contents">
                <dt className="text-muted-foreground">{language}</dt>
                <dd className="m-0 font-mono break-all" translate="no">
                  {name}
                </dd>
              </div>
            ))}
        </dl>
      </Part>
    </div>
  );
}

// `open`: the connect screen shows the part open. The dialog of a new key keeps it closed.
export function ConnectMcp({
  level,
  owner,
  open = false,
}: {
  level: Level;
  owner: boolean;
  open?: boolean;
}) {
  return (
    <Collapsible defaultOpen={open}>
      <Heading level={level}>
        <CollapsibleTrigger className="group flex min-h-11 w-full cursor-pointer items-center justify-between gap-2 text-left">
          Connect an MCP client
          <ChevronDown
            aria-hidden="true"
            className="size-5 flex-none transition-transform group-data-[panel-open]:rotate-180"
          />
        </CollapsibleTrigger>
      </Heading>
      <CollapsibleContent className="grid gap-4 pt-3">
        <Part title="The MCP address">
          <Address value={`${window.location.origin}/mcp`} label="Copy the MCP address" />
        </Part>
        <p className="m-0">
          Use a program where you can set a URL and a Bearer token. AI apps on a website cannot
          connect to Calyx yet.
        </p>
        <p className={muted}>{MCP_OFF_TEXT}</p>
        <Part title="The tools">
          <ul className="m-0 grid gap-1 pl-5">
            {tools.map((t) => (
              <li key={t}>
                <code translate="no">{t}</code>
              </li>
            ))}
          </ul>
        </Part>
        <div className="grid gap-1.5 rounded-md bg-wait-soft px-4 py-3.5">
          <b className="font-semibold">
            A program with this key can do all that you can do, on every instance that you can see,
            bulk jobs included. It cannot log in, change your password or create keys.
          </b>
          {owner && (
            <span>
              Do not give your key to a program of a client. Ask the client user to create a key.
            </span>
          )}
        </div>
        <p className="m-0">
          Caution: the text of a message can tell the AI what to do. Give a key only to an AI that
          you trust.
        </p>
        <p className="m-0">
          Caution: the answers of the tools go to the vendor of the AI, webhook URLs included. A
          webhook URL can be a secret.
        </p>
        <p className="m-0">Create one key for each program, so you can revoke one alone.</p>
        <p className={muted}>
          To find a tool call in the logs, the server admin follows "Find a tool call in the logs"
          in the README of the backend. It uses <code translate="no">mcp_request_id</code>.
        </p>
      </CollapsibleContent>
    </Collapsible>
  );
}
