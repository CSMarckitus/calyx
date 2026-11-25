import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { keys } from "../api/queries.ts";
import type { Failure } from "../api/request.ts";
import { newDraft, readDraft, saveDraft } from "../domain/draft.ts";
import { createSession, isPublic, loginPath, outsidePanel, safeNext } from "./session.tsx";

const origin = "https://calyx.example";

describe("safeNext", () => {
  it("accepts a path of the same origin", () => {
    expect(safeNext("/instances/7?x=1", origin)).toBe("/instances/7?x=1");
  });

  it.each([
    null,
    "",
    "https://evil.example/",
    "//evil.example/x",
    "/\\evil.example",
    "javascript:alert(1)",
    "instances",
    "https://calyx.example/account", // same origin, but not a path
  ])("gives / for %j", (next) => {
    expect(safeNext(next, origin)).toBe("/");
  });

  it("never gives a public path", () => {
    expect(safeNext("/login?next=/x", origin)).toBe("/");
    expect(safeNext("/invite?token=abc", origin)).toBe("/");
  });
});

describe("outsidePanel", () => {
  it.each(["/docs", "/docs/", "/docs/index.html", "/docs?x=1", "/docs#x", "/docs/?x=1#y"])(
    "%s is the docs page",
    (p) => {
      expect(outsidePanel(p)).toBe(true);
    },
  );
  it.each(["/", "/account", "/instances/1?next=/docs", "/docsx", "/docs/x", "/x/docs"])(
    "%s is a path of the panel",
    (p) => {
      expect(outsidePanel(p)).toBe(false);
    },
  );
});

describe("isPublic", () => {
  it.each(["/login", "/forgot-password", "/invite", "/reset-password"])("%s is public", (p) => {
    expect(isPublic(p)).toBe(true);
  });
  it.each(["/", "/account", "/instances/1", "/login/x", "/loginx"])("%s is not public", (p) => {
    expect(isPublic(p)).toBe(false);
  });
});

describe("loginPath", () => {
  it("keeps the path and the query in next", () => {
    expect(loginPath({ pathname: "/instances/7", search: "?a=1&b=2" })).toBe(
      `/login?next=${encodeURIComponent("/instances/7?a=1&b=2")}`,
    );
  });
  it("has no next for a public path", () => {
    expect(loginPath({ pathname: "/invite", search: "?token=abc" })).toBe("/login");
  });
});

const unauthorized: Failure = {
  kind: "problem",
  status: 401,
  code: "unauthenticated",
  detail: "",
  fields: [],
  requestId: "r1",
};
const me = (id: string) => ({ user: { id, role: "owner" } });

function setup(where = { pathname: "/instances/7", search: "?x=1" }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const reads = { clear: vi.fn(() => client.clear()) };
  const live = { setUser: vi.fn() };
  const go = vi.fn();
  const reload = vi.fn();
  const logout = vi.fn();
  const session = createSession({ client, reads, live, reload, where: () => where, logout });
  session.setGo(go);
  const stop = session.watch();
  return { client, reads, live, go, reload, logout, session, stop };
}

// Runs a read of `key` that gives `result`, through the query cache, as a screen does.
async function read(client: QueryClient, key: readonly unknown[], result: unknown | Failure) {
  await client
    .fetchQuery({
      queryKey: key,
      queryFn: () => {
        if (result && typeof result === "object" && "kind" in result) throw result;
        return result;
      },
      staleTime: 0,
    })
    .catch(() => undefined);
}

describe("createSession", () => {
  let s: ReturnType<typeof setup>;
  beforeEach(() => {
    s = setup();
  });
  afterEach(() => s.stop());

  it("has a user after the first getMe that succeeded, and opens the stream", async () => {
    expect(s.session.hasUser()).toBe(false);
    await read(s.client, keys.me, me("u1"));
    expect(s.session.hasUser()).toBe(true);
    expect(s.live.setUser).toHaveBeenCalledWith(true);
  });

  it("with no user, a 401 does not end anything", async () => {
    await read(s.client, keys.me, unauthorized);
    await read(s.client, keys.instances, unauthorized);
    s.session.onUnauthorized();
    expect(s.go).not.toHaveBeenCalled();
    expect(s.reads.clear).not.toHaveBeenCalled();
  });

  it("with a user, a 401 of a read ends the login session", async () => {
    await read(s.client, keys.me, me("u1"));
    await read(s.client, keys.instances, unauthorized);
    expect(s.reads.clear).toHaveBeenCalledTimes(1);
    expect(s.live.setUser).toHaveBeenLastCalledWith(false);
    expect(s.go).toHaveBeenCalledWith(`/login?next=${encodeURIComponent("/instances/7?x=1")}`);
    expect(s.session.hasUser()).toBe(false);
  });

  it("with a user, a 401 of the stream ends the login session", async () => {
    await read(s.client, keys.me, me("u1"));
    s.session.onUnauthorized();
    expect(s.reads.clear).toHaveBeenCalledTimes(1);
    expect(s.go).toHaveBeenCalledTimes(1);
  });

  it("with a user, a 401 of a change ends the login session", async () => {
    await read(s.client, keys.me, me("u1"));
    await s.client
      .getMutationCache()
      .build(s.client, {
        mutationKey: ["updateInstance"],
        mutationFn: () => Promise.reject(unauthorized),
      })
      .execute(undefined)
      .catch(() => undefined);
    expect(s.go).toHaveBeenCalledTimes(1);
  });

  it("a 401 of login never ends a login session", async () => {
    await read(s.client, keys.me, me("u1"));
    await s.client
      .getMutationCache()
      .build(s.client, { mutationKey: ["login"], mutationFn: () => Promise.reject(unauthorized) })
      .execute(undefined)
      .catch(() => undefined);
    expect(s.go).not.toHaveBeenCalled();
    expect(s.session.hasUser()).toBe(true);
  });

  it("another failure does not end the login session", async () => {
    await read(s.client, keys.me, me("u1"));
    await read(s.client, keys.instances, { kind: "unreachable" });
    await read(s.client, keys.instances, { ...unauthorized, status: 403, code: "owner_only" });
    expect(s.go).not.toHaveBeenCalled();
  });

  it("the same user in a new getMe changes nothing", async () => {
    await read(s.client, keys.me, me("u1"));
    await read(s.client, keys.me, me("u1"));
    expect(s.reload).not.toHaveBeenCalled();
    expect(s.live.setUser).toHaveBeenCalledTimes(1);
  });

  it("a different me.user.id reloads the page", async () => {
    await read(s.client, keys.me, me("u1"));
    await read(s.client, keys.me, me("u2"));
    expect(s.reload).toHaveBeenCalledTimes(1);
  });

  it("a new user after the end of a login session does not reload", async () => {
    await read(s.client, keys.me, me("u1"));
    s.session.onUnauthorized();
    await read(s.client, keys.me, me("u2"));
    expect(s.reload).not.toHaveBeenCalled();
    expect(s.session.hasUser()).toBe(true);
  });

  it("on a public path, the end goes to /login with no next", async () => {
    s.stop();
    s = setup({ pathname: "/invite", search: "?token=abc" });
    await read(s.client, keys.me, me("u1"));
    s.session.onUnauthorized();
    expect(s.go).toHaveBeenCalledWith("/login");
  });

  it("logout clears the data, closes the stream and goes to /login", async () => {
    await read(s.client, keys.me, me("u1"));
    s.logout.mockResolvedValue({ ok: true, data: undefined });
    expect(await s.session.logout()).toBeNull();
    expect(s.reads.clear).toHaveBeenCalledTimes(1);
    expect(s.live.setUser).toHaveBeenLastCalledWith(false);
    expect(s.go).toHaveBeenCalledWith("/login");
  });

  it("the end of the login session clears the drafts", async () => {
    const where = { client: "c1" };
    saveDraft(where, newDraft({ userId: "u1", clientId: "c1", kind: "new" }));
    await read(s.client, keys.me, me("u1"));
    s.session.onUnauthorized();
    expect(readDraft(where, "u1")).toBeNull();
  });

  it("logout clears the drafts", async () => {
    const where = { instance: "i1" };
    saveDraft(where, newDraft({ userId: "u1", clientId: "c1", kind: "reconnect" }));
    await read(s.client, keys.me, me("u1"));
    s.logout.mockResolvedValue({ ok: true, data: undefined });
    await s.session.logout();
    expect(readDraft(where, "u1")).toBeNull();
  });

  it("a logout that fails keeps the login session and gives the failure", async () => {
    await read(s.client, keys.me, me("u1"));
    s.logout.mockResolvedValue({ ok: false, failure: { kind: "unreachable" } });
    expect(await s.session.logout()).toEqual({ kind: "unreachable" });
    expect(s.go).not.toHaveBeenCalled();
    expect(s.session.hasUser()).toBe(true);
  });

  it("a logout with a 401 ends the login session too", async () => {
    await read(s.client, keys.me, me("u1"));
    s.logout.mockResolvedValue({ ok: false, failure: unauthorized });
    expect(await s.session.logout()).toBeNull();
    expect(s.go).toHaveBeenCalledWith("/login");
  });

  it("stops watching after the stop function", async () => {
    s.stop();
    await read(s.client, keys.me, me("u1"));
    expect(s.session.hasUser()).toBe(false);
  });
});
