import { execFileSync } from "node:child_process";

// Shared by the image test and its global setup. The id of the run comes from the environment.
export function runId(): string {
  const id = process.env.CALYX_IMAGE_TEST_RUN;
  if (!id) throw new Error("CALYX_IMAGE_TEST_RUN is not set: run the test with test:image");
  return id;
}

export const labelOf = (run: string) => `calyx-image-test=${run}`;
export const imageOf = (run: string) => `calyx-frontend-imgtest:${run}`;

export function docker(...args: string[]): string {
  return execFileSync("docker", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

// Removes every container, network and image tag of one run. It never throws.
export function cleanup(run: string): void {
  const attempt = (...args: string[]) => {
    try {
      return docker(...args);
    } catch {
      return "";
    }
  };
  const label = labelOf(run);
  const containers = attempt("ps", "-aq", "--filter", `label=${label}`).split("\n").filter(Boolean);
  if (containers.length > 0) attempt("rm", "-f", ...containers);
  const networks = attempt("network", "ls", "-q", "--filter", `label=${label}`)
    .split("\n")
    .filter(Boolean);
  if (networks.length > 0) attempt("network", "rm", ...networks);
  attempt("image", "rm", imageOf(run));
}
