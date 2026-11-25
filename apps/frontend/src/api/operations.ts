import type { operations as Generated, paths } from "./generated/types.ts";

export type OperationId = keyof Generated;
export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

// The pairs (method, path) of `paths` that hold the operation K. A name, a method or a path that
// the API does not have makes the table below fail to compile.
type Pairs<K extends OperationId> = {
  [P in keyof paths]: {
    [M in HttpMethod]: Lowercase<M> extends keyof paths[P]
      ? paths[P][Lowercase<M>] extends Generated[K]
        ? Generated[K] extends paths[P][Lowercase<M>]
          ? { method: M; path: P }
          : never
        : never
      : never;
  }[HttpMethod];
}[keyof paths];

type Row<K extends OperationId> = Pairs<K> & { timeoutMs: number };
type StreamRow<K extends OperationId> = Pairs<K> & { events: readonly string[] };

const read = 20_000;
const change = 45_000;
// The backend waits 60 s for the core when it removes an instance.
const remove = 75_000;

function json<K extends OperationId>(_op: K, row: Pairs<K>, timeoutMs: number): Row<K> {
  return { ...row, timeoutMs };
}

function sse<K extends OperationId>(
  _op: K,
  row: Pairs<K>,
  events: readonly string[],
): StreamRow<K> {
  return { ...row, events };
}

type Table = {
  [K in OperationId]: Row<K> | StreamRow<K>;
};

export const operations = {
  listAPIKeys: json("listAPIKeys", { method: "GET", path: "/api/v1/api-keys" }, read),
  createAPIKey: json("createAPIKey", { method: "POST", path: "/api/v1/api-keys" }, change),
  revokeAPIKey: json("revokeAPIKey", { method: "DELETE", path: "/api/v1/api-keys/{id}" }, change),
  login: json("login", { method: "POST", path: "/api/v1/auth/login" }, change),
  logout: json("logout", { method: "POST", path: "/api/v1/auth/logout" }, change),
  requestPasswordReset: json(
    "requestPasswordReset",
    { method: "POST", path: "/api/v1/auth/password-reset" },
    change,
  ),
  confirmPasswordReset: json(
    "confirmPasswordReset",
    { method: "POST", path: "/api/v1/auth/password-reset/confirm" },
    change,
  ),
  lookupPasswordReset: json(
    "lookupPasswordReset",
    { method: "POST", path: "/api/v1/auth/password-reset/lookup" },
    change,
  ),
  getBulkJob: json("getBulkJob", { method: "GET", path: "/api/v1/bulk-jobs/{id}" }, read),
  cancelBulkJob: json(
    "cancelBulkJob",
    { method: "POST", path: "/api/v1/bulk-jobs/{id}/cancel" },
    change,
  ),
  listClients: json("listClients", { method: "GET", path: "/api/v1/clients" }, read),
  createClient: json("createClient", { method: "POST", path: "/api/v1/clients" }, change),
  getClient: json("getClient", { method: "GET", path: "/api/v1/clients/{id}" }, read),
  updateClient: json("updateClient", { method: "PATCH", path: "/api/v1/clients/{id}" }, change),
  deleteClient: json("deleteClient", { method: "DELETE", path: "/api/v1/clients/{id}" }, change),
  inviteClientUser: json(
    "inviteClientUser",
    { method: "POST", path: "/api/v1/clients/{id}/invitations" },
    change,
  ),
  cancelInvitation: json(
    "cancelInvitation",
    { method: "DELETE", path: "/api/v1/clients/{id}/invitations" },
    change,
  ),
  removeClientAccess: json(
    "removeClientAccess",
    { method: "DELETE", path: "/api/v1/clients/{id}/user" },
    change,
  ),
  followEvents: sse("followEvents", { method: "GET", path: "/api/v1/events" }, [
    "changed",
    "replaced",
  ]),
  listInstances: json("listInstances", { method: "GET", path: "/api/v1/instances" }, read),
  createInstance: json("createInstance", { method: "POST", path: "/api/v1/instances" }, change),
  getInstance: json("getInstance", { method: "GET", path: "/api/v1/instances/{id}" }, read),
  removeInstance: json(
    "removeInstance",
    { method: "DELETE", path: "/api/v1/instances/{id}" },
    remove,
  ),
  updateInstance: json(
    "updateInstance",
    { method: "PATCH", path: "/api/v1/instances/{id}" },
    change,
  ),
  listBulkJobs: json(
    "listBulkJobs",
    { method: "GET", path: "/api/v1/instances/{id}/bulk-jobs" },
    read,
  ),
  createBulkJob: json(
    "createBulkJob",
    { method: "POST", path: "/api/v1/instances/{id}/bulk-jobs" },
    change,
  ),
  sendMessage: json(
    "sendMessage",
    { method: "POST", path: "/api/v1/instances/{id}/messages" },
    change,
  ),
  pairInstance: json(
    "pairInstance",
    { method: "POST", path: "/api/v1/instances/{id}/pair" },
    change,
  ),
  followPairing: sse("followPairing", { method: "GET", path: "/api/v1/instances/{id}/pairing" }, [
    "update",
    "done",
    "failed",
  ]),
  cancelPairing: json(
    "cancelPairing",
    { method: "DELETE", path: "/api/v1/instances/{id}/pairing" },
    change,
  ),
  acceptInvitation: json(
    "acceptInvitation",
    { method: "POST", path: "/api/v1/invitations/accept" },
    change,
  ),
  lookupInvitation: json(
    "lookupInvitation",
    { method: "POST", path: "/api/v1/invitations/lookup" },
    change,
  ),
  getMe: json("getMe", { method: "GET", path: "/api/v1/me" }, read),
  changePassword: json("changePassword", { method: "PUT", path: "/api/v1/me/password" }, change),
  getOrganization: json("getOrganization", { method: "GET", path: "/api/v1/organization" }, read),
  updateOrganization: json(
    "updateOrganization",
    { method: "PATCH", path: "/api/v1/organization" },
    change,
  ),
  acknowledgeWhatsAppWeb: json(
    "acknowledgeWhatsAppWeb",
    { method: "POST", path: "/api/v1/organization/whatsapp-web-acknowledgement" },
    change,
  ),
} satisfies Table;

export type StreamOperationId = {
  [K in keyof typeof operations]: (typeof operations)[K] extends { events: readonly string[] }
    ? K
    : never;
}[keyof typeof operations];
export type JsonOperationId = Exclude<OperationId, StreamOperationId>;

type Body<K extends OperationId> = Generated[K] extends {
  requestBody: { content: { "application/json": infer B } };
}
  ? B
  : never;
type PathParams<K extends OperationId> = Generated[K]["parameters"]["path"] extends infer P
  ? [P] extends [never | undefined]
    ? never
    : P
  : never;

// What a caller gives: `path` and `body` only when the operation has them.
export type Params<K extends OperationId> = ([PathParams<K>] extends [never]
  ? { path?: undefined }
  : { path: PathParams<K> }) &
  ([Body<K>] extends [never] ? { body?: undefined } : { body: Body<K> });

type Success<K extends OperationId> = Generated[K]["responses"] extends infer R
  ? {
      [C in keyof R & (200 | 201 | 202)]: R[C] extends {
        content: { "application/json": infer B };
      }
        ? B
        : undefined;
    }[keyof R & (200 | 201 | 202)]
  : never;

// The body of a good answer. An answer with no body (204, or 202 of removeInstance) gives undefined.
export type Answer<K extends OperationId> = Success<K> | undefined;

export type StreamItem<K extends StreamOperationId> = Generated[K]["responses"][200] extends {
  content: { "text/event-stream": (infer I)[] };
}
  ? I
  : never;
