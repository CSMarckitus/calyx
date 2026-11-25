import type { operations as Generated } from "../generated/backend.js";
import { newIdempotencyKey } from "../shared/key.js";
import { type CallOptions, type HttpConfig, request } from "./http.js";
import { type IncludedOperationId, operations } from "./operations.js";
import { openPairingStream, type PairingItem } from "./sse.js";

export type BackendClientConfig = HttpConfig;

export interface SendOptions extends CallOptions {
  idempotencyKey?: string;
}

type JsonId = Exclude<IncludedOperationId, "followPairing">;
type SendId = "sendMessage" | "createBulkJob";
type CreateId = "createClient" | "createInstance";

type Op<K extends JsonId> = Generated[K];

type PathParams<O> = O extends { parameters: { path: infer P } } ? P : unknown;
type Body<O> = O extends { requestBody: { content: { "application/json": infer B } } }
  ? B
  : unknown;
export type Params<K extends JsonId> = PathParams<Op<K>> & Body<Op<K>>;

type SuccessCode = 200 | 201 | 202 | 203 | 204;
type Success<R> = {
  [S in keyof R & SuccessCode]: R[S] extends { content: { "application/json": infer D } }
    ? D
    : undefined;
}[keyof R & SuccessCode];
export type Data<K extends JsonId> = Success<Op<K>["responses"]>;

type Args<K extends JsonId, O> = keyof Params<K> extends never
  ? [params?: Record<string, never>, options?: O]
  : [params: Params<K>, options?: O];

export type BackendClient = {
  [K in Exclude<JsonId, SendId | CreateId>]: (...args: Args<K, CallOptions>) => Promise<Data<K>>;
} & {
  /**
   * Creates a client.
   *
   * The call has no idempotency key. After a client error (`timeout`, `network_error`,
   * `invalid_response` or `aborted`), the advice is `unknown`: the backend can have made the
   * client. List the clients before you call again, because a second call can make a duplicate.
   */
  createClient: (...args: Args<"createClient", CallOptions>) => Promise<Data<"createClient">>;
  /**
   * Creates an instance and starts its pairing.
   *
   * The call has no idempotency key. After a client error (`timeout`, `network_error`,
   * `invalid_response` or `aborted`), the advice is `unknown`: the backend can have made the
   * instance. List the instances before you call again, because a second call can make a duplicate.
   */
  createInstance: (...args: Args<"createInstance", CallOptions>) => Promise<Data<"createInstance">>;
} & {
  // No time limit: the stream has an idle timeout. It ends with a CalyxError, never reopens.
  followPairing: (
    params: PathParams<Generated["followPairing"]>,
    options?: { signal?: AbortSignal },
  ) => AsyncIterable<PairingItem>;
} & {
  [K in SendId]: (
    ...args: Args<K, SendOptions>
  ) => Promise<{ data: Data<K>; idempotencyKey: string }>;
};

const sends = new Set<string>(["sendMessage", "createBulkJob"]);

export function createBackendClient(config: BackendClientConfig): BackendClient {
  const client: Record<string, unknown> = {};
  for (const [id, row] of Object.entries(operations)) {
    if (row.kind !== "json") continue;
    client[id] = async (params: Record<string, unknown> = {}, options: SendOptions = {}) => {
      if (!sends.has(id)) return request({ config, op: id, row, params, options });
      const idempotencyKey = options.idempotencyKey ?? newIdempotencyKey();
      const data = await request({ config, op: id, row, params, options, idempotencyKey });
      return { data, idempotencyKey };
    };
  }
  client.followPairing = (
    params: Record<string, unknown>,
    options: { signal?: AbortSignal } = {},
  ) =>
    openPairingStream({
      config,
      row: operations.followPairing,
      params,
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
  return client as unknown as BackendClient;
}
