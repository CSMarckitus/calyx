import {
  type MutateOptions,
  type Query,
  QueryClient,
  queryOptions,
  type UseMutationResult,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useRef, useState } from "react";
import { getHealth, readHealthz } from "./health.ts";
import type { Answer, JsonOperationId, Params } from "./operations.ts";
import type { Failure } from "./request.ts";
import { request } from "./request.ts";

// The keys. A screen uses the helpers below, so a key has one spelling.
export const keys = {
  me: ["me"],
  instances: ["instances"],
  clients: ["clients"],
  client: (id: string) => ["client", id],
  organization: ["organization"],
  apiKeys: ["api-keys"],
  apiDocument: ["api-document"],
  healthz: ["healthz"],
  bulkJobs: (id: string) => ["bulk-jobs", id],
} as const;

// TanStack Query reads the thrown value, so a failed read throws the failure itself.
async function read<K extends JsonOperationId>(op: K, params: Params<K>, signal: AbortSignal) {
  const outcome = await request(op, params, signal);
  if (!outcome.ok) throw outcome.failure;
  return outcome.data;
}

// A failed `me` is not read again when one more screen mounts: the shell hides its screen during
// the read, so the screen would mount and read again with no end. The stream and the health checks read it again.
export const meOptions = () =>
  queryOptions({
    queryKey: keys.me,
    queryFn: ({ signal }) => read("getMe", {}, signal),
    retryOnMount: false,
  });
export const instancesOptions = () =>
  queryOptions({
    queryKey: keys.instances,
    queryFn: ({ signal }) => read("listInstances", {}, signal),
  });
export const clientsOptions = () =>
  queryOptions({
    queryKey: keys.clients,
    queryFn: ({ signal }) => read("listClients", {}, signal),
  });
export const clientOptions = (id: string) =>
  queryOptions({
    queryKey: keys.client(id),
    queryFn: ({ signal }) => read("getClient", { path: { id } }, signal),
  });
export const organizationOptions = () =>
  queryOptions({
    queryKey: keys.organization,
    queryFn: ({ signal }) => read("getOrganization", {}, signal),
  });
export const apiKeysOptions = () =>
  queryOptions({
    queryKey: keys.apiKeys,
    queryFn: ({ signal }) => read("listAPIKeys", {}, signal),
  });
// Each open of the bulk view reads the jobs, and "Read at" shows when.
export const bulkJobsOptions = (id: string) =>
  queryOptions({
    queryKey: keys.bulkJobs(id),
    queryFn: ({ signal }) => read("listBulkJobs", { path: { id } }, signal),
    refetchOnMount: "always",
  });

// The OpenAPI document of the backend, for the docs page. `request()` takes only operation IDs, so
// this read is a plain same-origin `fetch`. Any bad answer is `unreachable`: nginx answers a
// failed backend with plain text, and the page needs no more detail.
const apiDocumentLimitMs = 30_000;

async function readApiDocument(signal: AbortSignal): Promise<unknown> {
  const control = new AbortController();
  const timer = setTimeout(() => control.abort(), apiDocumentLimitMs);
  const abort = () => control.abort();
  signal.addEventListener("abort", abort);
  if (signal.aborted) control.abort();
  try {
    const res = await fetch("/openapi.json", {
      credentials: "same-origin",
      signal: control.signal,
    });
    if (!res.ok) throw new Error("bad status");
    return JSON.parse(await res.text());
  } catch {
    const failure: Failure = { kind: "unreachable" };
    throw failure;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", abort);
  }
}

export const apiDocumentOptions = () =>
  queryOptions({
    queryKey: keys.apiDocument,
    queryFn: ({ signal }) => readApiDocument(signal),
  });

// The version of the backend, for the docs page. `readHealthz` gives null for any failure, so
// the query never fails: an empty string means "unknown". No retry: the page shows "unknown".
const backendVersionLimitMs = 10_000;

async function readBackendVersion(signal: AbortSignal): Promise<string> {
  const control = new AbortController();
  const timer = setTimeout(() => control.abort(), backendVersionLimitMs);
  const abort = () => control.abort();
  signal.addEventListener("abort", abort);
  if (signal.aborted) control.abort();
  try {
    return (await readHealthz(control.signal))?.version ?? "";
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", abort);
  }
}

export const backendVersionOptions = () =>
  queryOptions({
    queryKey: keys.healthz,
    queryFn: ({ signal }) => readBackendVersion(signal),
    retry: false,
  });

// `enabled: false` is for a role that does not read them (a client user has no clients).
export const useMe = () => useQuery(meOptions());
export const useInstances = () => useQuery(instancesOptions());
export const useClients = (enabled = true) => useQuery({ ...clientsOptions(), enabled });
export const useClient = (id: string, enabled = true) =>
  useQuery({ ...clientOptions(id), enabled });
export const useOrganization = () => useQuery(organizationOptions());
export const useApiKeys = () => useQuery(apiKeysOptions());
export const useApiDocument = () => useQuery(apiDocumentOptions());
export const useBackendVersion = () => useQuery(backendVersionOptions());
export const useBulkJobs = (id: string) => useQuery(bulkJobsOptions(id));

// A change of the person reads again at once, as a `changed` with no wait. The page
// installs the stream's function before the first render.
let refreshAll: () => void = () => getHealth().checkNow();
export function installRefresh(refresh: () => void) {
  refreshAll = refresh;
}
// For a change that does not go through useChange, such as a start of a pairing.
export const refreshNow = () => refreshAll();

// One request at a time: a call while a request of this change runs does nothing. The state
// "pending" comes one task later, so it alone does not stop a double click or Enter twice.
export function useSingle<D, E, V, C>(m: UseMutationResult<D, E, V, C>) {
  const running = useRef(false);
  const mutate = (variables: V, options?: MutateOptions<D, E, V, C>) => {
    if (running.current) return;
    running.current = true;
    m.mutate(variables, {
      ...options,
      onSettled: (...args) => {
        running.current = false;
        options?.onSettled?.(...args);
      },
    });
  };
  return { ...m, mutate };
}

// A change with no answer, or with an answer that does not know the result.
export const isLost = (f: Failure) => f.kind !== "problem" || f.code.endsWith("_outcome_unknown");

// One change of a person: one request, never sent again. After any answer the page reads again,
// so it shows the data and not a guess. After a lost change, `checking` is true until
// the reads of the mounted screens end, so the page can say "Checking the result".
export function useChange<K extends JsonOperationId>(op: K) {
  const client = useQueryClient();
  const [checking, setChecking] = useState(false);
  const change = useMutation<Answer<K>, Failure, Params<K>>({
    mutationKey: [op],
    mutationFn: async (params) => {
      const outcome = await request(op, params);
      if (!outcome.ok) throw outcome.failure;
      return outcome.data;
    },
    onSettled: (_data, failure) => {
      refreshAll();
      if (!failure || !isLost(failure)) return;
      setChecking(true);
      // `cancelRefetch: false` joins the reads that refreshAll started, and sends no new one.
      void client
        .refetchQueries({ type: "active" }, { cancelRefetch: false })
        .finally(() => setChecking(false));
    },
  });
  return { ...useSingle(change), checking };
}

// "Check again" of a blocked person and of a pending invitation: no event comes for
// these changes, so the person asks for the read. It reads `me`, the organization, the clients
// and each client screen (["client", id]).
export function useCheckAgain() {
  const client = useQueryClient();
  const [pending, setPending] = useState(false);
  async function check() {
    setPending(true);
    try {
      await Promise.all(
        [keys.me, keys.organization, keys.clients, ["client"]].map((queryKey) =>
          client.refetchQueries({ queryKey }, { cancelRefetch: false }),
        ),
      );
    } finally {
      setPending(false);
    }
  }
  return { check, pending };
}

// One retry, and only for a failure of the network. A problem is an answer, so it has no retry.
function retryRead(count: number, error: unknown): boolean {
  const failure = error as Failure;
  return count < 1 && (failure.kind === "unreachable" || failure.kind === "timeout");
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      // The stream and the rules below mark a query as old. Time does not.
      queries: {
        staleTime: Number.POSITIVE_INFINITY,
        retry: retryRead,
        retryDelay: 1000,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
      },
      // A change is never sent again by the page.
      mutations: { retry: false },
    },
  });
}

// The rules for reading again. `again` holds the keys that got old while their read was
// in flight: each one is read exactly once more when that read ends, so a flood of `changed`
// events cannot cancel a read and cannot run two reads of one key.
export function createReads(client: QueryClient) {
  const cache = client.getQueryCache();
  const again = new Set<string>();

  const mounted = (q: Query) => q.getObserversCount() > 0 || q.queryKey[0] === keys.me[0];
  const fetching = (q: Query) => q.state.fetchStatus === "fetching";
  // `cancelRefetch: false` keeps a read in flight. A failure is already in the query state.
  const readNow = (q: Query) =>
    void client.refetchQueries({ queryKey: q.queryKey, exact: true }, { cancelRefetch: false });

  const stop = cache.subscribe((event) => {
    if (event.type !== "updated") return;
    const kind = event.action.type;
    if (kind !== "success" && kind !== "error") return;
    const q = event.query;
    if (!again.delete(q.queryHash)) return;
    // Wait one tick: the cache is still in the middle of its own update.
    queueMicrotask(() => {
      q.invalidate();
      if (mounted(q)) readNow(q);
    });
  });

  return {
    // Marks every query as old. Reads `me` and each query that a screen uses.
    refresh() {
      for (const q of cache.getAll()) {
        if (fetching(q)) {
          again.add(q.queryHash);
          continue;
        }
        q.invalidate();
        if (mounted(q)) readNow(q);
      }
    },
    // The health check in a good state: a read that failed runs again.
    refreshFailed() {
      for (const q of cache.getAll()) {
        if (q.state.status === "error" && !fetching(q) && mounted(q)) readNow(q);
      }
    },
    // For the end of a login session: no old data of a person stays for the next one.
    clear() {
      again.clear();
      client.clear();
    },
    dispose() {
      stop();
    },
  };
}
