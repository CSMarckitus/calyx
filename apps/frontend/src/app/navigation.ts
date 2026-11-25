import type { Viewer } from "@/components/health-card";

export type Entry = { label: string; path: string; outside: boolean };
export type Group = { label: string; entries: Entry[] };

const entry = (label: string, path: string, outside = false): Entry => ({ label, path, outside });

export function groupsFor(viewer: Viewer): Group[] {
  const owner = viewer === "owner";
  const groups: Group[] = [
    { label: "Instances", entries: [entry(owner ? "Instances" : "My WhatsApp", "/")] },
    {
      label: "Developers",
      entries: [
        entry("API keys", "/developers/api-keys"),
        entry("Connect a program", "/developers/connect"),
        entry("API docs", "/docs", true),
      ],
    },
  ];
  if (owner) {
    groups.push({
      label: "Organization",
      entries: [
        entry("Organization", "/organization"),
        entry("WhatsApp Web", "/organization/whatsapp-web"),
      ],
    });
  }
  groups.push({ label: "Account", entries: [entry("Account", "/account")] });
  return groups;
}

// The instance and client screens belong to the Instances entry. Other paths
// go to the entry with the longest path that is a prefix of them.
export function activeEntry(pathname: string, groups: Group[]): string | null {
  if (pathname === "/" || pathname.startsWith("/instances/") || pathname.startsWith("/clients/")) {
    return "/";
  }
  let best: string | null = null;
  for (const group of groups) {
    for (const { path } of group.entries) {
      const matches = path !== "/" && (pathname === path || pathname.startsWith(`${path}/`));
      if (matches && (best === null || path.length > best.length)) best = path;
    }
  }
  return best;
}
