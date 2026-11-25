import type { QueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useContext, useEffect } from "react";
import { useNavigate } from "react-router";
import { keys } from "../api/queries.ts";
import { type Failure, request } from "../api/request.ts";
import { clearDrafts } from "../domain/draft.ts";

// Paths for a person with no login. They never redirect for a 401, and are never a `next`.
const publicPaths = new Set(["/login", "/forgot-password", "/invite", "/reset-password"]);

export const isPublic = (pathname: string) => publicPaths.has(pathname);

// `next` must be a path of this origin, so the login cannot send a person to another site.
export function safeNext(next: string | null, origin: string): string {
  if (!next?.startsWith("/")) return "/";
  let url: URL;
  try {
    url = new URL(next, origin);
  } catch {
    return "/";
  }
  if (url.origin !== origin || isPublic(url.pathname)) return "/";
  return url.pathname + url.search + url.hash;
}

// Pages that nginx serves outside the panel. Same rule as nginx.conf and `docsPath` in
// vite.config.ts. They need a full page load, because the router does not own them.
export function outsidePanel(path: string): boolean {
  return /^\/docs(\/|\/index\.html)?$/.test(path.replace(/[?#].*$/, ""));
}

type Where = { pathname: string; search: string };

export function loginPath(where: Where): string {
  if (isPublic(where.pathname)) return "/login";
  return `/login?next=${encodeURIComponent(where.pathname + where.search)}`;
}

export const isUnauthorized = (error: unknown) => {
  const f = error as Failure | null;
  return f?.kind === "problem" && f.status === 401;
};

type Go = (to: string) => void;

type Options = {
  client: QueryClient;
  reads: { clear(): void };
  live: { setUser(value: boolean): void };
  reload: () => void;
  where: () => Where;
  logout?: () => ReturnType<typeof request<"logout">>;
};

export type Session = ReturnType<typeof createSession>;

// The login session. The page has a user only after a getMe that succeeded.
export function createSession({
  client,
  reads,
  live,
  reload,
  where,
  logout = () => request("logout", {}),
}: Options) {
  let userId: string | null = null;
  let go: Go = (to) => window.location.assign(to);

  function onMe(me: { user: { id: string } }) {
    if (userId === null) {
      userId = me.user.id;
      live.setUser(true);
    } else if (userId !== me.user.id) {
      // Another tab logged in as another person. A reload drops every piece of the old data.
      reload();
    }
  }

  // No data of a person stays for the next one.
  function end(to: string) {
    userId = null;
    live.setUser(false);
    reads.clear();
    clearDrafts();
    go(to);
  }

  function onUnauthorized() {
    if (userId === null) return;
    end(loginPath(where()));
  }

  return {
    hasUser: () => userId !== null,
    setGo(next: Go) {
      go = next;
    },
    onUnauthorized,
    // Returns null when the login session ended, or the failure when it continues.
    async logout(): Promise<Failure | null> {
      const outcome = await logout();
      if (!outcome.ok && !isUnauthorized(outcome.failure)) return outcome.failure;
      end("/login");
      return null;
    },
    watch() {
      const stopQueries = client.getQueryCache().subscribe((event) => {
        if (event.type !== "updated") return;
        const { action, query } = event;
        if (action.type === "success" && query.queryKey[0] === keys.me[0]) {
          onMe(action.data as { user: { id: string } });
        } else if (action.type === "error" && isUnauthorized(action.error)) {
          onUnauthorized();
        }
      });
      // The 401 of `login` is the answer to a wrong password, not the end of a login session.
      const stopChanges = client.getMutationCache().subscribe((event) => {
        if (event.type !== "updated" || event.action.type !== "error") return;
        if (event.mutation.options.mutationKey?.[0] === "login") return;
        if (isUnauthorized(event.action.error)) onUnauthorized();
      });
      return () => {
        stopQueries();
        stopChanges();
      };
    },
  };
}

const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const session = useContext(SessionContext);
  if (!session) throw new Error("useSession needs a SessionProvider");
  return session;
}

// Gives the session the navigation of the router, so its end does not load the page again.
export function SessionProvider({ session, children }: { session: Session; children: ReactNode }) {
  const navigate = useNavigate();
  useEffect(() => {
    session.setGo((to) => void navigate(to, { replace: true }));
  }, [session, navigate]);
  return <SessionContext value={session}>{children}</SessionContext>;
}
