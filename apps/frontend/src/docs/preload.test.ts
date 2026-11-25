// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { reloadOnce } from "./preload.ts";

// A fake Storage with only the methods that reloadOnce uses.
function fakeStorage(start: Record<string, string> = {}): Storage {
  const items = new Map(Object.entries(start));
  return {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => void items.set(k, v),
  } as Storage;
}

describe("reloadOnce", () => {
  it("sets the flag and reloads when the flag is absent", () => {
    const storage = fakeStorage();
    const reload = vi.fn();
    reloadOnce(storage, reload);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(storage.getItem("calyx-docs-reloaded")).not.toBeNull();
  });

  it("does nothing when the flag is present", () => {
    const storage = fakeStorage({ "calyx-docs-reloaded": "1" });
    const reload = vi.fn();
    reloadOnce(storage, reload);
    expect(reload).not.toHaveBeenCalled();
  });

  it("reloads one time only for two missing chunks", () => {
    const storage = fakeStorage();
    const reload = vi.fn();
    reloadOnce(storage, reload);
    reloadOnce(storage, reload);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("does nothing when the storage is blocked, so the page cannot reload in a loop", () => {
    const storage = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
    } as unknown as Storage;
    const reload = vi.fn();
    reloadOnce(storage, reload);
    expect(reload).not.toHaveBeenCalled();
  });
});
