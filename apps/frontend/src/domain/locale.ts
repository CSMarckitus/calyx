import { dialCode, languages, regions } from "./countries.ts";

const twoLetters = /^[a-z]{2}$/i;
const fallback = "en-US";

// The locale that a pairing sends when the person changes nothing. The core
// accepts only a language of two letters and a region of two letters.
export function autoLocale(preferred: readonly string[] = navigator.languages): string {
  for (const tag of preferred) {
    let locale: Intl.Locale;
    try {
      locale = new Intl.Locale(tag);
    } catch {
      continue;
    }
    if (!twoLetters.test(locale.language)) continue;
    // A region such as 419 (Latin America) is not a country, so the language decides it.
    const region =
      locale.region && twoLetters.test(locale.region)
        ? locale.region
        : new Intl.Locale(locale.language).maximize().region;
    if (region && twoLetters.test(region)) return joinLocale(locale.language, region);
  }
  return fallback;
}

export const joinLocale = (language: string, region: string) => `${language}-${region}`;

export function splitLocale(locale: string): { language: string; region: string } {
  const [language = "", region = ""] = locale.split("-");
  return { language, region };
}

export type Choice = { code: string; name: string };

// The page is in English only, so the names are in English.
const byName = (a: Choice, b: Choice) => a.name.localeCompare(b.name, "en");

export function languageChoices(): Choice[] {
  const names = new Intl.DisplayNames(["en"], { type: "language" });
  return languages.map((code) => ({ code, name: names.of(code) ?? code })).sort(byName);
}

export function countryChoices(): (Choice & { dial: string })[] {
  const names = new Intl.DisplayNames(["en"], { type: "region" });
  return regions
    .map((code) => ({ code, name: names.of(code) ?? code, dial: dialCode(code) }))
    .sort(byName);
}
