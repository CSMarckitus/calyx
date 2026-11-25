import { describe, expect, it } from "vitest";
import { makeVersion, versionJson } from "./version";

describe("makeVersion", () => {
  it("joins the package version and the build time", () => {
    const v = makeVersion("0.1.0", new Date("2026-11-23T10:20:30.456Z"));
    expect(v).toEqual({ version: "0.1.0", build: "2026-11-23T10:20:30.456Z" });
  });

  it("writes the JSON with the two keys only", () => {
    const v = makeVersion("1.2.3", new Date("2026-01-01T00:00:00.000Z"));
    expect(JSON.parse(versionJson(v))).toEqual({
      version: "1.2.3",
      build: "2026-01-01T00:00:00.000Z",
    });
  });
});
