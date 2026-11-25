import { CalyxError } from "../errors/index.js";
import type {
  BulkFinished,
  ConnectionUpdate,
  Event,
  MessageDeliveryStatus,
  MessageReceived,
  MessageStatus,
} from "../generated/core-types/calyx/core/v1/event.js";
import type { InstanceStatus } from "../generated/core-types/calyx/core/v1/instance.js";
import type { BulkJobStatus } from "../generated/core-types/calyx/core/v1/message.js";

// A status union names the enum values without the prefix and without the two values that are
// not real states. The `string & {}` part keeps the union open for a value of a newer core.
type Names<E, Prefix extends string> =
  Exclude<keyof E, `${Prefix}UNSPECIFIED` | "UNRECOGNIZED"> extends infer K
    ? K extends `${Prefix}${infer Name}`
      ? Name
      : never
    : never;

export type InstanceStatusName = Names<typeof InstanceStatus, "INSTANCE_STATUS_"> | (string & {});
export type BulkJobStatusName = Names<typeof BulkJobStatus, "BULK_JOB_STATUS_"> | (string & {});
export type MessageDeliveryStatusName =
  | Lowercase<Names<typeof MessageDeliveryStatus, "MESSAGE_DELIVERY_STATUS_">>
  | (string & {});

export type ConnectionUpdateData = Omit<ConnectionUpdate, "status"> & {
  status: InstanceStatusName;
};
export type MessageStatusData = Omit<MessageStatus, "status"> & {
  status: MessageDeliveryStatusName;
};
export type BulkFinishedData = Omit<BulkFinished, "status"> & { status: BulkJobStatusName };

interface Envelope {
  id: string;
  instance: string;
  timestamp: string;
}

// The catch-all uses template literals, not `string`, so a check of `event` narrows `data`.
export type CalyxEvent =
  | (Envelope & { event: "connection.update"; data: ConnectionUpdateData })
  | (Envelope & { event: "message.received"; data: MessageReceived })
  | (Envelope & { event: "message.status"; data: MessageStatusData })
  | (Envelope & { event: "bulk.finished"; data: BulkFinishedData })
  | (Envelope & { event: `whatsmeow.${string}` | "meta.webhook"; data: unknown });

export function invalidEvent(detail: string, cause?: unknown): CalyxError {
  return new CalyxError({ source: "client", code: "invalid_event", status: 0, detail, cause });
}

// Checks the shape of the envelope only. The payload keeps the type that the event name gives.
// An empty id, event or instance is refused, because programs remove duplicates by id.
export function toCalyxEvent(value: unknown): CalyxEvent {
  const v = value as Record<string, unknown> | null;
  if (
    typeof v !== "object" ||
    v === null ||
    Array.isArray(v) ||
    !nonEmpty(v.id) ||
    !nonEmpty(v.event) ||
    !nonEmpty(v.instance) ||
    typeof v.timestamp !== "string" ||
    !("data" in v)
  ) {
    throw invalidEvent("The body is not a Calyx event.");
  }
  return v as unknown as CalyxEvent;
}

function nonEmpty(value: unknown): boolean {
  return typeof value === "string" && value !== "";
}

export function parseEvent(event: Event): CalyxEvent {
  if (event.occurredAt === undefined) throw invalidEvent("The event has no time.");
  let data: unknown;
  try {
    data = JSON.parse(event.payloadJson);
  } catch (cause) {
    throw invalidEvent("The payload of the event is not valid JSON.", cause);
  }
  return toCalyxEvent({
    id: event.id,
    event: event.type,
    instance: event.instanceName,
    data,
    timestamp: event.occurredAt.toISOString(),
  });
}
