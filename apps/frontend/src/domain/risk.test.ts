// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { risk } from "./risk.ts";

// The first two paragraphs of "Warning: WhatsApp Web", with each line break as a space.
function readmeParagraphs(): string[] {
  const readme = readFileSync(
    fileURLToPath(new URL("../../../backend/README.md", import.meta.url)),
    "utf8",
  );
  const section = readme.split("## Warning: WhatsApp Web\n")[1]?.split("\n## ")[0] ?? "";
  return section
    .split(/\n\s*\n/)
    .map((p) => p.replace(/\s*\n\s*/g, " ").trim())
    .filter((p) => p !== "")
    .slice(0, 2);
}

describe("risk", () => {
  it("is the first two paragraphs of the warning of the backend README", () => {
    const paragraphs = readmeParagraphs();
    expect(paragraphs).toHaveLength(2);
    expect(risk).toEqual(paragraphs);
  });

  it("never promises that a number cannot be banned", () => {
    expect(risk.join(" ")).toContain("WhatsApp can ban the number.");
    expect(risk.join(" ")).toContain("It does not remove the risk.");
  });
});
