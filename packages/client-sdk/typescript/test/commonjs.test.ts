import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const packageRoot = new URL("..", import.meta.url);

// A CommonJS program loads this ESM package with require(). That stops working when a module
// uses a top-level await. The package name resolves through the exports map, so this needs
// dist/ (pretest builds it).
test("a CommonJS program loads every entry with require()", () => {
  const program = [
    'const { createBackendClient } = require("@calyx/client-sdk/backend");',
    'const { createCoreClient } = require("@calyx/client-sdk/core");',
    'const { verifyWebhook } = require("@calyx/client-sdk/webhooks");',
    'const { isCalyxError } = require("@calyx/client-sdk/errors");',
    "console.log([createBackendClient, createCoreClient, verifyWebhook, isCalyxError]",
    '  .map((f) => typeof f).join(","));',
  ].join("\n");
  const output = execFileSync(process.execPath, ["--input-type=commonjs", "-e", program], {
    cwd: packageRoot,
    encoding: "utf8",
  });
  assert.equal(output.trim(), "function,function,function,function");
});

// require() of an ES module needs no flag from Node 22.12.
test("the package asks for Node 22.12 or later", () => {
  const pkg = JSON.parse(readFileSync(new URL("package.json", packageRoot), "utf8"));
  assert.equal(pkg.engines.node, ">=22.12");
});
