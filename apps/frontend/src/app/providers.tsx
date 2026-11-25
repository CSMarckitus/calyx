import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { BrowserRouter } from "react-router";
import { NoInlineStyles } from "@/components/csp-provider";
import { createHealth, installHealth } from "../api/health.ts";
import { createLive } from "../api/live.ts";
import { createQueryClient, createReads, installRefresh } from "../api/queries.ts";
import { createSession, type Session, SessionProvider } from "./session.tsx";

export type App = { client: ReturnType<typeof createQueryClient>; session: Session };

// The stores of the page. main.tsx makes them once, before the first render.
export function createApp(): App {
  const client = createQueryClient();
  const reads = createReads(client);
  const health = createHealth({ client });
  installHealth(health);
  // The session and the stream need each other: each one reaches the other through a function.
  const session = createSession({
    client,
    reads,
    live: { setUser: (value) => live.setUser(value) },
    reload: () => location.reload(),
    where: () => location,
  });
  const live = createLive({ reads, health, onUnauthorized: () => session.onUnauthorized() });
  installRefresh(() => live.refreshNow());
  session.watch();
  health.start();
  return { client, session };
}

export function Providers({ app, children }: { app: App; children: ReactNode }) {
  return (
    <NoInlineStyles>
      <QueryClientProvider client={app.client}>
        <BrowserRouter>
          <SessionProvider session={app.session}>{children}</SessionProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </NoInlineStyles>
  );
}
