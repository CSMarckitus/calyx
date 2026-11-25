import { describe, expect, it } from "vitest";
import {
  checkSizes,
  DOCS_JS_CEILING_BYTES,
  DOCS_JS_LIMIT_BYTES,
  FONT_LIMIT_BYTES,
  JS_LIMIT_BYTES,
} from "./size-limits";

const ok = { jsGzip: 0, docsJsGzip: 0, fonts: 0 };

describe("checkSizes", () => {
  it("uses decimal kilobytes", () => {
    expect(JS_LIMIT_BYTES).toBe(250_000);
    expect(FONT_LIMIT_BYTES).toBe(260_000);
    expect(DOCS_JS_CEILING_BYTES).toBe(1_500_000);
  });

  it("keeps the limit of the docs page under its ceiling", () => {
    expect(DOCS_JS_LIMIT_BYTES).toBeLessThanOrEqual(DOCS_JS_CEILING_BYTES);
  });

  it("sets the limit with the rule size plus 10 %, never above the ceiling", () => {
    const finalBuild = 1_457_682;
    const rule = Math.ceil((finalBuild * 1.1) / 10_000) * 10_000;
    expect(rule).toBe(1_610_000);
    expect(DOCS_JS_LIMIT_BYTES).toBe(Math.min(DOCS_JS_CEILING_BYTES, rule));
  });

  it("sets the limit of the panel with the rule size plus 10 %", () => {
    const measured = 218_318;
    const rule = Math.ceil((measured * 1.1) / 10_000) * 10_000;
    expect(rule).toBe(250_000);
    expect(JS_LIMIT_BYTES).toBe(rule);
  });

  it("passes at the limits", () => {
    const sizes = {
      jsGzip: JS_LIMIT_BYTES,
      docsJsGzip: DOCS_JS_LIMIT_BYTES,
      fonts: FONT_LIMIT_BYTES,
    };
    expect(checkSizes(sizes)).toEqual([]);
  });

  it("fails when the JavaScript of the panel is too big", () => {
    const problems = checkSizes({ ...ok, jsGzip: JS_LIMIT_BYTES + 1 });
    expect(problems).toEqual([
      `JavaScript is ${JS_LIMIT_BYTES + 1} bytes gzip. The limit is ${JS_LIMIT_BYTES}.`,
    ]);
  });

  it("fails when the JavaScript of the docs page is too big", () => {
    const n = DOCS_JS_LIMIT_BYTES + 1;
    expect(checkSizes({ ...ok, docsJsGzip: n })).toEqual([
      `The JavaScript of the docs page is ${n} bytes gzip. The limit is ${DOCS_JS_LIMIT_BYTES}.`,
    ]);
  });

  it("fails when the fonts are too big", () => {
    const problems = checkSizes({ ...ok, fonts: FONT_LIMIT_BYTES + 1 });
    expect(problems).toHaveLength(1);
    expect(problems[0]).toContain("fonts");
  });

  it("reports the three problems", () => {
    expect(checkSizes({ jsGzip: 9_000_000, docsJsGzip: 9_000_000, fonts: 9_000_000 })).toHaveLength(
      3,
    );
  });
});
