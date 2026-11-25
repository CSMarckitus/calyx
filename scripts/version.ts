// Sync and check of the product version. The root package.json is the one source.
// Node runs this file directly (22.18 or later), so it uses only syntax that Node can strip.
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Each pattern has three groups: the text before the version, the version and the text after it.
// A pattern that matches nothing is an error, so a renamed constant cannot pass silently.
type Copy = { file: string; patterns: RegExp[] };

const jsonVersion = /^(\s*"version": ")([^"]*)(")/m;
const tomlVersion = /^(version = ")([^"]*)(")/m;
const pyVersion = /^(__version__ = ")([^"]*)(")/m;

export const copies: Copy[] = [
  { file: "README.md", patterns: [/(img\.shields\.io\/badge\/version-)(\d+\.\d+\.\d+)(-)/] },
  { file: "apps/frontend/package.json", patterns: [jsonVersion] },
  { file: "packages/shared-types/package.json", patterns: [jsonVersion] },
  { file: "packages/client-sdk/typescript/package.json", patterns: [jsonVersion] },
  {
    file: "packages/client-sdk/typescript/src/version.ts",
    patterns: [/^(export const version = ")([^"]*)(")/m],
  },
  { file: "packages/client-sdk/go/calyx/version.go", patterns: [/^(const Version = ")([^"]*)(")/m] },
  { file: "packages/client-sdk/php/client/composer.json", patterns: [jsonVersion] },
  {
    file: "packages/client-sdk/php/client/src/Version.php",
    patterns: [/(public const string SDK = ')([^']*)(')/],
  },
  {
    file: "packages/client-sdk/php/core/composer.json",
    patterns: [jsonVersion, /("calyx\/client-sdk": ")([^"]*)(")/],
  },
  {
    file: "packages/client-sdk/php/core/composer.lock",
    patterns: [/("name": "calyx\/client-sdk",\s*"version": ")([^"]*)(")/],
  },
  {
    file: "packages/client-sdk/php/docker/run.sh",
    patterns: [
      /(calyx-client-sdk(?:-core)?-)(\d+\.\d+\.\d+)(\.zip)/g,
      /(composer require calyx\/client-sdk-core:)(\d+\.\d+\.\d+)(\s)/,
    ],
  },
  { file: "packages/client-sdk/python/backend/pyproject.toml", patterns: [tomlVersion] },
  {
    file: "packages/client-sdk/python/backend/src/calyx_client_sdk/_version.py",
    patterns: [pyVersion],
  },
  {
    file: "packages/client-sdk/python/core/pyproject.toml",
    patterns: [tomlVersion, /("calyx-client-sdk==)([^"]*)(")/],
  },
  {
    file: "packages/client-sdk/python/core/src/calyx_client_sdk_core/_version.py",
    patterns: [pyVersion],
  },
  {
    file: "packages/client-sdk/vb6/src/CalyxCore.bas",
    patterns: [/^(Public Const CALYX_SDK_VERSION As String = ")([^"]*)(")/m],
  },
];

const plain = /^\d+\.\d+\.\d+$/;

export function rootVersion(root: string): string {
  const version = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version;
  if (typeof version !== "string" || !plain.test(version)) {
    throw new Error(`The version of the root package.json is ${JSON.stringify(version)}. It must be a plain X.Y.Z.`);
  }
  return version;
}

function globalOf(pattern: RegExp): RegExp {
  return pattern.global ? pattern : new RegExp(pattern.source, `${pattern.flags}g`);
}

export function sync(root: string): void {
  const version = rootVersion(root);
  for (const { file, patterns } of copies) {
    let text = readFileSync(join(root, file), "utf8");
    for (const pattern of patterns) {
      const re = globalOf(pattern);
      if (!re.test(text)) {
        throw new Error(`The file ${file} has no match for ${pattern}.`);
      }
      re.lastIndex = 0;
      text = text.replace(re, (_all, before: string, _old: string, after: string) => before + version + after);
    }
    writeFileSync(join(root, file), text);
  }
}

// Returns one message for each problem. An empty list means that every copy is right.
export function check(root: string): string[] {
  let version: string;
  try {
    version = rootVersion(root);
  } catch (error) {
    return [(error as Error).message];
  }
  const errors: string[] = [];
  for (const { file, patterns } of copies) {
    const text = readFileSync(join(root, file), "utf8");
    for (const pattern of patterns) {
      const found = [...text.matchAll(globalOf(pattern))];
      if (found.length === 0) {
        errors.push(`The file ${file} has no match for ${pattern}.`);
      }
      for (const match of found) {
        if (match[2] !== version) {
          errors.push(`The version of ${file} is ${match[2]}. The root package.json says ${version}.`);
        }
      }
    }
  }
  return errors;
}

// Returns a message when the image does not print the root version, or null when it does.
export function imageVersionError(image: string, output: string, expected: string): string | null {
  const actual = output.trim();
  return actual === expected ? null : `The image ${image} says ${actual}. The root package.json says ${expected}.`;
}

// Builds the two images, runs --version in each, and always removes the images. Needs Docker.
export function imageCheck(root: string): string[] {
  const expected = rootVersion(root);
  const images = [
    { tag: "calyx-backend:version-check", dockerfile: "apps/backend/Dockerfile", command: [] as string[] },
    { tag: "calyx-core:version-check", dockerfile: "apps/core/Dockerfile", command: ["core"] },
  ];
  const errors: string[] = [];
  try {
    for (const { tag, dockerfile, command } of images) {
      execFileSync("docker", ["build", "-f", dockerfile, "-t", tag, "."], { cwd: root, stdio: "inherit" });
      const output = execFileSync("docker", ["run", "--rm", tag, ...command, "--version"], { encoding: "utf8" });
      const error = imageVersionError(tag, output, expected);
      if (error !== null) {
        errors.push(error);
      }
    }
  } finally {
    for (const { tag } of images) {
      try {
        execFileSync("docker", ["rmi", "-f", tag], { stdio: "ignore" });
      } catch {
        // The image does not exist when its build failed.
      }
    }
  }
  return errors;
}

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const command = process.argv[2];
  try {
    if (command === "sync") {
      sync(repoRoot);
      console.log(`Wrote version ${rootVersion(repoRoot)} into ${copies.length} files.`);
    } else if (command === "check") {
      const errors = check(repoRoot);
      for (const message of errors) {
        console.error(message);
      }
      if (errors.length > 0) {
        process.exit(1);
      }
      console.log(`Version ${rootVersion(repoRoot)} is in all ${copies.length} files.`);
    } else if (command === "image-check") {
      const errors = imageCheck(repoRoot);
      for (const message of errors) {
        console.error(message);
      }
      if (errors.length > 0) {
        process.exit(1);
      }
      console.log(`Both images say ${rootVersion(repoRoot)}.`);
    } else {
      console.error("Usage: node scripts/version.ts sync|check|image-check");
      process.exit(2);
    }
  } catch (error) {
    console.error((error as Error).message);
    process.exit(1);
  }
}
