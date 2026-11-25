import { describe, expect, it } from "vitest";
import type { components } from "../api/generated/types.ts";
import { describeJob } from "./bulk.ts";

type BulkJob = components["schemas"]["BulkJob"];

const zone = "UTC";
const now = Date.UTC(2026, 10, 24, 12, 0, 0);

function job(over: Partial<BulkJob>): BulkJob {
  return {
    created_at: "2026-11-24T10:00:00Z",
    estimated_finish_at: null,
    failed_messages: 0,
    id: "j1",
    instance_id: "i1",
    progress_percent: 0,
    replayed: false,
    sent_messages: 0,
    status: "QUEUED",
    total_messages: 10,
    ...over,
  };
}

describe("describeJob", () => {
  it("QUEUED: waiting to start, no message tried, can stop", () => {
    expect(describeJob(job({}), now, zone)).toEqual({
      label: "Waiting to start",
      tone: "wait",
      counts: "0 of 10 tried",
      tried: 0,
      canStop: true,
    });
  });
  it("RUNNING: counters, waiting and the estimated end today", () => {
    const d = describeJob(
      job({
        status: "RUNNING",
        sent_messages: 4,
        failed_messages: 1,
        estimated_finish_at: "2026-11-24T13:45:00Z",
      }),
      now,
      zone,
    );
    expect(d).toEqual({
      label: "Sending",
      tone: "wait",
      counts: "4 sent, 1 not sent (failed or unknown), 5 waiting",
      tried: 5,
      waiting: 5,
      finishAbout: "About 13:45",
      canStop: true,
    });
  });
  it("RUNNING: adds the date when the end is not today", () => {
    const d = describeJob(
      job({ status: "RUNNING", estimated_finish_at: "2026-11-25T01:05:00Z" }),
      now,
      zone,
    );
    expect(d.finishAbout).toBe("About 25 November, 01:05");
  });
  it("RUNNING: uses the fixed zone for the day", () => {
    const d = describeJob(
      job({ status: "RUNNING", estimated_finish_at: "2026-11-24T23:30:00Z" }),
      now,
      "Europe/Madrid",
    );
    expect(d.finishAbout).toBe("About 25 November, 00:30");
  });
  it("RUNNING: no estimate gives no finishAbout; no message tried waits for all", () => {
    const d = describeJob(job({ status: "RUNNING" }), now, zone);
    expect(d.finishAbout).toBeUndefined();
    expect(d.counts).toBe("0 sent, 0 not sent (failed or unknown), 10 waiting");
    expect(d.waiting).toBe(10);
  });
  it("COMPLETED: finished, no waiting, cannot stop", () => {
    expect(
      describeJob(
        job({ status: "COMPLETED", sent_messages: 9, failed_messages: 1, progress_percent: 100 }),
        now,
        zone,
      ),
    ).toEqual({
      label: "Finished",
      tone: "ok",
      counts: "9 sent, 1 not sent (failed or unknown)",
      tried: 10,
      canStop: false,
    });
  });
  it("COMPLETED with all messages failed is still finished", () => {
    const d = describeJob(job({ status: "COMPLETED", failed_messages: 10 }), now, zone);
    expect(d.counts).toBe("0 sent, 10 not sent (failed or unknown)");
    expect(d.tried).toBe(10);
  });
  it("CANCELLED after some messages went out: stopped, with the not sent (stopped)", () => {
    expect(
      describeJob(job({ status: "CANCELLED", sent_messages: 3, failed_messages: 2 }), now, zone),
    ).toEqual({
      label: "Stopped",
      tone: "idle",
      counts: "3 sent, 2 not sent (failed or unknown), 5 not sent (stopped)",
      tried: 5,
      stopped: 5,
      canStop: false,
    });
  });
  it("CANCELLED with no message tried: all are stopped", () => {
    const d = describeJob(job({ status: "CANCELLED" }), now, zone);
    expect(d.counts).toBe("0 sent, 0 not sent (failed or unknown), 10 not sent (stopped)");
    expect(d.tried).toBe(0);
  });
  it("FAILED: stopped by an error, the counters of CANCELLED", () => {
    expect(
      describeJob(job({ status: "FAILED", sent_messages: 6, failed_messages: 1 }), now, zone),
    ).toEqual({
      label: "Stopped by an error",
      tone: "bad",
      counts: "6 sent, 1 not sent (failed or unknown), 3 not sent (stopped)",
      tried: 7,
      stopped: 3,
      canStop: false,
    });
  });
  it("never gives a negative count", () => {
    const d = describeJob(
      job({ status: "RUNNING", sent_messages: 8, failed_messages: 5 }),
      now,
      zone,
    );
    expect(d.waiting).toBe(0);
  });
});
