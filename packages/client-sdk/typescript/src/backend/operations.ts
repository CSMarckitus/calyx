import type { operations as GeneratedOperations } from "../generated/backend.js";

export type OperationKind = "json" | "stream";
export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface OperationRow {
  method: HttpMethod;
  path: string;
  kind: OperationKind;
  // Default time limit in milliseconds. A stream has none.
  timeoutMs?: number;
}

// Shorter than the 300-second header timeout of undici, so the SDK ends the call first.
export const sendMessageTimeoutMs = 290_000;
export const defaultTimeoutMs = 120_000;
// A bulk job plans every message of the job before it answers.
export const bulkJobTimeoutMs = 150_000;

// Browser flows, one account action and four operations that an API key cannot call. See part 2 of CONTRACT.md.
export const excludedOperations = [
  "login",
  "logout",
  "requestPasswordReset",
  "confirmPasswordReset",
  "acceptInvitation",
  "changePassword",
  "acknowledgeWhatsAppWeb",
  "createAPIKey",
  "inviteClientUser",
  "lookupInvitation",
  "lookupPasswordReset",
  "cancelInvitation",
  "removeClientAccess",
  "followEvents",
] as const;

type Excluded = (typeof excludedOperations)[number];
export type IncludedOperationId = Exclude<keyof GeneratedOperations, Excluded>;

const json = (method: HttpMethod, path: string, timeoutMs = defaultTimeoutMs): OperationRow => ({
  method,
  path,
  kind: "json",
  timeoutMs,
});

// The type makes tsc fail when the generated operations and this table differ.
export const operations: Record<IncludedOperationId, OperationRow> = {
  listAPIKeys: json("GET", "/api/v1/api-keys"),
  revokeAPIKey: json("DELETE", "/api/v1/api-keys/{id}"),
  listClients: json("GET", "/api/v1/clients"),
  createClient: json("POST", "/api/v1/clients"),
  getClient: json("GET", "/api/v1/clients/{id}"),
  updateClient: json("PATCH", "/api/v1/clients/{id}"),
  deleteClient: json("DELETE", "/api/v1/clients/{id}"),
  listInstances: json("GET", "/api/v1/instances"),
  createInstance: json("POST", "/api/v1/instances"),
  getInstance: json("GET", "/api/v1/instances/{id}"),
  updateInstance: json("PATCH", "/api/v1/instances/{id}"),
  removeInstance: json("DELETE", "/api/v1/instances/{id}"),
  pairInstance: json("POST", "/api/v1/instances/{id}/pair"),
  cancelPairing: json("DELETE", "/api/v1/instances/{id}/pairing"),
  followPairing: { method: "GET", path: "/api/v1/instances/{id}/pairing", kind: "stream" },
  sendMessage: json("POST", "/api/v1/instances/{id}/messages", sendMessageTimeoutMs),
  createBulkJob: json("POST", "/api/v1/instances/{id}/bulk-jobs", bulkJobTimeoutMs),
  listBulkJobs: json("GET", "/api/v1/instances/{id}/bulk-jobs"),
  getBulkJob: json("GET", "/api/v1/bulk-jobs/{id}"),
  cancelBulkJob: json("POST", "/api/v1/bulk-jobs/{id}/cancel"),
  getMe: json("GET", "/api/v1/me"),
  getOrganization: json("GET", "/api/v1/organization"),
  updateOrganization: json("PATCH", "/api/v1/organization"),
};
