import { describe, expect, it } from "vitest";
import { checkPassword } from "./password.ts";

describe("checkPassword", () => {
  it("accepts 10 to 128 characters", () => {
    expect(checkPassword("a".repeat(10))).toBeNull();
    expect(checkPassword("a".repeat(128))).toBeNull();
  });

  it("refuses fewer than 10 characters", () => {
    expect(checkPassword("")).toBe("At least 10 characters.");
    expect(checkPassword("a".repeat(9))).toBe("At least 10 characters.");
  });

  it("refuses more than 128 characters", () => {
    expect(checkPassword("a".repeat(129))).toBe("At most 128 characters.");
  });

  // The backend counts runes, not UTF-16 units: an emoji is one character.
  it("counts characters as the backend does", () => {
    expect(checkPassword("😀".repeat(9))).toBe("At least 10 characters.");
    expect(checkPassword("😀".repeat(10))).toBeNull();
    expect(checkPassword("😀".repeat(128))).toBeNull();
  });
});
