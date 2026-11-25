// The parser of server-sent events, copied from packages/client-sdk/typescript/src/backend/sse.ts
// (Apache-2.0, CSMarckitus). See NOTICE.

export interface Frame {
  event: string;
  data: string;
}

export class EventTooLarge extends Error {}

export const maxEventBytes = 1024 * 1024;

const encoder = new TextEncoder();
export const byteLength = (text: string) => encoder.encode(text).byteLength;

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
