import { describe, expect, it } from "vitest";
import { EventTooLarge, FrameParser, maxEventBytes } from "./sse.ts";

const frames = (...chunks: string[]) => {
  const parser = new FrameParser();
  return chunks.flatMap((c) => parser.push(c));
};

describe("FrameParser", () => {
  it("reads one frame", () => {
    expect(frames("event: update\ndata: {}\n\n")).toEqual([{ event: "update", data: "{}" }]);
  });

  it("reads a frame split in many chunks", () => {
    expect(frames("event: up", "date\nda", "ta: 1\n", "\n")).toEqual([
      { event: "update", data: "1" },
    ]);
  });

  it("joins data lines with a newline", () => {
    expect(frames("data: a\ndata: b\n\n")).toEqual([{ event: "", data: "a\nb" }]);
  });

  it("accepts CRLF, CR and LF", () => {
    expect(frames("event: a\r\ndata: 1\r\n\r\nevent: b\rdata: 2\r\rdata: 3\n\n")).toEqual([
      { event: "a", data: "1" },
      { event: "b", data: "2" },
      { event: "", data: "3" },
    ]);
  });

  it("joins a CRLF split between two chunks", () => {
    expect(frames("data: 1\r", "\n\r", "\n", "data: 2\n\n")).toEqual([
      { event: "", data: "1" },
      { event: "", data: "2" },
    ]);
  });

  it("ignores comments and a frame with no data", () => {
    expect(frames(": ping\n\n", "event: x\n\n", ": hello\n: other\n\n")).toEqual([]);
  });

  it("accepts a field with no space after the colon", () => {
    expect(frames("event:a\ndata:b\n\n")).toEqual([{ event: "a", data: "b" }]);
  });

  it("takes an empty chunk as nothing", () => {
    expect(frames("data: 1\r", "", "\n\n")).toEqual([{ event: "", data: "1" }]);
  });

  it("fails on an event above the limit, counted in bytes", () => {
    expect(() => new FrameParser().push(`data: ${"x".repeat(maxEventBytes)}\n\n`)).toThrow(
      EventTooLarge,
    );
    // 3 bytes each: 350000 characters are 1050000 bytes, over 1 MiB.
    expect(() => new FrameParser().push(`data: ${"€".repeat(350_000)}\n\n`)).toThrow(EventTooLarge);
    expect(new FrameParser().push(`data: ${"x".repeat(1000)}\n\n`)).toHaveLength(1);
  });

  it("fails on a line with no end that grows above the limit", () => {
    const parser = new FrameParser();
    expect(() => {
      for (let i = 0; i < 20; i++) parser.push("x".repeat(100_000));
    }).toThrow(EventTooLarge);
  });
});
