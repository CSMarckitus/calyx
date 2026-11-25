import { useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { Failure } from "@/components/failure";
import { type Instance, StateWord } from "@/components/instance-card";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import type { components } from "../api/generated/types.ts";
import { keys, useBulkJobs, useChange, useMe } from "../api/queries.ts";
import type { Failure as RequestFailure } from "../api/request.ts";
import { describeJob } from "../domain/bulk.ts";
import { formatTime, formatWithSeconds } from "../domain/time.ts";

type BulkJob = components["schemas"]["BulkJob"];
type BulkJobList = components["schemas"]["BulkJobList"];

const stopText =
  "Messages that Calyx already sent stay sent. A message that goes out now can still arrive.";

function JobItem({
  job,
  now,
  onStop,
  children,
}: {
  job: BulkJob;
  now: number;
  onStop: () => void;
  // The failure of a "Stop sending" of this job.
  children?: ReactNode;
}) {
  const d = describeJob(job, now);
  const created = formatTime(Date.parse(job.created_at), now);
  const tried = `${d.tried} of ${job.total_messages} tried`;
  const triedId = useId();
  return (
    <li
      data-slot="bulk-job"
      className="grid gap-2 border-t border-border py-3 first:border-t-0 first:pt-0"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <StateWord color={d.tone}>{d.label}</StateWord>
        <span className="flex flex-wrap gap-x-3 text-sm text-muted-foreground">
          <span title={created.title}>{`Created ${created.text}`}</span>
          <span>{`${job.total_messages} ${job.total_messages === 1 ? "message" : "messages"}`}</span>
        </span>
      </div>
      {/* A waiting job has only "0 of N tried", and the progress shows it. */}
      {job.status !== "QUEUED" && <p className="m-0">{d.counts}</p>}
      <div className="grid gap-1">
        <progress
          value={job.progress_percent}
          max={100}
          aria-labelledby={triedId}
          className="h-2 w-full accent-primary"
        />
        <span id={triedId} className="text-sm">
          {tried}
        </span>
      </div>
      {d.finishAbout && <p className="m-0">{`Estimated end: ${d.finishAbout}`}</p>}
      {d.canStop && (
        <Button variant="destructive" className="justify-self-start" onClick={onStop}>
          Stop sending
        </Button>
      )}
      {children}
    </li>
  );
}

// `/instances/:id/bulk-jobs`: the bulk jobs of an instance. Programs start the jobs. The
// page only shows them and stops them. Each open reads the jobs (`bulkJobsOptions`), and no
// timer reads them.
export function BulkJobsView({ instance, level }: { instance: Instance; level: 1 | 2 }) {
  const Heading = level === 1 ? "h1" : "h2";
  // The drawer stays open when the card changes to the view, so its first focus does not run.
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  const me = useMe().data;
  const read = useBulkJobs(instance.id);
  const cancel = useChange("cancelBulkJob");
  const client = useQueryClient();
  const [asking, setAsking] = useState<BulkJob | null>(null);
  // The job of the last "Stop sending": its failure shows on it.
  const [stopping, setStopping] = useState<string | null>(null);
  // After a confirmed stop, the button of the job goes away, so the focus goes to the heading
  // and not to the body.
  const confirmed = useRef(false);

  const context = me && {
    role: me.user.role === "owner" ? ("owner" as const) : ("client" as const),
    manager: me.organization.name,
  };
  // A read throws its failure (queries.ts).
  const readFailure = read.error as unknown as RequestFailure | null;
  const items = read.data?.items ?? [];
  const now = Date.now();

  function checkAgain() {
    cancel.reset();
    void read.refetch({ cancelRefetch: false });
  }

  function ask(job: BulkJob) {
    confirmed.current = false;
    setAsking(job);
  }

  // One request. After any answer, useChange reads the jobs again, and it never sends the
  // cancel again by itself.
  function stop(job: BulkJob) {
    confirmed.current = true;
    setAsking(null);
    setStopping(job.id);
    cancel.mutate(
      { path: { id: job.id } },
      {
        // The answer is the job now, also a job that ended in the meantime. "Read at" stays the
        // time of the list.
        onSuccess: (answer) => {
          if (!answer) return;
          client.setQueryData<BulkJobList>(
            keys.bulkJobs(instance.id),
            (old) => old && { items: old.items.map((j) => (j.id === answer.id ? answer : j)) },
            { updatedAt: read.dataUpdatedAt },
          );
        },
      },
    );
  }

  return (
    <section data-slot="bulk-jobs" className="grid gap-4">
      <Heading ref={heading} tabIndex={-1}>
        Bulk jobs
      </Heading>
      <Link
        to={`/instances/${instance.id}`}
        className="inline-flex min-h-11 items-center justify-self-start font-semibold text-primary underline underline-offset-3"
      >
        Back
      </Link>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        {read.data && (
          <p className="m-0 text-muted-foreground">{`Read at ${formatWithSeconds(read.dataUpdatedAt)}`}</p>
        )}
        <Button variant="outline" onClick={checkAgain}>
          Check again
        </Button>
      </div>
      {readFailure && context && (
        <Failure failure={readFailure} context={{ operationId: "listBulkJobs", ...context }} />
      )}
      {/* A failed read keeps the last list and its time (Shape). */}
      {read.data &&
        (items.length === 0 ? (
          <p className="m-0">No bulk jobs. Programs start them through the API or MCP.</p>
        ) : (
          <ul className="m-0 grid list-none p-0">
            {items.map((job) => (
              <JobItem key={job.id} job={job} now={now} onStop={() => ask(job)}>
                {cancel.isError && stopping === job.id && context && (
                  <Failure
                    failure={cancel.error}
                    context={{ operationId: "cancelBulkJob", ...context }}
                    checking={cancel.checking}
                  />
                )}
              </JobItem>
            ))}
          </ul>
        ))}
      <p className="m-0 text-sm text-muted-foreground">The 50 newest jobs that Calyx keeps.</p>
      <p className="m-0 rounded-md bg-idle-soft px-4 py-3.5 text-[0.9375rem]">
        To stop a program,{" "}
        <Link
          to="/developers/api-keys"
          className="font-semibold text-primary underline underline-offset-3"
        >
          revoke its key
        </Link>{" "}
        first. An organization admin can also remove the user of a client.
      </p>

      <Dialog open={asking !== null} onOpenChange={(open) => !open && setAsking(null)}>
        <DialogContent
          showCloseButton={false}
          finalFocus={() => (confirmed.current ? heading.current : true)}
        >
          <DialogTitle>Stop sending?</DialogTitle>
          <DialogDescription>{stopText}</DialogDescription>
          <DialogFooter>
            <Button
              variant="destructive"
              disabled={cancel.isPending}
              onClick={() => asking && stop(asking)}
            >
              Stop sending
            </Button>
            <Button variant="outline" onClick={() => setAsking(null)}>
              Keep sending
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
