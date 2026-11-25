import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Trace } from "./trace";

const steps = [
  { id: "name", title: "Name", value: "Shop" },
  { id: "method", title: "Method", children: <p>Pick a method</p> },
  { id: "scan", title: "Scan" },
];

afterEach(cleanup);

describe("Trace", () => {
  it("is an ordered list with one item for each step, in order", () => {
    render(<Trace steps={steps} current={1} />);
    const list = screen.getByRole("list");
    expect(list.tagName).toBe("OL");
    const items = within(list).getAllByRole("listitem");
    expect(items.map((li) => li.querySelector("b")?.textContent)).toEqual([
      "Name",
      "Method",
      "Scan",
    ]);
  });

  it("marks only the current step with aria-current", () => {
    render(<Trace steps={steps} current={1} />);
    const items = screen.getAllByRole("listitem");
    expect(items.map((li) => li.getAttribute("aria-current"))).toEqual([null, "step", null]);
  });

  it("shows the value of a done step and the body of the current step only", () => {
    render(<Trace steps={steps} current={1} />);
    expect(screen.getByText("Shop")).toBeTruthy();
    expect(screen.getByText("Pick a method")).toBeTruthy();
  });

  it("gives each state a word", () => {
    render(<Trace steps={steps} current={1} />);
    const items = screen.getAllByRole("listitem");
    expect(items.map((li) => li.dataset.state)).toEqual(["done", "current", "next"]);
    expect(within(items[0]).getByText("Done")).toBeTruthy();
    expect(within(items[1]).getByText("Current step")).toBeTruthy();
  });

  it("marks every step done and no current step when complete", () => {
    render(<Trace steps={steps} current={2} complete />);
    const items = screen.getAllByRole("listitem");
    expect(items.map((li) => li.dataset.state)).toEqual(["done", "done", "done"]);
    expect(items.some((li) => li.hasAttribute("aria-current"))).toBe(false);
  });

  it("keeps the result under the last step when complete", () => {
    const done = [
      ...steps.slice(0, 2),
      { id: "done", title: "Connected", children: <p>Result</p> },
    ];
    render(<Trace steps={done} current={2} complete />);
    expect(within(screen.getAllByRole("listitem")[2]).getByText("Result")).toBeTruthy();
    expect(screen.queryByText("Pick a method")).toBeNull();
  });

  it("marks a failed current step", () => {
    render(<Trace steps={steps} current={2} failed />);
    expect(screen.getAllByRole("listitem")[2].dataset.state).toBe("failed");
  });
});
