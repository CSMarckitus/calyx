import { describe, expect, it } from "vitest";
import { autoLocale, countryChoices, joinLocale, languageChoices, splitLocale } from "./locale.ts";

describe("autoLocale", () => {
  it("gives the region of a language alone", () => {
    expect(autoLocale(["es"])).toBe("es-ES");
  });

  it("replaces a region that is not two letters", () => {
    expect(autoLocale(["es-419"])).toBe("es-ES");
  });

  it("keeps a region of two letters after a script", () => {
    expect(autoLocale(["zh-Hant-TW"])).toBe("zh-TW");
  });

  it("uses the first value", () => {
    expect(autoLocale(["es", "en-US"])).toBe("es-ES");
  });

  it("skips a language of three letters", () => {
    expect(autoLocale(["fil-PH", "pt-BR"])).toBe("pt-BR");
    expect(autoLocale(["fil"])).toBe("en-US");
  });

  it("skips a value that is not a language tag", () => {
    expect(autoLocale(["not a tag", "de"])).toBe("de-DE");
  });

  it("gives en-US with no value", () => {
    expect(autoLocale([])).toBe("en-US");
  });
});

describe("splitLocale and joinLocale", () => {
  it("split and join a locale", () => {
    expect(splitLocale("es-ES")).toEqual({ language: "es", region: "ES" });
    expect(joinLocale("pt", "BR")).toBe("pt-BR");
  });
});

describe("the choices", () => {
  it("name each language of two letters, and no other", () => {
    const list = languageChoices();
    expect(list).toHaveLength(183);
    expect(list.find((c) => c.code === "es")?.name).toBe("Spanish");
  });

  it("name each country with its dialling code, sorted by name", () => {
    const list = countryChoices();
    expect(list).toHaveLength(249);
    expect(list.find((c) => c.code === "IT")).toEqual({ code: "IT", name: "Italy", dial: "39" });
    const names = list.map((c) => c.name);
    expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b, "en")));
  });
});
