import { Moon, Sun } from "lucide-react";
import { useState } from "react";
import { Button } from "./ui/button.tsx";

const key = "calyx-theme";

// The theme that shows now: the choice of the person, or the one of the system.
export function isDark(): boolean {
  const chosen = document.documentElement.dataset.theme;
  if (chosen) return chosen === "dark";
  return typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches;
}

// Same key as public/theme.js, which sets the theme before the first paint.
export function ThemeButton({ onChange }: { onChange?: (dark: boolean) => void }) {
  const [dark, setDark] = useState(isDark);

  function toggle() {
    const next = dark ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(key, next);
    } catch {
      // Storage is blocked: the choice lasts until the page closes.
    }
    setDark(!dark);
    onChange?.(!dark);
  }

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      onClick={toggle}
      aria-label={dark ? "Use the light theme" : "Use the dark theme"}
    >
      {dark ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}
    </Button>
  );
}
