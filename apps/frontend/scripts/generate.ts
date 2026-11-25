// Generates src/api/generated/types.ts. With --check, it generates into a temporary folder and compares.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const spec = join(root, "../../packages/shared-types/openapi/backend.json");
const target = join(root, "src/api/generated/types.ts");

function generate(out: string): void {
  execFileSync(join(root, "node_modules/.bin/openapi-typescript"), [spec, "-o", out], {
    cwd: root,
    stdio: "inherit",
  });
}

if (process.argv.includes("--check")) {
  const tmp = mkdtempSync(join(tmpdir(), "calyx-frontend-generate-"));
  try {
    const fresh = join(tmp, "types.ts");
    generate(fresh);
    let same = false;
    try {
      same = readFileSync(fresh).equals(readFileSync(target));
    } catch {
      // A missing file counts as out of date.
    }
    if (!same) {
      console.error(
        'src/api/generated/types.ts is out of date. Run "pnpm --filter @calyx/frontend generate".',
      );
      process.exitCode = 1;
    }
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
} else {
  generate(target);
}
