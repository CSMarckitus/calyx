import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { type Health, type HealthStore, installHealth } from "../api/health.ts";
import { serviceRows, versionRows } from "./health-card.tsx";
import { HealthRegion, HealthStatus, headerText } from "./health-status.tsx";

const base: Health = {
  state: "checking",
  dataIsFresh: true,
  database: null,
  core: null,
  mail: null,
  version: "",
  coreVersion: "",
};
const ok: Health = {
  ...base,
  state: "ok",
  database: "ok",
  core: "ok",
  mail: "configured",
  lastAnswerAt: 1000,
  lastGoodAt: 1000,
};
const coreStopped: Health = { ...ok, state: "core", core: "unreachable" };
const noAnswer: Health = { ...base, state: "backend", lastGoodAt: 1000 };

function fakeStore(snapshot: Health): HealthStore {
  return {
    getSnapshot: () => snapshot,
    subscribe: () => () => undefined,
    onCheck: () => () => undefined,
    start() {},
    stop() {},
    checkNow() {},
    requestUnreachable() {},
  };
}

afterEach(cleanup);

describe("headerText", () => {
  it.each([
    ["backend", "Calyx does not answer", "Calyx does not answer"],
    ["database", "Database stopped", "Calyx does not answer"],
    ["core", "Core stopped", "Calyx cannot reach WhatsApp"],
    ["ok", "Backend and core fully operational", "Calyx is fully operational"],
    ["checking", "Checking Calyx", "Checking Calyx"],
  ] as const)("state %s", (state, owner, client) => {
    expect(headerText(state, "owner")).toBe(owner);
    expect(headerText(state, "client")).toBe(client);
  });
});

describe("serviceRows", () => {
  const pairs = (h: Health, viewer: "owner" | "client") =>
    serviceRows(h, viewer).map((r) => [r.name, r.value]);

  it("shows four services to the owner and two to the client user", () => {
    expect(pairs(ok, "owner")).toEqual([
      ["Backend", "Operational"],
      ["Core", "Operational"],
      ["Database", "Operational"],
      ["Mail", "Configured"],
    ]);
    expect(pairs(ok, "client")).toEqual([
      ["Calyx", "Operational"],
      ["WhatsApp service", "Operational"],
    ]);
  });

  it("shows a stopped core", () => {
    expect(pairs(coreStopped, "owner")[1]).toEqual(["Core", "Stopped"]);
    expect(pairs(coreStopped, "client")).toEqual([
      ["Calyx", "Operational"],
      ["WhatsApp service", "Stopped"],
    ]);
  });

  it("says No answer for the backend and Unknown for the rest", () => {
    expect(pairs(noAnswer, "owner")).toEqual([
      ["Backend", "No answer"],
      ["Core", "Unknown"],
      ["Database", "Unknown"],
      ["Mail", "Unknown"],
    ]);
    expect(pairs(noAnswer, "client")).toEqual([
      ["Calyx", "No answer"],
      ["WhatsApp service", "Unknown"],
    ]);
  });

  it("shows a stopped database", () => {
    const h: Health = { ...ok, state: "database", database: "unavailable", core: "unknown" };
    expect(pairs(h, "owner")[2]).toEqual(["Database", "Stopped"]);
    expect(pairs(h, "owner")[1]).toEqual(["Core", "Unknown"]);
  });

  it("says Checking before the first answer", () => {
    expect(pairs(base, "owner").map(([, v]) => v)).toEqual([
      "Checking",
      "Checking",
      "Checking",
      "Checking",
    ]);
  });

  it("shows mail that is off", () => {
    expect(pairs({ ...ok, mail: "off" }, "owner")[3]).toEqual(["Mail", "Off"]);
  });
});

describe("versionRows", () => {
  afterEach(() => document.head.querySelector('meta[name="calyx-version"]')?.remove());

  it("shows the unknown version of each part", () => {
    expect(versionRows(base).map((r) => [r.name, r.value])).toEqual([
      ["Panel", "unknown"],
      ["Backend", "unknown"],
      ["Core", "unknown"],
    ]);
  });

  it("lists the Panel, the Backend and the Core, in this order", () => {
    const tag = document.createElement("meta");
    tag.name = "calyx-version";
    tag.content = "0.1.0";
    document.head.append(tag);
    expect(
      versionRows({ ...base, version: "0.1.1", coreVersion: "0.1.2" }).map((r) => [
        r.name,
        r.value,
      ]),
    ).toEqual([
      ["Panel", "0.1.0"],
      ["Backend", "0.1.1"],
      ["Core", "0.1.2"],
    ]);
  });
});

describe("HealthStatus", () => {
  it("names the button with the state, and hides the seconds from a screen reader", () => {
    installHealth(fakeStore(ok));
    render(<HealthStatus viewer="owner" />);
    const button = screen.getByRole("button", { name: /^Backend and core fully operational/ });
    expect(button.textContent).toContain("checked");
    expect(button.querySelector("[aria-hidden='true']")?.textContent).toMatch(/^checked /);
    expect(button.getAttribute("aria-label")).toBeNull();
  });

  it("has no live region", () => {
    installHealth(fakeStore(ok));
    render(<HealthStatus viewer="owner" />);
    expect(screen.queryByRole("status")).toBeNull();
  });
});

describe("HealthRegion", () => {
  it("has a live region with only the state text, and it follows the state", () => {
    installHealth(fakeStore(ok));
    const { rerender } = render(<HealthRegion viewer="owner" />);
    const region = screen.getByRole("status");
    expect(region.textContent).toBe("Backend and core fully operational");
    installHealth(fakeStore(coreStopped));
    rerender(<HealthRegion viewer="owner" />);
    expect(screen.getByRole("status").textContent).toBe("Core stopped");
  });

  it("opens the card on focus and closes it on Escape", async () => {
    installHealth(fakeStore(noAnswer));
    render(<HealthStatus viewer="client" />);
    // "Calyx" has an element of its own. jsdom joins inline elements with no space in a
    // name; Chromium keeps the space (outage.spec.ts).
    const button = screen.getByRole("button", { name: /^Calyx ?does not answer/ });
    expect(document.querySelector('[data-slot="health-card"]')).toBeNull();
    await act(async () => button.focus());
    const card = document.querySelector('[data-slot="health-card"]');
    expect(card).not.toBeNull();
    expect(card?.textContent).toContain("Last good check at");
    expect(card?.querySelectorAll('[data-slot="health-service"]')).toHaveLength(2);
    await act(async () => {
      fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    });
    await waitFor(() => expect(document.querySelector('[data-slot="health-card"]')).toBeNull());
  });

  it("shows no footer time in the state ok with no good check", async () => {
    installHealth(fakeStore({ ...ok, lastGoodAt: undefined }));
    render(<HealthStatus viewer="owner" />);
    await act(async () => screen.getByRole("button").focus());
    const card = document.querySelector('[data-slot="health-card"]');
    expect(card?.textContent).not.toContain("Trying again");
    expect(card?.textContent).not.toContain("Last good check");
    expect(card?.textContent).not.toContain("checked");
  });

  it("shows no time when there was no good check", async () => {
    installHealth(fakeStore({ ...noAnswer, lastGoodAt: undefined }));
    render(<HealthStatus viewer="owner" />);
    await act(async () => screen.getByRole("button").focus());
    const card = document.querySelector('[data-slot="health-card"]');
    expect(card?.textContent).toContain("Trying again by itself.");
    expect(card?.textContent).not.toContain("Last good check");
  });
});
