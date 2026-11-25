// Checked by tsc only. It never runs.
import type * as grpcEvent from "../src/generated/core-grpc/calyx/core/v1/event.js";
import type * as grpcInstance from "../src/generated/core-grpc/calyx/core/v1/instance.js";
import type * as grpcMessage from "../src/generated/core-grpc/calyx/core/v1/message.js";
import type * as typesEvent from "../src/generated/core-types/calyx/core/v1/event.js";
import type * as typesInstance from "../src/generated/core-types/calyx/core/v1/instance.js";
import type * as typesMessage from "../src/generated/core-types/calyx/core/v1/message.js";

// Each enum of core-types is assignable to its copy in core-grpc, and the reverse.
type Pair<A, B> = [A] extends [B] ? ([B] extends [A] ? true : never) : never;
const same = <A, B>(_: Pair<A, B>) => undefined;

same<typesInstance.InstanceStatus, grpcInstance.InstanceStatus>(true);
same<typesInstance.Provider, grpcInstance.Provider>(true);
same<typesInstance.DeviceIdentity, grpcInstance.DeviceIdentity>(true);
same<typesMessage.ErrorReason, grpcMessage.ErrorReason>(true);
same<typesMessage.BulkJobStatus, grpcMessage.BulkJobStatus>(true);
same<typesEvent.MessageDeliveryStatus, grpcEvent.MessageDeliveryStatus>(true);

// isCalyxError narrows err.code.
import { isCalyxError } from "../src/errors/index.js";

declare const caught: unknown;
if (isCalyxError(caught, "duplicate_message")) {
  const code: "duplicate_message" = caught.code;
  void code;
}
if (isCalyxError(caught)) {
  const known = caught.code === "not_found";
  void known;
}
// @ts-expect-error "no_such_code" is not a known code.
isCalyxError(caught, "no_such_code");

import type { MessageReceived } from "../src/generated/core-types/calyx/core/v1/event.js";
// The webhook example narrows event.data.
import { type CalyxEvent, verifyWebhook } from "../src/webhooks/index.js";

declare const rawBody: string;
declare const headers: Headers;
async function receive() {
  const event = await verifyWebhook({ rawBody, headers, secret: "s" });
  if (event.event === "message.received") {
    const data: MessageReceived = event.data;
    void data.messageId;
  } else if (event.event === "connection.update") {
    const status: string = event.data.status;
    void status;
  } else if (event.event.startsWith("whatsmeow.")) {
    const unknownData: unknown = event.data;
    void unknownData;
  }
}
void receive;
// @ts-expect-error the data of "message.received" has no "status" field.
(null as unknown as Extract<CalyxEvent, { event: "message.received" }>).data.status;

// The known part of each open status union is exactly the enum names, so a change of the enums
// breaks this check. `(string & {})` accepts any string, so a value check cannot prove this.
import type {
  BulkJobStatusName,
  InstanceStatusName,
  MessageDeliveryStatusName,
} from "../src/webhooks/index.js";

type Known<T> = T extends string ? (string extends T ? never : T) : never;
same<
  Known<InstanceStatusName>,
  "CONNECTED" | "CONNECTING" | "NEEDS_AUTH" | "DISCONNECTED" | "ERROR"
>(true);
same<Known<BulkJobStatusName>, "QUEUED" | "RUNNING" | "COMPLETED" | "CANCELLED" | "FAILED">(true);
same<Known<MessageDeliveryStatusName>, "sent" | "delivered" | "read" | "failed">(true);
const connectionStatus = (
  event: Extract<CalyxEvent, { event: "connection.update" }>,
): InstanceStatusName => event.data.status;
void connectionStatus;

import type * as browserCore from "../src/core/browser.js";
// Core: createCoreClient has no default for tls, so a call without it is a compile error.
import { type CoreClient, type CoreClientOptions, createCoreClient } from "../src/core/index.js";

// @ts-expect-error tls is required.
createCoreClient({ address: "core:50051", token: "t" });
const coreClient = createCoreClient({ address: "core:50051", token: "t", tls: false });
// The real client returns CoreClient, not never.
same<ReturnType<typeof createCoreClient>, CoreClient>(true);
// The browser stub takes the options of the real client, so the two cannot drift.
same<Parameters<typeof browserCore.createCoreClient>[0], CoreClientOptions | undefined>(true);
// The idempotency key of a send goes in the options, not in the request.
coreClient.sendPlainText(
  // @ts-expect-error idempotencyKey is not a field of the params.
  { instanceName: "i", to: "1", text: "t", idempotencyKey: "k" },
);
// subscribe gives the parsed events, not the generated Event messages.
same<ReturnType<CoreClient["subscribe"]>, AsyncIterable<CalyxEvent>>(true);
// A filter can leave out a field.
coreClient.subscribe({ instanceNames: ["i_1"] });
