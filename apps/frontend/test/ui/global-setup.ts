import { type ChildProcess, spawn } from "node:child_process";
import { createConnection } from "node:net";
import { fileURLToPath } from "node:url";
import type { FullConfig } from "@playwright/test";
import { type Lane, lanes } from "./lanes.ts";

const controlUrl = (l: Lane) => `http://127.0.0.1:${l.controlPort}`;

function taken(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const s = createConnection({ port, host: "127.0.0.1" });
    s.once("connect", () => {
      s.destroy();
      resolve(true);
    });
    s.once("error", () => resolve(false));
  });
}

async function ready(l: Lane): Promise<boolean> {
  try {
    return (await fetch(`${controlUrl(l)}/ready`)).status === 200;
  } catch {
    return false;
  }
}

async function quit(l: Lane): Promise<void> {
  try {
    // The timeout keeps the 30 s deadline of stop() hard: a hung answer cannot hold the loop.
    await fetch(`${controlUrl(l)}/quit`, { method: "POST", signal: AbortSignal.timeout(2000) });
  } catch {
    // The program may be gone already.
  }
}

type Program = {
  lane: Lane;
  kill: () => void;
  output: () => string;
  exited: () => boolean;
  stop: () => Promise<void>;
};

// Starts the test program of one lane. Each program makes its own database and drops it when it ends.
function start(l: Lane): Program {
  const backendDir = fileURLToPath(new URL("../../../backend", import.meta.url));
  let output = "";
  let exited = false;
  const program: ChildProcess = spawn(
    "go",
    [
      "test",
      "-tags",
      "e2e uiserver",
      "-run",
      "^TestUIServer$",
      "-count=1",
      "-timeout",
      "0",
      "./internal/e2e/",
    ],
    {
      cwd: backendDir,
      env: {
        ...process.env,
        CALYX_UI_BACKEND_PORT: String(l.backendPort),
        CALYX_UI_CONTROL_PORT: String(l.controlPort),
        CALYX_UI_PREVIEW_PORT: String(l.previewPort),
      },
      stdio: ["ignore", "pipe", "pipe"],
      // Its own process group: `go test` does not pass a signal on to the test binary, so stop()
      // signals the whole group (go, the test binary and the backend).
      detached: true,
    },
  );
  program.stdout?.on("data", (d) => (output += d));
  program.stderr?.on("data", (d) => (output += d));
  program.once("error", (e) => {
    output += `${e}\n`;
    exited = true;
  });
  const done = new Promise<void>((resolve) =>
    program.once("exit", () => {
      exited = true;
      resolve();
    }),
  );
  const waitExit = (ms: number) => Promise.race([done, new Promise((r) => setTimeout(r, ms))]);
  const signal = (s: NodeJS.Signals) => {
    try {
      if (program.pid !== undefined) process.kill(-program.pid, s);
    } catch {
      // The group is gone already.
    }
  };
  // /quit first: the program then stops the backend and drops its database. A program that still
  // compiles has no control endpoint yet, so stop() sends /quit again until the program ends.
  const stop = async () => {
    const deadline = Date.now() + 30_000;
    while (!exited && Date.now() < deadline) {
      await quit(l);
      await waitExit(500);
    }
    if (exited) return;
    // The test binary ends at once on SIGINT, and the backend stops cleanly. The database stays.
    signal("SIGINT");
    await waitExit(10_000);
    signal("SIGKILL");
  };
  return {
    lane: l,
    output: () => output,
    exited: () => exited,
    stop,
    kill: () => signal("SIGINT"),
  };
}

async function waitReady(p: Program): Promise<void> {
  const deadline = Date.now() + 300_000; // The first run compiles the backend once for each lane.
  while (!(await ready(p.lane))) {
    if (p.exited() || Date.now() > deadline) {
      throw new Error(
        `The test program of control port ${p.lane.controlPort} did not become ready.\n${p.output().slice(-4000)}`,
      );
    }
    await new Promise((r) => setTimeout(r, 500));
  }
}

// Starts the test program of each lane, and returns the teardown.
export default async function globalSetup(config: FullConfig): Promise<() => Promise<void>> {
  if (config.workers > lanes.length) {
    throw new Error(
      `${config.workers} workers, but only ${lanes.length} lanes. Set CALYX_UI_WORKERS, not --workers.`,
    );
  }
  for (const l of lanes) {
    for (const port of [l.backendPort, l.controlPort]) {
      if (await taken(port)) {
        throw new Error(
          `Port ${port} is already in use. Stop the old test program (POST ${controlUrl(l)}/quit) or the process that holds it.`,
        );
      }
    }
  }
  if (!process.env.TEST_DATABASE_URL) {
    throw new Error(
      "TEST_DATABASE_URL is not set. Start the test database with `docker compose -f docker-compose.dev.yml --profile test up -d postgres-test`.",
    );
  }

  const programs = lanes.map(start);
  // A forced exit or a thrown error skips the teardown. The exit handler is synchronous, so it
  // only signals the groups; the test programs then drop their databases.
  process.once("exit", () => {
    for (const p of programs) if (!p.exited()) p.kill();
  });
  const teardown = async () => {
    await Promise.all(programs.map((p) => p.stop()));
  };
  try {
    await Promise.all(programs.map(waitReady));
  } catch (e) {
    await teardown();
    throw e;
  }
  return teardown;
}
