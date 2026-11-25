import assert from "node:assert/strict";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, before, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { check, copies, imageVersionError, sync } from "./version.ts";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..");
let root = "";

function read(file: string): string {
  return readFileSync(join(root, file), "utf8");
}

function write(file: string, text: string): void {
  writeFileSync(join(root, file), text);
}

function setRoot(version: string): void {
  write("package.json", JSON.stringify({ name: "calyx-monorepo", version }, null, 2));
}

describe("version script", () => {
  before(() => {
    root = mkdtempSync(join(tmpdir(), "calyx-version-"));
  });
  after(() => rmSync(root, { recursive: true, force: true }));

  // Each test starts from a fresh copy of the real files, so the tests do not depend on each other.
  function fresh(): void {
    for (const file of copies.map((c) => c.file)) {
      mkdirSync(dirname(join(root, file)), { recursive: true });
      cpSync(join(repo, file), join(root, file));
    }
    setRoot("0.1.0");
  }

  test("sync writes the root version into every copy", () => {
    fresh();
    setRoot("2.4.6");
    sync(root);
    for (const { file } of copies) {
      assert.ok(read(file).includes("2.4.6"), `${file} has no 2.4.6`);
    }
    assert.deepEqual(check(root), []);
  });

  test("sync keeps the CRLF line endings of the VB6 file", () => {
    fresh();
    setRoot("2.4.6");
    sync(root);
    const bas = read("packages/client-sdk/vb6/src/CalyxCore.bas");
    assert.match(bas, /CALYX_SDK_VERSION As String = "2\.4\.6"\r\n/);
    assert.ok(!/[^\r]\n/.test(bas));
  });

  test("check passes when every copy has the root version", () => {
    fresh();
    assert.deepEqual(check(root), []);
  });

  test("check finds a changed copy and names it", () => {
    fresh();
    const file = "packages/client-sdk/go/calyx/version.go";
    write(file, read(file).replace("0.1.0", "0.9.9"));
    assert.deepEqual(check(root), [`The version of ${file} is 0.9.9. The root package.json says 0.1.0.`]);
  });

  test("check finds a changed second pattern in the same file", () => {
    fresh();
    const file = "packages/client-sdk/python/core/pyproject.toml";
    write(file, read(file).replace("calyx-client-sdk==0.1.0", "calyx-client-sdk==0.2.0"));
    assert.deepEqual(check(root), [`The version of ${file} is 0.2.0. The root package.json says 0.1.0.`]);
  });

  test("check fails when a pattern matches nothing", () => {
    fresh();
    const file = "packages/client-sdk/python/backend/src/calyx_client_sdk/_version.py";
    write(file, "\n");
    const errors = check(root);
    assert.equal(errors.length, 1);
    assert.ok(errors[0].includes(file));
  });

  test("check and sync fail when the root version is not a plain X.Y.Z", () => {
    fresh();
    for (const bad of ["1.0.0-alpha.1", "1.0", "v1.0.0", ""]) {
      setRoot(bad);
      assert.equal(check(root).length, 1, bad);
      assert.throws(() => sync(root), /plain X\.Y\.Z/, bad);
    }
  });

  test("imageVersionError passes when the image prints the root version", () => {
    assert.equal(imageVersionError("calyx-core", "0.1.0\n", "0.1.0"), null);
  });

  test("imageVersionError fails on dev", () => {
    assert.equal(
      imageVersionError("calyx-core", "dev\n", "0.1.0"),
      "The image calyx-core says dev. The root package.json says 0.1.0.",
    );
  });

  test("imageVersionError fails on a different version", () => {
    assert.match(imageVersionError("calyx-backend", "0.2.0\n", "0.1.0") ?? "", /says 0\.2\.0/);
  });
});
