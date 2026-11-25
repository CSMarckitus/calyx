// Fails the build when the JavaScript or the fonts are over the limits.
// The panel and the docs page have their own limit. The Vite manifest lists the files of each entry.
import { readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { checkSizes } from "../src/build/size-limits";

type Chunk = { file: string; imports?: string[]; dynamicImports?: string[] };
type Manifest = Record<string, Chunk>;

const dist = join(fileURLToPath(new URL("..", import.meta.url)), "dist");
const manifest = JSON.parse(readFileSync(join(dist, ".vite/manifest.json"), "utf8")) as Manifest;

// The manifest lists only the direct imports, so the walk follows them. Each file counts one time.
function entryGzip(entry: string, allowDynamic: boolean): number {
  const seen = new Set<string>();
  const walk = (key: string, viaDynamic: boolean) => {
    const chunk = manifest[key];
    if (!chunk) throw new Error(`The manifest has no chunk ${key}.`);
    if (viaDynamic && !allowDynamic) {
      console.error(`The panel has a dynamic import: ${chunk.file}.`);
      process.exit(1);
    }
    if (seen.has(key)) return;
    seen.add(key);
    for (const k of chunk.imports ?? []) walk(k, false);
    for (const k of chunk.dynamicImports ?? []) walk(k, true);
  };
  walk(entry, false);
  let sum = 0;
  for (const key of seen) {
    const file = manifest[key]?.file ?? "";
    if (file.endsWith(".js")) sum += gzipSync(readFileSync(join(dist, file))).length;
  }
  return sum;
}

const jsGzip = entryGzip("index.html", false);
const docsJsGzip = entryGzip("docs/index.html", true);

const assets = join(dist, "assets");
const fonts = readdirSync(assets)
  .filter((f) => f.endsWith(".woff2"))
  .reduce((sum, f) => sum + statSync(join(assets, f)).size, 0);

rmSync(join(dist, ".vite"), { recursive: true, force: true });

console.log(
  `JavaScript of the panel: ${jsGzip} bytes gzip. JavaScript of the docs page: ${docsJsGzip} bytes gzip. Fonts: ${fonts} bytes.`,
);
const problems = checkSizes({ jsGzip, docsJsGzip, fonts });
for (const p of problems) console.error(p);
if (problems.length > 0) process.exit(1);
