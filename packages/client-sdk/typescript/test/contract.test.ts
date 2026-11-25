import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import {
  bulkJobTimeoutMs,
  defaultTimeoutMs,
  excludedOperations,
  operations,
  sendMessageTimeoutMs,
} from "../src/backend/operations.js";

type Schema = { $ref?: string; properties?: Record<string, unknown> };
type Operation = {
  operationId?: string;
  requestBody?: { content?: Record<string, { schema?: Schema }> };
};
type Doc = {
  paths: Record<string, Record<string, Operation>>;
  components: { schemas: Record<string, Schema> };
};

const doc = JSON.parse(
  readFileSync(new URL("../../../shared-types/openapi/backend.json", import.meta.url), "utf8"),
) as Doc;

const methods = ["get", "put", "post", "delete", "patch"];

function documentOperations() {
  const found = new Map<string, { method: string; path: string; op: Operation }>();
  for (const [path, item] of Object.entries(doc.paths)) {
    for (const [method, op] of Object.entries(item)) {
      if (methods.includes(method) && op.operationId) {
        found.set(op.operationId, { method: method.toUpperCase(), path, op });
      }
    }
  }
  return found;
}

test("every operation of backend.json is in the table or in the excluded list", () => {
  for (const id of documentOperations().keys()) {
    const listed =
      Object.hasOwn(operations, id) || (excludedOperations as readonly string[]).includes(id);
    assert.ok(listed, `${id} is in neither list`);
  }
});

test("the lists have the sizes of the contract and no overlap", () => {
  assert.equal(Object.keys(operations).length, 23);
  assert.equal(excludedOperations.length, 14);
  for (const id of excludedOperations) assert.ok(!Object.hasOwn(operations, id), id);
  assert.ok(excludedOperations.includes("createAPIKey"));
  assert.ok(excludedOperations.includes("inviteClientUser"));
});

test("every method and path of the table matches the document", () => {
  const found = documentOperations();
  for (const [id, row] of Object.entries(operations)) {
    const doc = found.get(id);
    assert.ok(doc, `${id} is not in backend.json`);
    assert.equal(row.method, doc.method, `${id} method`);
    assert.equal(row.path, doc.path, `${id} path`);
  }
});

test("only followPairing is a stream, and the time limits follow the contract", () => {
  for (const [id, row] of Object.entries(operations)) {
    assert.equal(row.kind, id === "followPairing" ? "stream" : "json", id);
    if (id === "followPairing") assert.equal(row.timeoutMs, undefined, id);
    else if (id === "sendMessage") assert.equal(row.timeoutMs, sendMessageTimeoutMs, id);
    else if (id === "createBulkJob") assert.equal(row.timeoutMs, bulkJobTimeoutMs, id);
    else assert.equal(row.timeoutMs, defaultTimeoutMs, id);
  }
  assert.equal(sendMessageTimeoutMs, 290_000);
  assert.equal(defaultTimeoutMs, 120_000);
  assert.equal(bulkJobTimeoutMs, 150_000);
});

test("no operation has a path parameter and a body field with the same name", () => {
  const found = documentOperations();
  for (const id of Object.keys(operations)) {
    const { path, op } = found.get(id)!;
    const pathParams = [...path.matchAll(/\{(\w+)\}/g)].map((m) => m[1]!);
    let schema = op.requestBody?.content?.["application/json"]?.schema;
    if (schema?.$ref) schema = doc.components.schemas[schema.$ref.split("/").pop()!];
    const fields = Object.keys(schema?.properties ?? {});
    for (const name of pathParams) assert.ok(!fields.includes(name), `${id}: ${name}`);
  }
});

test("every retry value in the shared test data is one of the five new names", () => {
  const names = ["retry_now", "retry_later", "check_if_sent", "do_not_retry", "unknown"];
  const read = (file: string) =>
    JSON.parse(readFileSync(new URL(`../../testdata/${file}`, import.meta.url), "utf8")) as {
      rows?: { code: string; retry: string }[];
      cases?: { name: string; expected: { retry?: string } }[];
    };
  const advice = read("retry-advice.json");
  assert.ok((advice.rows ?? []).length > 0);
  for (const row of advice.rows ?? []) {
    assert.ok(names.includes(row.retry), `retry-advice.json ${row.code}: ${row.retry}`);
  }
  for (const file of ["backend-errors.json", "core-errors.json"]) {
    const cases = read(file).cases ?? [];
    assert.ok(cases.length > 0);
    for (const c of cases) {
      const retry = c.expected.retry;
      if (retry !== undefined) {
        assert.ok(names.includes(retry), `${file} ${c.name}: ${retry}`);
      }
    }
  }
});
