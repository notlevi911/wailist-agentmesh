import { describe, expect, it } from "vitest";
import type { Element as HastElement, Text as HastText } from "hast";
import { languageOf, textOf } from "./MarkdownContent";

// These two helpers are what let the `pre` override in MarkdownContent
// handle a fenced code block by reading react-markdown's hast node directly,
// rather than trusting the already-rendered `children` (which have, by the
// time `pre` runs, already gone through react-markdown's own `code`
// component and lost the raw text/className). Covering them here pins that
// behaviour without needing a full component-render harness, matching how
// the rest of this directory tests its pure logic (settleIn, resolveReply).

function text(value: string): HastText {
  return { type: "text", value };
}

function code(className: string[] | undefined, children: HastElement["children"]): HastElement {
  return {
    type: "element",
    tagName: "code",
    properties: className ? { className } : {},
    children,
  };
}

describe("textOf", () => {
  it("joins plain text children", () => {
    expect(textOf(code(undefined, [text("const x = 1;")]))).toBe("const x = 1;");
  });

  it("concatenates multiple text nodes in order", () => {
    expect(textOf(code(undefined, [text("line one\n"), text("line two")]))).toBe(
      "line one\nline two",
    );
  });

  it("returns empty string for an undefined node", () => {
    expect(textOf(undefined)).toBe("");
  });

  it("recurses into nested elements", () => {
    const nested: HastElement = {
      type: "element",
      tagName: "span",
      properties: {},
      children: [text("nested")],
    };
    expect(textOf(code(undefined, [nested]))).toBe("nested");
  });
});

describe("languageOf", () => {
  it("extracts the language from a language-xxx class", () => {
    expect(languageOf(code(["language-typescript"], []))).toBe("typescript");
  });

  it("returns undefined for an unlabelled fence", () => {
    expect(languageOf(code(undefined, []))).toBeUndefined();
  });

  it("ignores unrelated classes alongside the language one", () => {
    expect(languageOf(code(["hljs", "language-python"], []))).toBe("python");
  });
});
