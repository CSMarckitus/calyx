import "./styles.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createApp, Providers } from "./app/providers.tsx";
import { AppRoutes } from "./app/router.tsx";

const root = document.getElementById("root");
if (!root) throw new Error("index.html has no #root");

const app = createApp();
createRoot(root).render(
  <StrictMode>
    <Providers app={app}>
      <AppRoutes />
    </Providers>
  </StrictMode>,
);
