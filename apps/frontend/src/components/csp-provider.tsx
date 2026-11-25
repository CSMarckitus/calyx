import { CSPProvider } from "@base-ui/react/csp-provider";
import type { ReactNode } from "react";

// The CSP has no 'unsafe-inline' for styles, so Base UI must not inject <style> elements.
// styles.css holds the rules that it would inject.
export function NoInlineStyles({ children }: { children: ReactNode }) {
  return <CSPProvider disableStyleElements>{children}</CSPProvider>;
}
