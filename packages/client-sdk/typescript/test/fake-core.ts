import {
  Metadata,
  Server,
  ServerCredentials,
  type ServerUnaryCall,
  type ServerWritableStream,
  type ServiceDefinition,
  type sendUnaryData,
  type UntypedServiceImplementation,
} from "@grpc/grpc-js";

import { EventServiceService } from "../src/generated/core-grpc/calyx/core/v1/event.js";
import { InstanceServiceService } from "../src/generated/core-grpc/calyx/core/v1/instance.js";
import { MessageServiceService } from "../src/generated/core-grpc/calyx/core/v1/message.js";
import { ErrorInfo } from "../src/generated/core-grpc/google/rpc/error_details.js";
import { Status } from "../src/generated/core-grpc/google/rpc/status.js";

export interface Recorded {
  // The method name of the service definition, for example "sendPlainText".
  method: string;
  request: unknown;
  metadata: Record<string, unknown>;
  // The deadline of the call in milliseconds since the epoch, or Infinity.
  deadline: number;
}

export interface ErrorReply {
  code: number;
  details: string;
  // When set, the status carries a google.rpc.ErrorInfo detail.
  info?: { reason: string; domain: string; metadata?: Record<string, string> };
}

export type Reply =
  // Without a value, the answer is the empty message of the method.
  | { value?: unknown }
  | { error: ErrorReply }
  // Never answer. The test ends the call with a time limit, a signal or close().
  | { hang: true }
  // For a stream method only: the test writes the items and ends the stream.
  | { stream: (stream: FakeStream) => void };

export interface FakeStream {
  write(value: unknown): void;
  // Without an error, the stream ends with status OK.
  end(error?: ErrorReply): void;
  // Resolves when the client cancels the call.
  cancelled: Promise<void>;
}

export interface FakeCore {
  address: string;
  calls: Recorded[];
  close(): void;
}

// The trailer that the Go core writes for a status with details.
function statusTrailer(e: ErrorReply): Metadata {
  const metadata = new Metadata();
  if (e.info === undefined) return metadata;
  const info = ErrorInfo.encode({ ...e.info, metadata: e.info.metadata ?? {} }).finish();
  const status = Status.encode({
    code: e.code,
    message: e.details,
    details: [{ typeUrl: "type.googleapis.com/google.rpc.ErrorInfo", value: Buffer.from(info) }],
  }).finish();
  metadata.set("grpc-status-details-bin", Buffer.from(status));
  return metadata;
}

// Listens on 127.0.0.1 in the test process, without TLS unless the test gives credentials.
// No other network.
export async function startFakeCore(
  reply: (method: string, request: unknown) => Reply,
  credentials: ServerCredentials = ServerCredentials.createInsecure(),
): Promise<FakeCore> {
  const calls: Recorded[] = [];
  const server = new Server();

  const services: ServiceDefinition[] = [
    InstanceServiceService,
    MessageServiceService,
    EventServiceService,
  ];
  for (const service of services) {
    const impl: UntypedServiceImplementation = {};
    for (const [method, def] of Object.entries(service)) {
      const record = (
        call: ServerUnaryCall<unknown, unknown> | ServerWritableStream<unknown, unknown>,
      ) => {
        const deadline = call.getDeadline();
        calls.push({
          method,
          request: call.request,
          metadata: call.metadata.getMap(),
          deadline: deadline instanceof Date ? deadline.getTime() : deadline,
        });
        return reply(method, call.request);
      };
      if (def.responseStream) {
        impl[method] = (call: ServerWritableStream<unknown, unknown>) => {
          const cancelled = new Promise<void>((resolve) => call.on("cancelled", () => resolve()));
          const r = record(call);
          if (!("stream" in r)) throw new Error(`The test gave no stream for ${method}.`);
          r.stream({
            write: (value) => call.write(value),
            end: (error) => {
              if (error === undefined) return call.end();
              // grpc-js sends the status of an "error" event of a server stream.
              call.emit("error", {
                code: error.code,
                details: error.details,
                metadata: statusTrailer(error),
              });
            },
            cancelled,
          });
        };
        continue;
      }
      impl[method] = (
        call: ServerUnaryCall<unknown, unknown>,
        callback: sendUnaryData<unknown>,
      ) => {
        const r = record(call);
        if ("hang" in r || "stream" in r) return;
        if ("error" in r) {
          callback({
            code: r.error.code,
            details: r.error.details,
            metadata: statusTrailer(r.error),
          });
          return;
        }
        callback(null, r.value ?? def.responseDeserialize(Buffer.alloc(0)));
      };
    }
    server.addService(service, impl);
  }

  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", credentials, (err, p) =>
      err === null ? resolve(p) : reject(err),
    );
  });
  return {
    address: `127.0.0.1:${port}`,
    calls,
    // forceShutdown ends the calls that hang.
    close: () => server.forceShutdown(),
  };
}
