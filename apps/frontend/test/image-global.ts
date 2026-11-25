import { randomBytes } from "node:crypto";
import { cleanup } from "./image-docker.ts";

// Runs in the main process of vitest, so the cleanup also runs when the run is interrupted
// and the worker dies without a chance to clean.
export default function setup(): () => void {
  const run = randomBytes(4).toString("hex");
  process.env.CALYX_IMAGE_TEST_RUN = run;
  const stop = () => {
    cleanup(run);
    process.exit(1);
  };
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
  return () => cleanup(run);
}
