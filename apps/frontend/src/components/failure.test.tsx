import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Failure as RequestFailure } from "../api/request.ts";
import { Failure } from "./failure.tsx";

afterEach(cleanup);

const problem = (code: string, detail = "", requestId = ""): RequestFailure => ({
  kind: "problem",
  status: 500,
  code,
  detail,
  fields: [],
  requestId,
});
const internal = problem("internal", "boom", "req-42");

describe("Failure", () => {
  it("says Checking the result while the page reads the data after a lost change", () => {
    const { rerender } = render(
      <Failure
        failure={{ kind: "timeout" }}
        context={{ operationId: "updateInstance", role: "owner" }}
        checking
      />,
    );
    expect(screen.getByRole("status").textContent).toBe("Checking the result");
    rerender(
      <Failure
        failure={{ kind: "timeout" }}
        context={{ operationId: "updateInstance", role: "owner" }}
      />,
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("shows the cause and the next step", () => {
    render(<Failure failure={internal} context={{ operationId: "login", role: "owner" }} />);
    const box = screen.getByRole("alert");
    expect(box.textContent).toContain("Calyx had an error.");
    expect(box.textContent).toContain("Send them to the server admin.");
  });

  it("leaves out an empty next step", () => {
    const failure = { ...problem("not_found"), status: 404 };
    render(<Failure failure={failure} context={{ operationId: "revokeAPIKey", role: "owner" }} />);
    const box = screen.getByRole("alert");
    expect(box.textContent).toContain("This key does not exist now. It was revoked.");
    expect([...box.children].filter((c) => c.textContent === "")).toEqual([]);
  });

  it("shows a name between backticks as code, with no markup injection", () => {
    render(
      <Failure
        failure={problem("csrf_check_failed")}
        context={{ operationId: "login", role: "owner" }}
      />,
    );
    const code = screen.getByRole("alert").querySelector("code");
    expect(code?.textContent).toBe("PUBLIC_URL");
    expect(screen.getByRole("alert").textContent).not.toContain("`");
  });

  it("keeps technical details closed, and shows the request id of a 5xx when opened", () => {
    render(<Failure failure={internal} context={{ operationId: "login", role: "owner" }} />);
    expect(screen.queryByText("req-42")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Technical details" }));
    expect(screen.getByText("req-42")).toBeTruthy();
    expect(screen.getByText("internal")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });

  it("copies the details", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<Failure failure={internal} context={{ operationId: "login", role: "owner" }} />);
    fireEvent.click(screen.getByRole("button", { name: "Technical details" }));
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("Code: internal\nDetail: boom\nRequest ID: req-42");
  });

  it("has no technical details when there are none", () => {
    render(
      <Failure
        failure={{ kind: "unreachable" }}
        context={{ operationId: "login", role: "owner" }}
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain("Calyx does not answer.");
    expect(screen.queryByRole("button", { name: "Technical details" })).toBeNull();
  });
});
