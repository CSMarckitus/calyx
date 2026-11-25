import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NewVersion } from "./new-version.tsx";

const mine = { version: "0.1.0", build: "2026-01-01T00:00:00.000Z" };
const fetchMock = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>();
let visible = true;

function meta(name: string, content: string) {
  const el = document.createElement("meta");
  el.name = name;
  el.content = content;
  document.head.append(el);
}

const serve = (body: unknown) =>
  fetchMock.mockImplementation(() => Promise.resolve(new Response(JSON.stringify(body))));

async function comeBack() {
  await act(async () => {
    visible = true;
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

beforeEach(() => {
  document.head.innerHTML = "";
  meta("calyx-version", mine.version);
  meta("calyx-build", mine.build);
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
  visible = true;
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => (visible ? "visible" : "hidden"),
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("NewVersion", () => {
  it("shows nothing before the page asks", () => {
    serve(mine);
    render(<NewVersion />);
    expect(screen.queryByText("A new version is ready.")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows nothing when the build is the same", async () => {
    serve(mine);
    render(<NewVersion />);
    await comeBack();
    expect(fetchMock).toHaveBeenCalledWith(
      "/version.json",
      expect.objectContaining({ cache: "no-store" }),
    );
    expect(screen.queryByText("A new version is ready.")).toBeNull();
  });

  it("shows the notice and a Reload button when the build differs", async () => {
    serve({ version: "0.1.0", build: "2099-01-01T00:00:00.000Z" });
    render(<NewVersion />);
    await comeBack();
    expect(screen.getByText("A new version is ready.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });

  it("does not ask while the page is hidden", async () => {
    serve(mine);
    render(<NewVersion />);
    await act(async () => {
      visible = false;
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("ignores a failed request and an answer that is not a version", async () => {
    render(<NewVersion />);
    fetchMock.mockRejectedValue(new Error("offline"));
    await comeBack();
    serve({ nothing: true });
    await comeBack();
    fetchMock.mockImplementation(() => Promise.resolve(new Response("<html>", { status: 200 })));
    await comeBack();
    expect(screen.queryByText("A new version is ready.")).toBeNull();
  });

  it("does nothing when the page has no build time (dev server)", async () => {
    document.head.innerHTML = "";
    serve({ version: "0.1.0", build: "2099-01-01T00:00:00.000Z" });
    render(<NewVersion />);
    await comeBack();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
