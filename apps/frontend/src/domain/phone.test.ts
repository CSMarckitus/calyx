import { describe, expect, it } from "vitest";
import { fullNumber, isPhoneValid, showNumber } from "./phone.ts";

describe("fullNumber", () => {
  it("reads a number that starts with + as complete", () => {
    expect(fullNumber("+34 600 12 34 56", "39")).toBe("34600123456");
  });

  it("reads a number that starts with 00 as complete", () => {
    expect(fullNumber("0034 600-12-34-56", "39")).toBe("34600123456");
  });

  it("puts the code of the selected country before any other number", () => {
    // The first digits are equal to the code of Italy, but they are part of the number.
    expect(fullNumber("391 234 5678", "39")).toBe("393912345678");
  });

  it("keeps a first 0, because some countries need it", () => {
    expect(fullNumber("06 1234 5678", "39")).toBe("390612345678");
  });

  it("ignores spaces before the + or the 00", () => {
    expect(fullNumber("  +34 600", "39")).toBe("34600");
  });

  it("gives an empty number for a text with no digits", () => {
    expect(fullNumber("", "39")).toBe("");
    expect(fullNumber("+", "39")).toBe("");
  });
});

describe("isPhoneValid", () => {
  it("accepts 7 to 15 digits", () => {
    expect(isPhoneValid("1234567")).toBe(true);
    expect(isPhoneValid("123456789012345")).toBe(true);
    expect(isPhoneValid("123456")).toBe(false);
    expect(isPhoneValid("1234567890123456")).toBe(false);
  });
});

describe("showNumber", () => {
  it("shows the complete number with a +", () => {
    expect(showNumber("34600123456")).toBe("+34600123456");
  });
});
