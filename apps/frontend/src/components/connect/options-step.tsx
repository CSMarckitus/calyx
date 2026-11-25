import { useId, useMemo } from "react";
import type { DeviceIdentity } from "../../domain/draft.ts";
import {
  autoLocale,
  countryChoices,
  joinLocale,
  languageChoices,
  splitLocale,
} from "../../domain/locale.ts";
import { Button } from "../ui/button.tsx";
import { Label } from "../ui/label.tsx";
import { Words } from "../words.tsx";

// A long list of the system is easier on a phone than a custom list, so these are native.
export const selectClass =
  "min-h-11 w-full min-w-0 rounded-lg border border-input bg-background px-2.5 text-base text-foreground";

export const devices: Record<DeviceIdentity, string> = {
  CHROME_WINDOWS: "Chrome (Windows)",
  EDGE_WINDOWS: "Edge (Windows)",
  FIREFOX_WINDOWS: "Firefox (Windows)",
  FIREFOX_LINUX: "Firefox (Linux)",
  SAFARI_MAC_OS: "Safari (macOS)",
};

const languageNames = new Intl.DisplayNames(["en"], { type: "language" });

// The value of the finished step "Optional settings".
export function optionsText(locale: string | null, device: DeviceIdentity | null): string {
  if (locale === null && device === null) return "Automatic";
  return [locale && (languageNames.of(locale) ?? locale), device && devices[device]]
    .filter(Boolean)
    .join(", ");
}

// "Optional settings": the language and the country that WhatsApp gets, and the name in the
// linked devices of the phone. null is the automatic value.
export function OptionsStep({
  locale,
  device,
  onChange,
  onContinue,
}: {
  locale: string | null;
  device: DeviceIdentity | null;
  onChange: (locale: string | null, device: DeviceIdentity | null) => void;
  onContinue: () => void;
}) {
  const auto = useMemo(() => splitLocale(autoLocale()), []);
  const shown = splitLocale(locale ?? joinLocale(auto.language, auto.region));
  const langs = useMemo(languageChoices, []);
  const countries = useMemo(countryChoices, []);
  const ids = { language: useId(), country: useId(), device: useId(), hint: useId() };
  const changed = locale !== null || device !== null;

  // The automatic language and country give null, so the pairing follows the browser.
  function setLocale(language: string, region: string) {
    const same = language === auto.language && region === auto.region;
    onChange(same ? null : joinLocale(language, region), device);
  }

  return (
    <div className="grid gap-4">
      <div className="grid gap-1.5 rounded-md bg-accent px-4 py-3.5 text-[0.9375rem]">
        <b className="font-semibold">You do not need to change anything here.</b>
        <span>
          <Words text="Calyx chooses these values for you. Leave them automatic if you are not sure. They do not change the risk of a ban." />
        </span>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="grid gap-2">
          <Label htmlFor={ids.language} className="text-base font-semibold">
            Language
          </Label>
          <select
            id={ids.language}
            className={selectClass}
            aria-describedby={ids.hint}
            value={shown.language}
            onChange={(e) => setLocale(e.target.value, shown.region)}
          >
            {langs.map((l) => (
              <option key={l.code} value={l.code}>
                {l.code === auto.language ? `Automatic: ${l.name}` : l.name}
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-2">
          <Label htmlFor={ids.country} className="text-base font-semibold">
            Country
          </Label>
          <select
            id={ids.country}
            className={selectClass}
            aria-describedby={ids.hint}
            value={shown.region}
            onChange={(e) => setLocale(shown.language, e.target.value)}
          >
            {countries.map((c) => (
              <option key={c.code} value={c.code}>
                {c.code === auto.region ? `Automatic: ${c.name}` : c.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <span id={ids.hint} className="text-[0.9375rem] text-muted-foreground">
        Automatic uses the values of your browser. WhatsApp gets them with the connection.
      </span>
      <div className="grid gap-2">
        <Label htmlFor={ids.device} className="text-base font-semibold">
          Name in the linked devices of the phone
        </Label>
        <select
          id={ids.device}
          className={selectClass}
          value={device ?? ""}
          onChange={(e) => onChange(locale, (e.target.value || null) as DeviceIdentity | null)}
        >
          <option value="">Automatic</option>
          {(Object.keys(devices) as DeviceIdentity[]).map((d) => (
            <option key={d} value={d}>
              {devices[d]}
            </option>
          ))}
        </select>
        <span className="text-[0.9375rem] text-muted-foreground">
          The phone shows it under Linked devices.
        </span>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button onClick={onContinue}>{changed ? "Continue" : "Continue with automatic"}</Button>
        {changed && (
          <Button variant="outline" onClick={() => onChange(null, null)}>
            Back to automatic
          </Button>
        )}
      </div>
    </div>
  );
}
