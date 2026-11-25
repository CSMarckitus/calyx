import "../styles.css";
// After the styles of Calyx, so the rules of Scalar come after the preflight of Tailwind.
import "@scalar/api-reference-react/style.css";
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { NoInlineStyles } from "@/components/csp-provider";
import { createQueryClient } from "../api/queries.ts";
import { DocsPage } from "./docs-page.tsx";
import { reloadOnce } from "./preload.ts";

// A chunk of an old build is gone after a new release: reload one time to get the new build.
window.addEventListener("vite:preloadError", () => {
  let storage: Storage;
  try {
    storage = sessionStorage;
  } catch {
    return;
  }
  reloadOnce(storage, () => location.reload());
});

// The docs page has no router, no session, no health check and no live stream: they make requests.
const root = document.getElementById("root");
if (!root) throw new Error("docs/index.html has no #root");

createRoot(root).render(
  <StrictMode>
    <NoInlineStyles>
      <QueryClientProvider client={createQueryClient()}>
        <DocsPage />
      </QueryClientProvider>
    </NoInlineStyles>
  </StrictMode>,
);
