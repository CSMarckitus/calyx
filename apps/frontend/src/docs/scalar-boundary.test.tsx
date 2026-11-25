import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ScalarBoundary } from "./scalar-boundary.tsx";

afterEach(cleanup);

function Broken(): never {
  throw new Error("the viewer stopped");
}

describe("ScalarBoundary", () => {
  it("shows the child when it renders", () => {
    render(
      <ScalarBoundary>
        <p>The reference</p>
      </ScalarBoundary>,
    );
    expect(screen.getByText("The reference")).toBeTruthy();
  });

  it("says that the page cannot show the reference, and links to /openapi.json", () => {
    // React logs the error of a child that throws. The test expects it.
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <ScalarBoundary>
        <Broken />
      </ScalarBoundary>,
    );
    log.mockRestore();
    expect(screen.getByRole("alert").textContent).toContain("The page cannot show the reference.");
    expect(screen.getByRole("link", { name: "/openapi.json" }).getAttribute("href")).toBe(
      "/openapi.json",
    );
  });
});
