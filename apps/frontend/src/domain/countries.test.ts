import { describe, expect, it } from "vitest";
import { dialCode, languages, regions } from "./countries.ts";

describe("countries", () => {
  it("has every code of ISO 3166-1 and of ISO 639-1", () => {
    expect(regions).toHaveLength(249);
    expect(languages).toHaveLength(183);
    expect(regions).toContain("ES");
    expect(languages).toContain("es");
    expect(languages).not.toContain("fil");
  });

  it("gives the dialling code of a country", () => {
    expect(dialCode("ES")).toBe("34");
    expect(dialCode("IT")).toBe("39");
    expect(dialCode("US")).toBe("1");
    expect(dialCode("CA")).toBe("1");
  });

  it("gives an empty code for a country with no dialling code, or an unknown one", () => {
    expect(dialCode("AQ")).toBe("");
    expect(dialCode("ZZ")).toBe("");
  });
});
