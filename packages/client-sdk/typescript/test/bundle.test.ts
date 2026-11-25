import assert from "node:assert/strict";
import { test } from "node:test";

import { build } from "esbuild";

import { createCoreClient } from "../src/core/browser.js";
import { isCalyxError } from "../src/errors/index.js";

// Bundles the three browser-safe entries as a web build would, and checks what they load.
test("backend, webhooks and errors load no node: module and no gRPC code", async () => {
  const result = await build({
    entryPoints: ["src/backend/index.ts", "src/webhooks/index.ts", "src/errors/index.ts"],
    bundle: true,
    platform: "browser",
    format: "esm",
    metafile: true,
    external: ["node:*"],
    write: false,
    outdir: "out",
    logLevel: "silent",
  });

  const inputs = Object.keys(result.metafile.inputs);
  assert.ok(inputs.length > 0);
  const forbidden = inputs.filter(
    (input) => input.includes("node_modules/@grpc/") || input.includes("node_modules/@bufbuild/"),
  );
  assert.deepEqual(forbidden, []);

  const nodeImports = Object.values(result.metafile.outputs)
    .flatMap((output) => output.imports)
    .filter((imported) => imported.path.startsWith("node:"));
  assert.deepEqual(nodeImports, []);
});

// esbuild resolves the package name through the exports map, so this needs dist/ (pretest builds it).
test("the core entry takes the browser file and loads no gRPC code", async () => {
  const result = await build({
    stdin: {
      contents: 'import "@calyx/client-sdk/core";',
      resolveDir: process.cwd(),
      loader: "js",
    },
    bundle: true,
    platform: "browser",
    format: "esm",
    metafile: true,
    external: ["node:*"],
    write: false,
    outdir: "out",
    logLevel: "silent",
  });

  const inputs = Object.keys(result.metafile.inputs);
  assert.ok(inputs.includes("dist/core/browser.js"));
  assert.deepEqual(
    inputs.filter((input) => input.includes("node_modules/@grpc/")),
    [],
  );
});

test("createCoreClient in the browser file throws unsupported_runtime", () => {
  assert.throws(
    () => createCoreClient(),
    (err) =>
      isCalyxError(err, "unsupported_runtime") &&
      err.source === "client" &&
      err.detail === "The core client needs Node, Bun or Deno.",
  );
});
