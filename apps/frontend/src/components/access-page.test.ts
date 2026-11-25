import { describe, expect, it } from "vitest";
import type { Failure } from "../api/request.ts";
import { passwordField } from "./access-page.tsx";

const invalid = (location: string): Failure => ({
  kind: "problem",
  status: 400,
  code: "invalid_request",
  detail: "",
  fields: [{ location, message: "must have 10 to 128 characters." }],
  requestId: "",
});

describe("passwordField", () => {
  it("gives the rule that the sent password breaks", () => {
    expect(passwordField(invalid("body.password"), "short")).toBe("At least 10 characters.");
    expect(passwordField(invalid("body.password"), "a".repeat(129))).toBe(
      "At most 128 characters.",
    );
  });

  it("reads only the field that it names", () => {
    expect(passwordField(invalid("body.current_password"), "short")).toBeNull();
    expect(passwordField(invalid("body.new_password"), "short", "new_password")).toBe(
      "At least 10 characters.",
    );
  });

  it("gives null with no failure of the field", () => {
    expect(passwordField(null, "short")).toBeNull();
  });
});
