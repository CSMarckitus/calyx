import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Words } from "./words.tsx";

afterEach(cleanup);

const parts = (el: HTMLElement) =>
  [...el.children].map((c) => [c.textContent, c.getAttribute("translate")]);

describe("Words", () => {
  it("keeps the word Calyx and the given names, each part in its own element", () => {
    const { container } = render(
      <p>
        <Words text="Ask Acme Ltd. Calyx waits for Acme Ltd." keep={["Acme Ltd"]} />
      </p>,
    );
    const p = container.querySelector("p") as HTMLElement;
    expect(p.textContent).toBe("Ask Acme Ltd. Calyx waits for Acme Ltd.");
    expect(parts(p)).toEqual([
      ["Ask ", null],
      ["Acme Ltd", "no"],
      [". ", null],
      ["Calyx", "no"],
      [" waits for ", null],
      ["Acme Ltd", "no"],
      [".", null],
    ]);
    // No text node is a direct child: React writes each part as the only child of its element.
    expect([...p.childNodes].every((n) => n.nodeType === Node.ELEMENT_NODE)).toBe(true);
  });

  it("reads a name as text, not as a pattern", () => {
    const { container } = render(
      <p>
        <Words text="Ask a.b (c)+ now." keep={["a.b (c)+", ""]} />
      </p>,
    );
    expect(parts(container.querySelector("p") as HTMLElement)).toEqual([
      ["Ask ", null],
      ["a.b (c)+", "no"],
      [" now.", null],
    ]);
  });

  it("keeps a longer name whole when it holds a shorter kept word", () => {
    const { container } = render(
      <p>
        <Words text="Ask Calyx Admin and Al." keep={["Al", "Calyx Admin"]} />
      </p>,
    );
    expect(parts(container.querySelector("p") as HTMLElement)).toEqual([
      ["Ask ", null],
      ["Calyx Admin", "no"],
      [" and ", null],
      ["Al", "no"],
      [".", null],
    ]);
  });

  it("gives one element for a text with no kept word", () => {
    const { container } = render(
      <p>
        <Words text="Connected" />
      </p>,
    );
    expect(parts(container.querySelector("p") as HTMLElement)).toEqual([["Connected", null]]);
  });
});
