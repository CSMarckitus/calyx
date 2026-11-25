import { describe, expect, it } from "vitest";
import type { Health, HealthState } from "../api/health.ts";
import { outage } from "./outage.ts";

const zone = "Europe/Madrid";
const now = Date.parse("2026-11-23T14:00:00Z"); // 15:00 in Madrid
const today = Date.parse("2026-11-23T09:42:00Z"); // 10:42 in Madrid
const yesterday = Date.parse("2026-11-22T09:42:00Z");
const earlier = Date.parse("2026-11-20T09:42:00Z");

const health = (state: HealthState, lastGoodAt?: number): Health => ({
  state,
  dataIsFresh: false,
  database: null,
  core: null,
  mail: null,
  version: "",
  coreVersion: "",
  ...(lastGoodAt !== undefined ? { lastGoodAt } : {}),
});
const say = (state: HealthState, viewer: "owner" | "client", lastGoodAt?: number) =>
  outage(health(state, lastGoodAt), viewer, "Acme", now, zone);

describe("outage", () => {
  it.each(["ok", "checking"] as const)("has no notice in the state %s", (state) => {
    expect(say(state, "owner", today)).toBeNull();
    expect(say(state, "client", today)).toBeNull();
  });

  it("core, owner", () => {
    expect(say("core", "owner", today)).toEqual({
      title: "The core does not answer.",
      text: "No message can go out and no number can be linked. Check that the core runs, and read its logs.",
    });
  });

  it("core, client user: names the manager, and has no time", () => {
    expect(say("core", "client", today)).toEqual({
      title: "Calyx cannot reach WhatsApp now.",
      text: "Messages cannot go out. Contact Acme. The states below can be old.",
    });
  });

  it("backend, owner", () => {
    expect(say("backend", "owner", today)).toEqual({
      title: "Calyx does not answer.",
      text: "Check that the backend runs. This page tries again by itself. The states below are from 10:42.",
    });
  });

  it("backend, client user", () => {
    expect(say("backend", "client", today)).toEqual({
      title: "Calyx does not answer.",
      text: "This page tries again by itself. If it continues, contact Acme. The states below are from 10:42.",
    });
  });

  it("database, owner: the provisional text", () => {
    expect(say("database", "owner", today)).toEqual({
      title: "The database does not answer.",
      text: "Check that PostgreSQL runs. This page tries again by itself. The states below are from 10:42.",
    });
  });

  it("database, client user: the same notice as backend", () => {
    expect(say("database", "client", today)).toEqual(say("backend", "client", today));
  });

  it.each([
    ["backend", "owner"],
    ["backend", "client"],
    ["database", "owner"],
  ] as const)("%s, %s with no lastGoodAt: no time", (state, viewer) => {
    const text = say(state, viewer)?.text ?? "";
    expect(text).not.toContain("The states below");
    expect(text.endsWith("by itself.") || text.endsWith("contact Acme.")).toBe(true);
  });

  it("a lastGoodAt of yesterday reads as a sentence", () => {
    expect(say("backend", "owner", yesterday)?.text).toBe(
      "Check that the backend runs. This page tries again by itself. The states below are from yesterday at 10:42.",
    );
  });

  it("a lastGoodAt of an earlier day gives the date", () => {
    expect(say("backend", "client", earlier)?.text).toBe(
      "This page tries again by itself. If it continues, contact Acme. The states below are from 20 November at 10:42.",
    );
  });
});
