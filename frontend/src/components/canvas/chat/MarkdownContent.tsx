"use client";
import { memo, useEffect, useMemo, useState } from "react";
import type { Element as HastElement } from "hast";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import type { PluggableList } from "unified";
import { CodeBlock } from "./CodeBlock";

// Renders one message's text as markdown: headers/lists/tables/emphasis via
// remark-gfm, soft line breaks preserved via remark-breaks (CommonMark
// collapses a single newline into a space, which reads as a regression for
// the common "agent just says a few lines" plain-text reply), inline `$..$`
// and block `$$..$$` math via remark-math + rehype-katex (loaded lazily,
// see useMathPlugins below), and fenced code blocks as CodeBlock (syntax
// highlight + copy button). Plain text with no markdown in it still
// round-trips fine -- it just comes out as a single <p>.
//
// `pre` is the only element we intercept. Reaching into its hast `node`
// (rather than trusting the already-rendered `children`, which by the time
// `pre` runs have already gone through react-markdown's own `code`
// component) is what lets one code path cover both `language-xxx` fences and
// unlabelled ones -- a fence with no language still arrives as `pre > code`,
// it just has no `language-` class on it.

/** Exported for tests: the raw-text and language extraction is what makes
 * both labelled and unlabelled fences render through the same CodeBlock. */
export function textOf(node: HastElement | undefined): string {
  if (!node?.children) return "";
  return node.children
    .map((child) =>
      child.type === "text" ? child.value : textOf("children" in child ? (child as HastElement) : undefined),
    )
    .join("");
}

export function languageOf(node: HastElement): string | undefined {
  const raw = node.properties?.className;
  const classes = Array.isArray(raw) ? raw.map(String) : [];
  const match = classes.find((c) => c.startsWith("language-"));
  return match?.slice("language-".length);
}

const components: Components = {
  pre({ node, children }) {
    const codeNode = node?.children.find(
      (c): c is HastElement => c.type === "element" && c.tagName === "code",
    );
    if (!codeNode) return <pre>{children}</pre>;
    return <CodeBlock code={textOf(codeNode).replace(/\n$/, "")} language={languageOf(codeNode)} />;
  },
  // Wide tables scroll inside their own container rather than pushing the
  // whole ~62ch bubble (and the rail around it) wider.
  table({ children }) {
    return (
      <div className="chat-table-wrap">
        <table>{children}</table>
      </div>
    );
  },
  // Agent text is never trusted markup: rendering a remote `![](url)` would
  // fire an unproxied request straight from the reader's browser (a
  // tracking-pixel / IP-leak vector), and there's no image proxy to route
  // it through. Drop images rather than load them.
  img: () => null,
};

/**
 * Loads remark-math + rehype-katex + KaTeX's CSS only for messages that
 * actually contain a `$` -- the katex runtime and stylesheet are large
 * enough (~300KB combined) that pulling them into every chat render, math or
 * not, is wasteful. Once loaded for one message the browser has the chunk
 * cached, so later math messages resolve instantly.
 */
function useMathPlugins(text: string): { remark: PluggableList; rehype: PluggableList } {
  const hasMath = useMemo(() => text.includes("$"), [text]);
  const [loaded, setLoaded] = useState<{ remarkMath: unknown; rehypeKatex: unknown } | null>(null);

  useEffect(() => {
    if (!hasMath || loaded) return;
    let cancelled = false;
    Promise.all([import("remark-math"), import("rehype-katex"), import("katex/dist/katex.min.css")]).then(
      ([remarkMathMod, rehypeKatexMod]) => {
        if (!cancelled) {
          setLoaded({ remarkMath: remarkMathMod.default, rehypeKatex: rehypeKatexMod.default });
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, [hasMath, loaded]);

  return useMemo(
    () => ({
      remark: loaded ? [loaded.remarkMath as never] : [],
      rehype: loaded ? [loaded.rehypeKatex as never] : [],
    }),
    [loaded],
  );
}

export const MarkdownContent = memo(function MarkdownContent({ text }: { text: string }) {
  const math = useMathPlugins(text);
  const remarkPlugins = useMemo(() => [remarkGfm, remarkBreaks, ...math.remark], [math.remark]);

  return (
    <div className="chat-markdown">
      <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={math.rehype} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});
