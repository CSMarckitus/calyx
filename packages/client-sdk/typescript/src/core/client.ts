import { isIP } from "node:net";

import {
  type ChannelCredentials,
  Client,
  type ClientReadableStream,
  type ClientUnaryCall,
  credentials,
  Metadata,
  type ServiceError,
} from "@grpc/grpc-js";

import { CalyxError } from "../errors/index.js";
import {
  EventServiceService,
  type SubscribeRequest,
} from "../generated/core-grpc/calyx/core/v1/event.js";
import {
  type CreateInstanceRequest,
  type CreateInstanceResponse,
  type GetInstanceStatusRequest,
  type GetInstanceStatusResponse,
  InstanceServiceService,
  type ListInstancesRequest,
  type ListInstancesResponse,
  type RemoveInstanceRequest,
  type RemoveInstanceResponse,
  type UpdateInstanceConfigRequest,
  type UpdateInstanceConfigResponse,
} from "../generated/core-grpc/calyx/core/v1/instance.js";
import {
  type BulkJob,
  type CancelBulkJobRequest,
  type GetBulkJobRequest,
  type GetBulkJobsRequest,
  type GetBulkJobsResponse,
  MessageServiceService,
  type SendBulkCustomMessagesRequest,
  type SendBulkSameMessageRequest,
  type SendPlainTextRequest,
  type SendPlainTextResponse,
} from "../generated/core-grpc/calyx/core/v1/message.js";
import { newIdempotencyKey } from "../shared/key.js";
import { type CalyxEvent, parseEvent } from "../webhooks/events.js";
import { coreError } from "./errors.js";

export interface CoreClientOptions {
  // "host:port"
  address: string;
  token: string;
  // No default: a program without TLS writes false on purpose. The contents are not file paths.
  tls: boolean | { ca?: Uint8Array; cert?: Uint8Array; key?: Uint8Array };
  timeoutMs?: number;
}

export interface CallOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export interface StreamOptions {
  signal?: AbortSignal;
}

export interface SendOptions extends CallOptions {
  idempotencyKey?: string;
}

// The SDK sets idempotencyKey from the options, so the params do not have it.
type SendParams<Req> = Omit<Req, "idempotencyKey">;
type Sent<Res> = Promise<{ data: Res; idempotencyKey: string }>;

export interface CoreClient {
  listInstances(
    params?: ListInstancesRequest,
    options?: CallOptions,
  ): Promise<ListInstancesResponse>;
  getInstanceStatus(
    params: GetInstanceStatusRequest,
    options?: CallOptions,
  ): Promise<GetInstanceStatusResponse>;
  removeInstance(
    params: RemoveInstanceRequest,
    options?: CallOptions,
  ): Promise<RemoveInstanceResponse>;
  updateInstanceConfig(
    params: UpdateInstanceConfigRequest,
    options?: CallOptions,
  ): Promise<UpdateInstanceConfigResponse>;
  getBulkJob(params: GetBulkJobRequest, options?: CallOptions): Promise<BulkJob>;
  // 1 to 50 job ids. The core leaves out an unknown job, and the order of the jobs is not defined.
  getBulkJobs(params: GetBulkJobsRequest, options?: CallOptions): Promise<GetBulkJobsResponse>;
  cancelBulkJob(params: CancelBulkJobRequest, options?: CallOptions): Promise<BulkJob>;
  sendPlainText(
    params: SendParams<SendPlainTextRequest>,
    options?: SendOptions,
  ): Sent<SendPlainTextResponse>;
  sendBulkSameMessage(
    params: SendParams<SendBulkSameMessageRequest>,
    options?: SendOptions,
  ): Sent<BulkJob>;
  sendBulkCustomMessages(
    params: SendParams<SendBulkCustomMessagesRequest>,
    options?: SendOptions,
  ): Sent<BulkJob>;
  // Always break or abort a stream (createInstance or subscribe): a dropped iterator keeps the
  // call open. A break or the signal cancels the stream: for createInstance, the core stops the
  // pairing.
  createInstance(
    params: CreateInstanceRequest,
    options?: StreamOptions,
  ): AsyncIterable<CreateInstanceResponse>;
  // Events that occur while no stream is open are lost. The SDK never opens the stream again.
  // An absent filter field means all, as an empty one.
  subscribe(params?: Partial<SubscribeRequest>, options?: StreamOptions): AsyncIterable<CalyxEvent>;
  // Closes the channel. Calls already sent can still finish. A call that still connects gets
  // UNAVAILABLE from grpc-js.
  close(): void;
}

// A cancelled call removes a waiting send from the core queue, so the send limit is longer than
// the slowest normal send (4 minutes).
const sendLimitMs = 6 * 60_000;
const callLimitMs = 2 * 60_000;

// Finds a core host that disappears. The core refuses pings more often than once a minute, and
// pings while no call runs. Exported for the tests only: index.ts does not have it.
export const keepalive = {
  "grpc.keepalive_time_ms": 2 * 60_000,
  "grpc.keepalive_timeout_ms": 20_000,
  "grpc.keepalive_permit_without_calls": 0,
};

interface Method<Req, Res> {
  path: string;
  requestSerialize(value: Req): Buffer;
  responseDeserialize(value: Buffer): Res;
}

function channelCredentials(tls: CoreClientOptions["tls"]): ChannelCredentials {
  if (tls === false) return credentials.createInsecure();
  if (tls === true) return credentials.createSsl();
  const contents = (b: Uint8Array | undefined) => (b === undefined ? null : Buffer.from(b));
  return credentials.createSsl(contents(tls.ca), contents(tls.key), contents(tls.cert));
}

// grpc-js puts the host of the address in the TLS server name, and Node 26 refuses an IP address
// there. Without this check, every call fails with UNAVAILABLE, and a retry never helps.
function isIpAddress(address: string): boolean {
  const target = address.replace(/^dns:(\/\/[^/]*\/)?/, "");
  if (/^ipv[46]:/.test(target) || isIP(target) !== 0) return true;
  const host = target.startsWith("[")
    ? target.slice(1, target.indexOf("]"))
    : target.replace(/:\d+$/, "");
  return isIP(host) !== 0;
}

function aborted(detail: string, cause: unknown, idempotencyKey: string | undefined): CalyxError {
  return new CalyxError({
    source: "client",
    code: "aborted",
    status: 0,
    detail,
    cause,
    ...(idempotencyKey === undefined ? {} : { idempotencyKey }),
  });
}

export function createCoreClient(options: CoreClientOptions): CoreClient {
  if (options.tls !== false && isIpAddress(options.address)) {
    throw new CalyxError({
      source: "client",
      code: "invalid_config",
      status: 0,
      detail: "With tls, the address must be a host name, not an IP address.",
    });
  }
  const client = new Client(options.address, channelCredentials(options.tls), { ...keepalive });
  let closed = false;

  // Call metadata, not call credentials: grpc-js refuses those on a channel without TLS.
  function tokenMetadata(): Metadata {
    const metadata = new Metadata();
    metadata.set("authorization", `Bearer ${options.token}`);
    return metadata;
  }

  // A stream has no deadline. A break, the signal or an error of the program cancels the call.
  async function* stream<Req, Res>(
    method: Method<Req, Res>,
    params: Req,
    signal: AbortSignal | undefined,
  ) {
    if (closed) throw aborted("The program closed the core client.", undefined, undefined);
    if (signal?.aborted) throw aborted("The program aborted the call.", signal.reason, undefined);
    const call: ClientReadableStream<Res> = client.makeServerStreamRequest(
      method.path,
      method.requestSerialize,
      method.responseDeserialize,
      params,
      tokenMetadata(),
      {},
    );
    // The loop below reads the error. This listener stops a late error, after our own cancel,
    // from crashing the process.
    call.on("error", () => undefined);
    const onAbort = () => call.cancel();
    signal?.addEventListener("abort", onAbort, { once: true });
    const abortedBySignal = () =>
      aborted("The program aborted the call.", signal?.reason, undefined);
    try {
      for await (const item of call) {
        // Node gives the buffered items before the error of the cancel, so check the signal here.
        if (signal?.aborted) throw abortedBySignal();
        yield item as Res;
      }
      // An abort after the end of the stream has priority over the end.
      if (signal?.aborted) throw abortedBySignal();
    } catch (err) {
      if (signal?.aborted) throw abortedBySignal();
      throw coreError(err as ServiceError);
    } finally {
      signal?.removeEventListener("abort", onAbort);
      // Does nothing when the call already ended.
      call.cancel();
    }
  }

  async function* subscribe(
    params: SubscribeRequest,
    signal: AbortSignal | undefined,
  ): AsyncGenerator<CalyxEvent> {
    for await (const event of stream(EventServiceService.subscribe, params, signal)) {
      yield parseEvent(event);
    }
    // The core never ends a Subscribe stream by itself, so an end with status OK is a failure.
    throw new CalyxError({
      source: "client",
      code: "stream_failed",
      status: 0,
      detail: "The core ended the event stream.",
    });
  }

  function unary<Req, Res>(method: Method<Req, Res>, defaultLimitMs: number) {
    // A send gives its idempotency key, so that every error of the call has it.
    return (params: Req, call: CallOptions = {}, idempotencyKey?: string): Promise<Res> =>
      new Promise((resolve, reject) => {
        const { signal } = call;
        if (closed) {
          return reject(aborted("The program closed the core client.", undefined, idempotencyKey));
        }
        if (signal?.aborted) {
          return reject(aborted("The program aborted the call.", signal.reason, idempotencyKey));
        }

        // The call limit, then the client limit, then the default of the method.
        const limit = call.timeoutMs ?? options.timeoutMs ?? defaultLimitMs;

        let pending: ClientUnaryCall | undefined;
        const onAbort = () => pending?.cancel();
        pending = client.makeUnaryRequest(
          method.path,
          method.requestSerialize,
          method.responseDeserialize,
          params,
          tokenMetadata(),
          { deadline: Date.now() + limit },
          (err, res) => {
            signal?.removeEventListener("abort", onAbort);
            if (err === null) return resolve(res as Res);
            reject(
              signal?.aborted
                ? aborted("The program aborted the call.", signal.reason, idempotencyKey)
                : coreError(err, idempotencyKey),
            );
          },
        );
        signal?.addEventListener("abort", onAbort, { once: true });
      });
  }

  // The idempotency key rule of the backend: the key of the options, or a new UUID.
  function send<Req extends { idempotencyKey?: string | undefined }, Res>(
    method: Method<Req, Res>,
    defaultLimitMs: number,
  ) {
    const call = unary(method, defaultLimitMs);
    return async (params: SendParams<Req>, options: SendOptions = {}) => {
      const idempotencyKey = options.idempotencyKey ?? newIdempotencyKey();
      const data = await call({ ...params, idempotencyKey } as Req, options, idempotencyKey);
      return { data, idempotencyKey };
    };
  }

  const i = InstanceServiceService;
  const m = MessageServiceService;
  const listInstances = unary(i.listInstances, callLimitMs);
  return {
    listInstances: (params = {}, call) => listInstances(params, call),
    getInstanceStatus: unary(i.getInstanceStatus, callLimitMs),
    removeInstance: unary(i.removeInstance, callLimitMs),
    updateInstanceConfig: unary(i.updateInstanceConfig, callLimitMs),
    getBulkJob: unary(m.getBulkJob, callLimitMs),
    getBulkJobs: unary(m.getBulkJobs, callLimitMs),
    cancelBulkJob: unary(m.cancelBulkJob, callLimitMs),
    sendPlainText: send(m.sendPlainText, sendLimitMs),
    sendBulkSameMessage: send(m.sendBulkSameMessage, callLimitMs),
    sendBulkCustomMessages: send(m.sendBulkCustomMessages, callLimitMs),
    createInstance: (params, call = {}) => stream(i.createInstance, params, call.signal),
    subscribe: (params = {}, call = {}) =>
      subscribe(
        { instanceNames: params.instanceNames ?? [], eventTypes: params.eventTypes ?? [] },
        call.signal,
      ),
    close: () => {
      closed = true;
      client.close();
    },
  };
}
