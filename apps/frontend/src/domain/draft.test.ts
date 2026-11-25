import { beforeEach, describe, expect, it } from "vitest";
import {
  clearDrafts,
  clearStarted,
  type Draft,
  endDraft,
  endDraftNamed,
  markStarted,
  moveDraft,
  newDraft,
  readDraft,
  saveDraft,
  startedRecently,
} from "./draft.ts";

const anna = "user-anna";
const bob = "user-bob";
const clientA = { client: "client-a" };
const clientB = { client: "client-b" };
const instance = { instance: "instance-1" };

const draft = (over: Partial<Draft> = {}): Draft => ({
  ...newDraft({ userId: anna, clientId: "client-a", kind: "new" }),
  name: "Shop",
  ...over,
});

beforeEach(() => sessionStorage.clear());

describe("a draft", () => {
  it("starts with the automatic values", () => {
    expect(newDraft({ userId: anna, clientId: "client-a", kind: "new" })).toEqual({
      userId: anna,
      clientId: "client-a",
      kind: "new",
      name: "",
      sendOnly: false,
      locale: null,
      device: null,
      method: null,
      phone: null,
      startedAt: null,
    });
  });

  it("stays after a reload, and holds no code and no QR", () => {
    saveDraft(clientA, draft({ phone: { country: "ES", text: "600 123 456" } }));
    expect(readDraft(clientA, anna)).toEqual(
      draft({ phone: { country: "ES", text: "600 123 456" } }),
    );
    expect(Object.keys(readDraft(clientA, anna) ?? {})).not.toContain("code");
  });

  it("of another user is discarded", () => {
    saveDraft(clientA, draft());
    expect(readDraft(clientA, bob)).toBeNull();
    // Discarded, so it is gone for the first user too.
    expect(readDraft(clientA, anna)).toBeNull();
  });

  it("of another client is not read", () => {
    saveDraft(clientA, draft());
    expect(readDraft(clientB, anna)).toBeNull();
    expect(readDraft(clientA, anna)).not.toBeNull();
  });

  it("under the key of a client, with another client inside, is not read", () => {
    saveDraft(clientB, draft());
    expect(readDraft(clientB, anna)).toBeNull();
  });

  it("that is not valid is discarded", () => {
    sessionStorage.setItem("calyx.draft.client.client-a", "{not json");
    expect(readDraft(clientA, anna)).toBeNull();
    sessionStorage.setItem("calyx.draft.client.client-a", JSON.stringify({ userId: anna }));
    expect(readDraft(clientA, anna)).toBeNull();
  });

  it("moves to the key of the instance, and back to the key of its client", () => {
    saveDraft(clientA, draft());
    moveDraft(clientA, instance);
    expect(readDraft(clientA, anna)).toBeNull();
    expect(readDraft(instance, anna)?.name).toBe("Shop");
    moveDraft(instance, { client: "client-a" });
    expect(readDraft(clientA, anna)?.name).toBe("Shop");
    expect(readDraft(instance, anna)).toBeNull();
  });

  it("ends", () => {
    saveDraft(instance, draft());
    endDraft(instance);
    expect(readDraft(instance, anna)).toBeNull();
  });

  it("of a client ends when its name is the name of an instance that the person continues", () => {
    saveDraft(clientA, draft());
    endDraftNamed("client-a", "Other", anna);
    expect(readDraft(clientA, anna)).not.toBeNull();
    endDraftNamed("client-a", "Shop", anna);
    expect(readDraft(clientA, anna)).toBeNull();
  });

  it("are all cleared at the end of the login session, and nothing else is", () => {
    saveDraft(clientA, draft());
    saveDraft(instance, draft());
    sessionStorage.setItem("other", "x");
    clearDrafts();
    expect(sessionStorage.length).toBe(1);
    expect(sessionStorage.getItem("other")).toBe("x");
  });
});

describe("started", () => {
  it("is set with its time, and is recent for less than 10 minutes", () => {
    saveDraft(instance, draft());
    markStarted(instance, { userId: anna, clientId: "client-a", kind: "new" }, 1000);
    expect(readDraft(instance, anna)?.startedAt).toBe(1000);
    expect(readDraft(instance, anna)?.name).toBe("Shop");
    expect(startedRecently(instance, anna, 1000 + 599_999)).toBe(true);
    expect(startedRecently(instance, anna, 1000 + 600_000)).toBe(false);
  });

  it("makes a draft with no phone number when the tab has none", () => {
    markStarted(instance, { userId: anna, clientId: "client-a", kind: "reconnect" }, 1000);
    expect(readDraft(instance, anna)).toEqual({
      ...newDraft({ userId: anna, clientId: "client-a", kind: "reconnect" }),
      startedAt: 1000,
    });
  });

  it("is removed, and the draft stays", () => {
    saveDraft(instance, draft({ startedAt: 1000 }));
    clearStarted(instance);
    expect(readDraft(instance, anna)?.startedAt).toBeNull();
    expect(startedRecently(instance, anna, 1000)).toBe(false);
  });

  it("of another user is not recent", () => {
    saveDraft(instance, draft({ startedAt: 1000 }));
    expect(startedRecently(instance, bob, 1000)).toBe(false);
  });
});
