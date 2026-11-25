import { type ApiReferenceConfiguration, ApiReferenceReact } from "@scalar/api-reference-react";
import { ArrowDown, ArrowLeft } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Failure } from "@/components/failure";
import { Notice } from "@/components/notice";
import { Address, MCP_OFF_TEXT, sdks } from "@/components/program-help";
import { isDark, ThemeButton } from "@/components/theme-button";
import { Button } from "@/components/ui/button";
import { Wordmark } from "@/components/wordmark";
import { useApiDocument, useBackendVersion, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { isUnauthorized, loginPath } from "../app/session.tsx";
import { filterForRole, type OpenApiDocument } from "../domain/api-docs.ts";
import { people } from "../domain/people.ts";
import { ScalarBoundary } from "./scalar-boundary.tsx";

// The settings of Scalar. The page sends no request to an operation, and nothing to another origin.
// A local address (localhost, 127.0.0.1) makes Scalar show a toolbar and "Ask AI" unless
// `showDeveloperTools` and `agent` turn them off.
const settings = {
  hideTestRequestButton: true,
  hideClientButton: true,
  withDefaultFonts: false,
  telemetry: false,
  hideDarkModeToggle: true,
  hideModels: true,
  documentDownloadType: "none",
  showDeveloperTools: "never",
  agent: { disabled: true },
} as const;

// The languages of the Calyx SDKs that Scalar supports. Go has one client (native), so it stays.
// The keys are checked against @scalar/types 0.32.0. A Scalar upgrade can rename them.
const hiddenClients: NonNullable<ApiReferenceConfiguration["hiddenClients"]> = {
  c: true,
  clojure: true,
  csharp: true,
  dart: true,
  fsharp: true,
  http: true,
  java: true,
  js: true,
  julia: true,
  kotlin: true,
  objc: true,
  ocaml: true,
  powershell: true,
  r: true,
  ruby: true,
  rust: true,
  swift: true,
  shell: ["httpie", "wget"],
  node: ["axios", "ofetch", "undici"],
  python: ["python3", "httpx_sync", "httpx_async", "aiohttp"],
  php: ["curl", "laravel"],
};

function TryAgain({ onClick }: { onClick: () => void }) {
  return (
    <Button type="button" variant="outline" className="justify-self-start" onClick={onClick}>
      Try again
    </Button>
  );
}

export function DocsPage() {
  const me = useMe();
  const read = useApiDocument();
  const backendVersion = useBackendVersion().data;
  const apiDocument = read.data as OpenApiDocument | undefined;
  const info = apiDocument?.info as { version?: string } | undefined;
  const contract = info?.version;
  const meFailure = me.error as unknown as RequestFailure | null;
  const [dark, setDark] = useState(isDark);
  const referenceRef = useRef<HTMLDivElement>(null);

  const unauthorized = me.isError && isUnauthorized(me.error);
  useEffect(() => {
    if (unauthorized) location.replace(loginPath({ pathname: "/docs", search: "" }));
  }, [unauthorized]);

  const role = me.data?.user.role === "owner" ? "owner" : "client";
  const content = useMemo(
    () => (apiDocument && me.data ? filterForRole(apiDocument, role) : undefined),
    [apiDocument, me.data, role],
  );
  // A new object on each render would make Scalar read the document again.
  const configuration = useMemo(
    () =>
      content && {
        ...settings,
        hiddenClients,
        content,
        servers: [{ url: location.origin }],
        forceDarkModeState: dark ? ("dark" as const) : ("light" as const),
      },
    [content, dark],
  );

  if (unauthorized) return null;

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="border-b border-border bg-card">
        <div className="mx-auto flex min-h-17 w-full max-w-[1120px] items-center gap-x-3 px-4 sm:gap-x-6 sm:px-7">
          <a
            href="/"
            className="inline-flex min-h-11 items-center gap-1.5 px-0.5 font-medium whitespace-nowrap text-muted-foreground hover:text-primary"
          >
            <ArrowLeft aria-hidden="true" className="size-4" />
            Back to Calyx
          </a>
          <div className="flex min-h-11 items-center gap-2.5 text-foreground">
            <img src="/calyx-logo.svg" alt="" className="h-6.5 w-7 sm:h-7 sm:w-8" />
            <Wordmark className="h-6 w-auto" />
          </div>
          <div className="ml-auto">
            <ThemeButton onChange={setDark} />
          </div>
        </div>
      </header>
      <main className="grid flex-1 content-start gap-6 pt-2 pb-14 sm:pt-3">
        {configuration && (
          <div className="mx-auto flex w-full max-w-[1120px] justify-end px-4 sm:px-7">
            <button
              type="button"
              onClick={() => {
                // A button, not an anchor: an anchor would change the hash, and Scalar adds that hash to its own links.
                const reference = referenceRef.current;
                reference?.scrollIntoView();
                reference?.focus({ preventScroll: true });
              }}
              className="inline-flex min-h-11 cursor-pointer items-center gap-1.5 font-medium text-primary hover:underline"
            >
              Go to the API reference
              <ArrowDown aria-hidden="true" className="size-4" />
            </button>
          </div>
        )}
        <div className="mx-auto grid w-full max-w-[1120px] gap-4 px-4 sm:px-7">
          <section aria-labelledby="ways-title" className="grid gap-4">
            <h2 id="ways-title" className="m-0">
              Ways to connect to Calyx
            </h2>
            <div className="grid gap-4 sm:grid-cols-3">
              <section aria-labelledby="way-http" className="grid content-start gap-1.5">
                <h3 id="way-http" className="m-0">
                  HTTP API
                </h3>
                <Address value={`${location.origin}/api/v1`} label="Copy the HTTP API address" />
                <p className="m-0 mt-1 text-muted-foreground">Official SDKs:</p>
                <ul className="m-0 grid list-none gap-1 p-0">
                  {sdks.map(({ language, name, kind }) => (
                    <li key={name}>
                      <span className="text-muted-foreground">{language}</span>{" "}
                      <code translate="no" className="font-mono break-all">
                        {name}
                      </code>{" "}
                      <span className="text-muted-foreground">{kind}</span>
                    </li>
                  ))}
                </ul>
              </section>
              <section aria-labelledby="way-openapi" className="grid content-start gap-1.5">
                <h3 id="way-openapi" className="m-0">
                  OpenAPI document
                </h3>
                <Address
                  value={`${location.origin}/openapi.json`}
                  label="Copy the OpenAPI address"
                />
              </section>
              <section aria-labelledby="way-mcp" className="grid content-start gap-1.5">
                <h3 id="way-mcp" className="m-0">
                  MCP server
                </h3>
                <Address value={`${location.origin}/mcp`} label="Copy the MCP address" />
                <p className="m-0 text-muted-foreground">{MCP_OFF_TEXT}</p>
              </section>
            </div>
            {contract && <p className="m-0">API contract: {contract}</p>}
            {backendVersion !== undefined && (
              <p className="m-0">Running backend: {backendVersion || "unknown"}</p>
            )}
            <dl className="m-0 grid gap-3">
              {people.map((person) => (
                <div key={person.name}>
                  <dt className="font-semibold">{person.name}</dt>
                  <dd className="m-0">{person.text}</dd>
                </div>
              ))}
            </dl>
          </section>
          <p className="m-0">
            The file /openapi.json has every operation of the API. This page shows only the
            operations that your role can call.
          </p>
          <p className="m-0">
            The language selector changes the language of every example on this page.
          </p>
          {meFailure && (
            <>
              <Failure failure={meFailure} context={{ operationId: "getMe", role: null }} />
              <TryAgain onClick={() => me.refetch()} />
            </>
          )}
          {read.isError && (
            <>
              <Notice tone="bad" title="The page cannot read the API document." />
              <TryAgain onClick={() => read.refetch()} />
            </>
          )}
        </div>
        {configuration && (
          <div ref={referenceRef} data-slot="reference" tabIndex={-1} className="outline-none">
            <div data-slot="scalar">
              <ScalarBoundary>
                {/* Scalar reads forceDarkModeState only when it mounts, so a new theme mounts it again. */}
                <ApiReferenceReact key={dark ? "dark" : "light"} configuration={configuration} />
              </ScalarBoundary>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}
