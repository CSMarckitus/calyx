// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { filterForRole, type OpenApiDocument } from "./api-docs";

const owner = [
  "cancelBulkJob",
  "cancelPairing",
  "createBulkJob",
  "createClient",
  "createInstance",
  "deleteClient",
  "followPairing",
  "getBulkJob",
  "getClient",
  "getInstance",
  "getMe",
  "getOrganization",
  "listAPIKeys",
  "listBulkJobs",
  "listClients",
  "listInstances",
  "pairInstance",
  "removeInstance",
  "revokeAPIKey",
  "sendMessage",
  "updateClient",
  "updateInstance",
  "updateOrganization",
];
const ownerOnly = [
  "listClients",
  "createClient",
  "getClient",
  "updateClient",
  "deleteClient",
  "getOrganization",
  "updateOrganization",
];
const client = owner.filter((id) => !ownerOnly.includes(id));

function load(): OpenApiDocument {
  const url = new URL("../../../../packages/shared-types/openapi/backend.json", import.meta.url);
  return JSON.parse(readFileSync(fileURLToPath(url), "utf8"));
}

function operations(doc: OpenApiDocument) {
  return Object.values(doc.paths).flatMap((item) =>
    Object.values(item).filter(
      (op) => typeof op === "object" && op !== null && "operationId" in op,
    ),
  );
}
const ids = (doc: OpenApiDocument) =>
  operations(doc)
    .map((op) => op.operationId)
    .sort();

describe("filterForRole", () => {
  it("gives the owner the 23 program operations", () => {
    expect(ids(filterForRole(load(), "owner"))).toEqual([...owner].sort());
  });

  it("gives a client user the 16 operations that allow the client role", () => {
    expect(client).toHaveLength(16);
    expect(ids(filterForRole(load(), "client"))).toEqual([...client].sort());
  });

  it("removes the session scheme and the session entries", () => {
    for (const role of ["owner", "client"] as const) {
      const out = filterForRole(load(), role);
      expect(Object.keys(out.components?.securitySchemes ?? {})).toEqual(["apiKey"]);
      for (const op of operations(out)) {
        expect(op.security?.some((s) => "session" in s)).toBe(false);
        expect(op.security?.some((s) => "apiKey" in s)).toBe(true);
      }
    }
  });

  it("keeps no public operation and no operation of the login cookie only", () => {
    const all = operations(load());
    const excluded = all
      .filter((op) => !op.security?.some((s) => "apiKey" in s))
      .map((op) => op.operationId);
    expect(excluded.length).toBeGreaterThan(0);
    const kept = ids(filterForRole(load(), "owner"));
    for (const id of excluded) expect(kept).not.toContain(id);
  });

  it("removes the paths and the tags with no operation left", () => {
    const doc = load();
    const out = filterForRole(doc, "client");
    for (const item of Object.values(out.paths))
      expect(operations({ ...out, paths: { x: item } })).not.toHaveLength(0);
    const used = new Set(operations(out).flatMap((op) => op.tags ?? []));
    expect(out.tags?.map((t) => t.name).sort()).toEqual([...used].sort());
    expect(out.tags?.length).toBeLessThan(doc.tags?.length ?? 0);
  });

  it("does not change the input", () => {
    const doc = load();
    const before = JSON.stringify(doc);
    filterForRole(doc, "client");
    expect(JSON.stringify(doc)).toBe(before);
  });
});
