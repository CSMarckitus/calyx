// The browser tests run one test program (a lane) for each Playwright worker. A lane has its own
// backend, fake core, fake SMTP server, database, control endpoint and vite preview. So "core
// stop", the faults and the backend restart of one test do not reach the tests of other workers.

function envInt(name: string, def: number, min: number, max: number): number {
  const raw = process.env[name];
  if (raw === undefined || raw === "") return def;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < min || n > max) {
    throw new Error(`${name}=${raw} is not an integer from ${min} to ${max}.`);
  }
  return n;
}

// The number of workers, and so of lanes. 1 runs all spec files one after the other.
export const workers = envInt("CALYX_UI_WORKERS", 6, 1, 10);

// The base moves all the ports, for example to run two suites on one machine. The previews move
// by the same offset as the base.
const base = envInt("CALYX_UI_PORT_BASE", 18080, 15000, 60000);
const shift = base - 18080;

export type Lane = { backendPort: number; controlPort: number; previewPort: number };

// Lane i: backend base+i, control base+20+i, preview 4180+i (with the default base).
export function lane(i: number): Lane {
  return { backendPort: base + i, controlPort: base + 20 + i, previewPort: 4180 + shift + i };
}

// The CSP page has its own build and preview, and no lane.
export const cspPort = 4174 + shift;

export const lanes: Lane[] = Array.from({ length: workers }, (_, i) => lane(i));

// The lane of this worker. Playwright gives each running worker a different TEST_PARALLEL_INDEX
// from 0 to workers-1, and a new worker after a failure takes the index of the old one.
export const myLane = lane(Number(process.env.TEST_PARALLEL_INDEX ?? 0));
