import { type ReactNode, useId, useState } from "react";
import { Link, useLocation } from "react-router";
import type { Viewer } from "@/components/health-card";
import { HealthRegion, HealthStatus } from "@/components/health-status";
import { meta, NewVersion } from "@/components/new-version";
import { Notice } from "@/components/notice";
import { TermsNotice } from "@/components/terms-notice";
import { ThemeButton } from "@/components/theme-button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { Wordmark } from "@/components/wordmark";
import { cn } from "@/lib/utils";
import { useHealth } from "../api/health.ts";
import { outage } from "../domain/outage.ts";
import { Boundary } from "./boundary.tsx";
import { activeEntry, groupsFor } from "./navigation.ts";

function OutageNotice({ viewer, manager }: { viewer: Viewer; manager: string }) {
  const health = useHealth();
  const found = outage(health, viewer, manager, Date.now());
  if (!found) return null;
  return (
    <Notice tone="bad" title={found.title} keep={[manager]}>
      {found.text}
    </Notice>
  );
}

const entryClass =
  "flex min-h-11 items-center rounded-md px-3 font-medium text-muted-foreground hover:text-primary aria-[current=page]:bg-accent aria-[current=page]:font-semibold aria-[current=page]:text-foreground";

function Logo() {
  return (
    <Link to="/" className="flex min-h-11 items-center gap-2.5 px-1 text-foreground">
      <img src="/calyx-logo.svg" alt="" className="h-6.5 w-7" />
      <Wordmark className="h-6 w-auto" />
    </Link>
  );
}

// The navigation, the health status and the theme button. The layout renders it twice: in
// the left column from `sm`, and in the Sheet at the phone width. Only one of the two is visible, so only
// one is in the accessibility tree.
function Sidebar({
  viewer,
  pathname,
  onSelect,
}: {
  viewer: Viewer;
  pathname: string;
  onSelect?: () => void;
}) {
  const id = useId();
  const groups = groupsFor(viewer);
  const active = activeEntry(pathname, groups);
  return (
    <div className="flex flex-1 flex-col gap-6">
      <nav aria-label="Main" className="grid gap-2">
        {groups.map((group, i) => {
          // A group with one entry has no heading.
          const heading = group.entries.length > 1 ? `${id}-${i}` : undefined;
          return (
            <div key={group.label} className={cn("grid gap-1", heading && "mt-4")}>
              {heading && (
                <p
                  id={heading}
                  className="px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
                >
                  {group.label}
                </p>
              )}
              {/* The entries of a group are indented, with a thin line on their left. */}
              <ul
                aria-labelledby={heading}
                className={cn("grid", heading && "ml-3 border-l border-border pl-2")}
              >
                {group.entries.map(({ label, path, outside }) => (
                  <li key={path}>
                    {outside ? (
                      // A page that is not a route of the panel: the browser loads it, with its own CSP.
                      <a href={path} className={entryClass}>
                        {label}
                      </a>
                    ) : (
                      <Link
                        to={path}
                        onClick={onSelect}
                        aria-current={active === path ? "page" : undefined}
                        className={entryClass}
                      >
                        {label}
                      </Link>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
      </nav>
      <div className="mt-auto grid gap-1">
        <HealthStatus viewer={viewer} />
        <div className="flex items-center justify-between gap-2 pl-3">
          {/* The name and the version stay as they are. */}
          <p translate="no" className="text-xs text-muted-foreground">
            Calyx {meta("calyx-version") ?? "dev"}
          </p>
          <ThemeButton />
        </div>
      </div>
    </div>
  );
}

// The button "Menu" opens the sidebar from the right side at the phone width.
function MenuSheet({ viewer, pathname }: { viewer: Viewer; pathname: string }) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger className="inline-flex min-h-11 items-center px-0.5 font-medium text-muted-foreground hover:text-primary">
        Menu
      </SheetTrigger>
      <SheetContent side="right" className="overflow-y-auto pb-4">
        <SheetHeader>
          <SheetTitle>Menu</SheetTitle>
        </SheetHeader>
        <div className="flex flex-1 flex-col px-1">
          <Sidebar viewer={viewer} pathname={pathname} onSelect={() => setOpen(false)} />
        </div>
      </SheetContent>
    </Sheet>
  );
}

// The frame of every screen of a logged-in person. From `sm` there is no top bar: the sidebar holds the
// logo, the health status and the theme button. At the phone width a bar holds the logo and "Menu".
export function Layout({
  viewer,
  manager,
  terms,
  children,
}: {
  viewer: Viewer;
  manager: string;
  // The owner has not accepted the terms of WhatsApp Web yet. `blocked`: the server admin blocked
  // WhatsApp Web.
  terms: { blocked: boolean } | null;
  children: ReactNode;
}) {
  const { pathname } = useLocation();
  // The list and its drawers are one screen: a drawer must not mount the list again.
  const screen = pathname === "/" || pathname.startsWith("/instances/") ? "/" : pathname;
  return (
    <div className="flex min-h-dvh flex-col sm:flex-row">
      {/* One live region at each width: the Sheet is not in the page while it is closed. */}
      <HealthRegion viewer={viewer} />
      <header className="flex items-center gap-x-3 border-b border-border bg-card px-4 py-1 sm:hidden">
        <Logo />
        <div className="ml-auto">
          <MenuSheet viewer={viewer} pathname={pathname} />
        </div>
      </header>
      {/* The column grows with the screen, and the sidebar in it stays in view. */}
      <aside className="w-60 shrink-0 border-r border-border bg-card max-sm:hidden">
        <div
          data-slot="sidebar"
          className="sticky top-0 flex h-dvh flex-col overflow-y-auto px-3 pt-4 pb-3"
        >
          <div className="mb-4">
            <Logo />
          </div>
          <Sidebar viewer={viewer} pathname={pathname} />
        </div>
      </aside>
      <main className="mx-auto grid w-full min-w-0 max-w-[1120px] flex-1 content-start gap-6 px-4 pt-5 pb-14 sm:px-7 sm:pt-8">
        <NewVersion />
        <OutageNotice viewer={viewer} manager={manager} />
        {/* It stays on each screen until the acceptance, except on the terms themselves
            and on the Instances screen, which shows the same notice in place of the list. */}
        {terms && screen !== "/organization/whatsapp-web" && screen !== "/" && (
          <div data-slot="terms-banner">
            <TermsNotice blocked={terms.blocked} />
          </div>
        )}
        {/* A new path gets a new boundary, so one stopped screen does not stop the next one. */}
        <Boundary key={screen}>{children}</Boundary>
      </main>
    </div>
  );
}
