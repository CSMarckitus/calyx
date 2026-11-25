import { useEffect, useState } from "react";
import { Notice } from "./notice.tsx";
import { Button } from "./ui/button.tsx";

type Build = { version: string; build: string };

export const meta = (name: string) =>
  document.querySelector<HTMLMetaElement>(`meta[name="${name}"]`)?.content;

// The build of this page (the build step puts it in two meta tags). Absent in the dev server.
function mine(): Build | null {
  const version = meta("calyx-version");
  const build = meta("calyx-build");
  return version && build ? { version, build } : null;
}

async function latest(): Promise<Build | null> {
  try {
    const res = await fetch("/version.json", { cache: "no-store" });
    const body: unknown = await res.json();
    const b = body as Partial<Build> | null;
    return typeof b?.version === "string" && typeof b.build === "string"
      ? { version: b.version, build: b.build }
      : null;
  } catch {
    return null;
  }
}

// Asks for `version.json` when the page becomes visible. A different build shows the notice.
export function NewVersion() {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const own = mine();
    if (!own) return;
    let live = true;
    async function onVisible() {
      if (document.visibilityState !== "visible") return;
      const found = await latest();
      if (live && found && (found.build !== own?.build || found.version !== own?.version))
        setReady(true);
    }
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      live = false;
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  if (!ready) return null;
  return (
    <Notice tone="brand" title="A new version is ready.">
      <Button type="button" variant="outline" onClick={() => location.reload()}>
        Reload
      </Button>
    </Notice>
  );
}
