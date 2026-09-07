import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownContent } from "./MarkdownContent";

// Covers the render-time wiring the pure-function tests in
// MarkdownContent.test.ts can't reach: that `pre` actually gets swapped for
// a CodeBlock (labelled and unlabelled), that `table` gets the scroll
// wrapper, that images are dropped, and that a soft line break survives
// (remark-breaks). Math isn't asserted here -- rehype-katex loads via a
// dynamic import gated on a `useEffect`, which doesn't run during
// server-side rendering, so a static render only ever sees the synchronous
// (non-math) markdown pipeline.

function render(text: string): string {
  return renderToStaticMarkup(createElement(MarkdownContent, { text }));
}

describe("MarkdownContent render", () => {
  it("renders a labelled fence through CodeBlock", () => {
    const html = render(["```ts", "const x = 1;", "```"].join("\n"));
    expect(html).toContain("chat-code-block");
    expect(html).toContain("chat-code-block__lang");
    expect(html).toContain("const");
  });

  it("renders an unlabelled fence through CodeBlock too, not raw text", () => {
    const html = render(["```", "plain block", "```"].join("\n"));
    expect(html).toContain("chat-code-block");
    expect(html).toContain("plain block");
  });

  it("wraps a table for horizontal scroll", () => {
    const html = render(["| a | b |", "|---|---|", "| 1 | 2 |"].join("\n"));
    expect(html).toContain("chat-table-wrap");
    expect(html).toContain("<table>");
  });

  it("drops images instead of loading them", () => {
    const html = render("![alt](https://example.com/tracker.gif)");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("tracker.gif");
  });

  it("preserves a soft line break in plain prose", () => {
    const html = render(["line one", "line two"].join("\n"));
    expect(html).toContain("<br");
  });
});
