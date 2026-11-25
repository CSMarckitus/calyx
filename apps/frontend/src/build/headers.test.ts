// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { parseHeaders } from "./headers";

function read(path: string): Record<string, string> {
  return parseHeaders(readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8"));
}

describe("parseHeaders", () => {
  it("reads add_header lines and skips comments", () => {
    const h = parseHeaders('# note\nadd_header A "x y" always;\nadd_header B "z";\n');
    expect(h).toEqual({ A: "x y", B: "z" });
  });

  it("reads the real headers.conf", () => {
    const h = parseHeaders(
      readFileSync(fileURLToPath(new URL("../../headers.conf", import.meta.url)), "utf8"),
    );
    expect(Object.keys(h).sort()).toEqual([
      "Content-Security-Policy",
      "Permissions-Policy",
      "Referrer-Policy",
      "X-Content-Type-Options",
    ]);
    expect(h["Content-Security-Policy"]).toBe(
      "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
    );
  });

  it("keeps the same four header names in headers-docs.conf", () => {
    expect(Object.keys(read("../../headers-docs.conf")).sort()).toEqual(
      Object.keys(read("../../headers.conf")).sort(),
    );
  });

  it("differs from the panel only by the style exception for Scalar", () => {
    const panel = read("../../headers.conf");
    const docs = read("../../headers-docs.conf");
    expect(docs["Content-Security-Policy"]).toBe(
      `${panel["Content-Security-Policy"]}; style-src 'self' 'unsafe-inline'`,
    );
    for (const name of Object.keys(panel)) {
      if (name !== "Content-Security-Policy") expect(docs[name]).toBe(panel[name]);
    }
  });
});
