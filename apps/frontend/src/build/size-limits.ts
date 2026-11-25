// 1 KB is 1000 bytes.
// The panel is 218,318 bytes gzip after the sidebar (2026-11-25). The rule is size plus 10 %
// (240,149.8), rounded up to the next 10,000 bytes. So the limit is 250,000.
export const JS_LIMIT_BYTES = 250_000;
export const FONT_LIMIT_BYTES = 260_000;

// The docs page is 1,409,500 bytes gzip in a first test and 1,457,682 bytes in the final build (97 % of
// the ceiling). The same rule gives 1,610,000 (size plus 10 %, rounded up to 10,000), which is above
// the ceiling. So the limit is the ceiling itself.
export const DOCS_JS_CEILING_BYTES = 1_500_000;
export const DOCS_JS_LIMIT_BYTES = 1_500_000;

export type Sizes = { jsGzip: number; docsJsGzip: number; fonts: number };

export function checkSizes(sizes: Sizes): string[] {
  const problems: string[] = [];
  if (sizes.jsGzip > JS_LIMIT_BYTES) {
    problems.push(`JavaScript is ${sizes.jsGzip} bytes gzip. The limit is ${JS_LIMIT_BYTES}.`);
  }
  if (sizes.docsJsGzip > DOCS_JS_LIMIT_BYTES) {
    problems.push(
      `The JavaScript of the docs page is ${sizes.docsJsGzip} bytes gzip. The limit is ${DOCS_JS_LIMIT_BYTES}.`,
    );
  }
  if (sizes.fonts > FONT_LIMIT_BYTES) {
    problems.push(`The fonts are ${sizes.fonts} bytes. The limit is ${FONT_LIMIT_BYTES}.`);
  }
  return problems;
}
