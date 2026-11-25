import type { components } from "../api/generated/types.ts";
import { formatClock } from "./time.ts";

type BulkJob = components["schemas"]["BulkJob"];

export type JobWords = {
  label: string;
  tone: "ok" | "wait" | "bad" | "idle";
  counts: string;
  tried: number;
  waiting?: number;
  stopped?: number;
  finishAbout?: string;
  canStop: boolean;
};

// The API has no counter for waiting or stopped messages, so both come from the total.
export function describeJob(job: BulkJob, now: number, zone?: string): JobWords {
  const sent = job.sent_messages;
  const failed = job.failed_messages;
  const tried = sent + failed;
  const rest = Math.max(0, job.total_messages - tried);
  const done = `${sent} sent, ${failed} not sent (failed or unknown)`;
  switch (job.status) {
    case "QUEUED":
      return {
        label: "Waiting to start",
        tone: "wait",
        counts: `${tried} of ${job.total_messages} tried`,
        tried,
        canStop: true,
      };
    case "RUNNING":
      return {
        label: "Sending",
        tone: "wait",
        counts: `${done}, ${rest} waiting`,
        tried,
        waiting: rest,
        ...(job.estimated_finish_at
          ? {
              finishAbout: `About ${formatClock(Date.parse(job.estimated_finish_at), now, zone)}`,
            }
          : {}),
        canStop: true,
      };
    case "COMPLETED":
      return { label: "Finished", tone: "ok", counts: done, tried, canStop: false };
    case "CANCELLED":
      return stopped("Stopped", "idle");
    case "FAILED":
      return stopped("Stopped by an error", "bad");
  }

  function stopped(label: string, tone: JobWords["tone"]): JobWords {
    return {
      label,
      tone,
      counts: `${done}, ${rest} not sent (stopped)`,
      tried,
      stopped: rest,
      canStop: false,
    };
  }
}
