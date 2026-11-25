import { describe, expect, it } from "vitest";
import { activeEntry, groupsFor } from "./navigation";

function summary(viewer: "owner" | "client") {
  return groupsFor(viewer).map((g) => [
    g.label,
    g.entries.map((e) => [e.label, e.path, e.outside]),
  ]);
}

describe("groupsFor", () => {
  it("gives the owner four groups in order", () => {
    expect(summary("owner")).toEqual([
      ["Instances", [["Instances", "/", false]]],
      [
        "Developers",
        [
          ["API keys", "/developers/api-keys", false],
          ["Connect a program", "/developers/connect", false],
          ["API docs", "/docs", true],
        ],
      ],
      [
        "Organization",
        [
          ["Organization", "/organization", false],
          ["WhatsApp Web", "/organization/whatsapp-web", false],
        ],
      ],
      ["Account", [["Account", "/account", false]]],
    ]);
  });

  it("gives the client user three groups and no Organization", () => {
    expect(summary("client")).toEqual([
      ["Instances", [["My WhatsApp", "/", false]]],
      [
        "Developers",
        [
          ["API keys", "/developers/api-keys", false],
          ["Connect a program", "/developers/connect", false],
          ["API docs", "/docs", true],
        ],
      ],
      ["Account", [["Account", "/account", false]]],
    ]);
  });
});

describe("activeEntry", () => {
  const groups = groupsFor("owner");
  it.each([
    ["/", "/"],
    ["/instances/x/view", "/"],
    ["/clients/x", "/"],
    ["/developers/api-keys", "/developers/api-keys"],
    ["/organization", "/organization"],
    ["/organization/whatsapp-web", "/organization/whatsapp-web"],
    ["/nothing", null],
  ])("%s gives %s", (pathname, expected) => {
    expect(activeEntry(pathname, groups)).toBe(expected);
  });
});
