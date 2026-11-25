import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { people } from "./people.ts";

const sources = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return e.name === "generated" ? [] : sources(path);
    const isSource = /\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name);
    return isSource && e.name !== "people.ts" ? [path] : [];
  });

describe("people", () => {
  it("has the server admin, the organization admin and the client user, in this order", () => {
    expect(people.map((p) => p.name)).toEqual([
      "Server admin (operator)",
      "Organization admin (owner)",
      "Client user",
    ]);
  });

  it("explains each person with the approved text", () => {
    expect(people.map((p) => p.text)).toEqual([
      "The server admin installs and runs Calyx on a server, and uses the command line, not this panel.",
      "The organization admin manages one organization in this panel: its numbers, its clients and its settings.",
      "A client user is the person of one client, and sees only the numbers of that client.",
    ]);
  });

  it("names its person in each text", () => {
    for (const p of people)
      expect(p.text.toLowerCase()).toContain(p.name.replace(/ \(.*\)$/, "").toLowerCase());
  });

  it("leaves no source file with an old name of a person", () => {
    const files = sources(join(import.meta.dirname, ".."));
    expect(files.length).toBeGreaterThan(20);
    const found = files.filter((f) => {
      const code = readFileSync(f, "utf8");
      return (
        code.includes("the person who installed Calyx") ||
        code.includes("the person who manages Calyx") ||
        code.includes("person who manages") ||
        /the operator/i.test(code)
      );
    });
    expect(found).toEqual([]);
  });
});
