import { CalyxError } from "../errors/index.js";
import type { operations as Generated } from "../generated/backend.js";
import {
  build,
  type Call,
  fail,
  isAnswerTooLarge,
  maxEventBytes,
  problemError,
  readText,
  setUserAgent,
  tooLarge,
} from "./http.js";

export type PairingItem =
  Generated["followPairing"]["responses"][200]["content"]["text/event-stream"][number];

export interface PairingStreamRequest {
  config: Call["config"];
  row: Call["row"];
  params: Call["params"];
  signal?: AbortSignal;
}

export interface PairingStreamOptions {
  idleTimeoutMs?: number;
}

// The backend pings every 15 seconds, so 45 seconds of silence means a dead connection.
const defaultIdleTimeoutMs = 45_000;

interface Frame {
  event: string;
  data: string;
}

class EventTooLarge extends Error {}

const encoder = new TextEncoder();
const byteLength = (text: string) => encoder.encode(text).byteLength;

// Turns chunks of text into frames. A frame ends at an empty line. A line that starts with ":" is
// a comment. A frame without data lines is dropped.
export class FrameParser {
  private pending = "";
  private event = "";
  private data: string[] = [];
  private afterCR = false;
  // The bytes read for the event now, line terminators included. An empty line resets it.
  private size = 0;

  push(text: string): Frame[] {
    // An empty chunk must not reset afterCR.
    if (text === "") return [];
    // A "\r\n" can split between two chunks: skip the "\n" that follows a "\r" of the last chunk.
    if (this.afterCR && text.startsWith("\n")) {
      text = text.slice(1);
      this.add(1);
    }
    this.afterCR = text.endsWith("\r");
    this.pending += text;
    // With a capture group, split gives the lines and their terminators in turn.
    const parts = this.pending.split(/(\r\n|\n|\r)/);
    this.pending = parts.pop() ?? "";
    const frames: Frame[] = [];
    for (let i = 0; i < parts.length; i += 2) {
      const line = parts[i] ?? "";
      this.add(byteLength(line) + byteLength(parts[i + 1] ?? ""));
      const frame = this.line(line);
      if (frame !== undefined) frames.push(frame);
    }
    // The check runs while a line grows, so a line without an end cannot fill the memory.
    this.add(0, byteLength(this.pending));
    return frames;
  }

  private add(bytes: number, partial = 0): void {
    this.size += bytes;
    if (this.size + partial > maxEventBytes) throw new EventTooLarge();
  }

  private line(line: string): Frame | undefined {
    if (line === "") {
      this.size = 0;
      const frame =
        this.data.length > 0 ? { event: this.event, data: this.data.join("\n") } : undefined;
      this.event = "";
      this.data = [];
      return frame;
    }
    if (line.startsWith(":")) return undefined;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") this.event = value;
    else if (field === "data") this.data.push(value);
    return undefined;
  }
}

const known = new Set(["update", "done", "failed"]);

// Opens the pairing stream. It never opens the stream again by itself. A break in the loop, a
// failure or the end of the generator closes the connection.
export async function* openPairingStream(
  request: PairingStreamRequest,
  options: PairingStreamOptions = {},
): AsyncGenerator<PairingItem, void, undefined> {
  const idleTimeoutMs = options.idleTimeoutMs ?? defaultIdleTimeoutMs;
  const call: Call = {
    config: request.config,
    row: request.row,
    params: request.params,
    options: {},
  };
  const { url } = build(call);
  const user = request.signal;
  const control = new AbortController();
  const abort = () => control.abort();
  user?.addEventListener("abort", abort);
  let idle = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  // The idle time starts before the connection opens and restarts with every chunk.
  const restart = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      idle = true;
      control.abort();
    }, idleTimeoutMs);
  };
  const reason = (err: unknown): CalyxError => {
    if (err instanceof CalyxError) return err;
    if (user?.aborted)
      return fail(call, { code: "aborted", detail: "The program aborted the call.", cause: err });
    if (idle) {
      return fail(call, {
        code: "stream_failed",
        detail: `The pairing stream sent no data for ${idleTimeoutMs} ms.`,
        cause: err,
      });
    }
    return fail(call, { code: "stream_failed", detail: "The pairing stream broke.", cause: err });
  };

  try {
    if (user?.aborted)
      throw fail(call, { code: "aborted", detail: "The program aborted the call." });
    const headers = new Headers({ Accept: "text/event-stream" });
    if (request.config.apiKey !== undefined)
      headers.set("Authorization", `Bearer ${request.config.apiKey}`);
    setUserAgent(headers);
    restart();
    let res: Response;
    try {
      res = await (request.config.fetch ?? globalThis.fetch)(url, {
        method: request.row.method,
        headers,
        signal: control.signal,
      });
    } catch (err) {
      if (user?.aborted || idle) throw reason(err);
      throw fail(call, {
        code: "network_error",
        detail: "The connection to the backend failed.",
        cause: err,
      });
    }
    if (!res.ok) {
      let text = "";
      try {
        text = await readText(res);
      } catch (err) {
        if (isAnswerTooLarge(err)) throw tooLarge(call, err);
        if (user?.aborted || idle) throw reason(err);
        // The status is enough: problemError then reports an answer that is not problem+json.
      }
      throw problemError(call, res, text);
    }
    if (!(res.headers.get("content-type") ?? "").toLowerCase().startsWith("text/event-stream")) {
      throw fail(call, {
        code: "invalid_response",
        detail: `The backend answered ${res.status} with a body that is not an event stream.`,
      });
    }
    if (res.body === null)
      throw fail(call, { code: "invalid_response", detail: "The backend sent no stream." });

    const decoder = new TextDecoder();
    const parser = new FrameParser();
    const reader = res.body.getReader();
    try {
      for (;;) {
        let chunk: ReadableStreamReadResult<Uint8Array>;
        try {
          chunk = await reader.read();
        } catch (err) {
          throw reason(err);
        }
        if (chunk.done) break;
        restart();
        let frames: Frame[];
        try {
          frames = parser.push(decoder.decode(chunk.value, { stream: true }));
        } catch (err) {
          if (!(err instanceof EventTooLarge)) throw err;
          throw fail(call, {
            code: "invalid_response",
            detail: "The backend sent an event that is too large.",
            cause: err,
          });
        }
        for (const frame of frames) {
          if (!known.has(frame.event)) continue;
          let data: unknown;
          try {
            data = JSON.parse(frame.data);
          } catch (err) {
            throw fail(call, {
              code: "invalid_response",
              detail: `The backend sent a "${frame.event}" event with data that is not JSON.`,
              cause: err,
            });
          }
          const item = { event: frame.event, data } as PairingItem;
          // The loop body of the program is not idle time: pings wait in the socket meanwhile.
          clearTimeout(timer);
          yield item;
          restart();
          if (item.event !== "update") return;
        }
      }
    } finally {
      // Cancel the body, so the connection closes. Ignore a failure of a body that already ended.
      await reader.cancel().catch(() => undefined);
    }
    throw fail(call, {
      code: "stream_failed",
      detail: "The pairing stream ended before done or failed.",
    });
  } finally {
    clearTimeout(timer);
    user?.removeEventListener("abort", abort);
    control.abort();
  }
}
