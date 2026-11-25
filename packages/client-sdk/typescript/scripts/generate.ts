// Generates src/generated. With --check, it generates into a temporary folder and compares.
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const bin = (name: string) => join(root, "node_modules/.bin", name);
const protoDir = "../../shared-types/proto";
const corePrefix = ["instance", "message", "event"].map(
  (n) => `${protoDir}/calyx/core/v1/${n}.proto`,
);
const baseOpts =
  "enumsAsLiterals=true,esModuleInterop=true,importSuffix=.js,annotateFilesWithVersion=false";

function generate(out: string): void {
  const run = (cmd: string, args: string[]) =>
    execFileSync(cmd, args, { cwd: root, stdio: "inherit" });
  // protoc does not create its output folders.
  mkdirSync(join(out, "core-types"), { recursive: true });
  mkdirSync(join(out, "core-grpc"), { recursive: true });
  const plugin = `--plugin=${bin("protoc-gen-ts_proto")}`;

  run(bin("openapi-typescript"), [
    "../../shared-types/openapi/backend.json",
    "-o",
    join(out, "backend.ts"),
  ]);
  run("protoc", [
    plugin,
    `--ts_proto_out=${join(out, "core-types")}`,
    `--ts_proto_opt=onlyTypes=true,outputServices=false,${baseOpts}`,
    `--proto_path=${protoDir}`,
    ...corePrefix,
  ]);
  run("protoc", [
    plugin,
    `--ts_proto_out=${join(out, "core-grpc")}`,
    `--ts_proto_opt=outputServices=grpc-js,env=node,${baseOpts}`,
    `--proto_path=${protoDir}`,
    "--proto_path=proto",
    ...corePrefix,
    "proto/google/rpc/status.proto",
    "proto/google/rpc/error_details.proto",
  ]);
}

function listFiles(dir: string): string[] {
  const files: string[] = [];
  const walk = (d: string) => {
    for (const name of readdirSync(d)) {
      const path = join(d, name);
      if (statSync(path).isDirectory()) walk(path);
      else files.push(relative(dir, path));
    }
  };
  try {
    walk(dir);
  } catch {
    // A missing folder has no files.
  }
  return files.sort();
}

const target = join(root, "src/generated");
if (process.argv.includes("--check")) {
  const tmp = mkdtempSync(join(tmpdir(), "calyx-sdk-generate-"));
  try {
    generate(tmp);
    const fresh = listFiles(tmp);
    const committed = listFiles(target);
    const problems: string[] = [];
    for (const f of new Set([...fresh, ...committed])) {
      if (!committed.includes(f)) problems.push(`missing: ${f}`);
      else if (!fresh.includes(f)) problems.push(`extra: ${f}`);
      else if (!readFileSync(join(tmp, f)).equals(readFileSync(join(target, f))))
        problems.push(`differs: ${f}`);
    }
    if (problems.length > 0) {
      console.error(
        `src/generated is out of date. Run "pnpm --filter @calyx/client-sdk generate".\n${problems.join("\n")}`,
      );
      process.exitCode = 1;
    }
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
} else {
  rmSync(target, { recursive: true, force: true });
  generate(target);
}
