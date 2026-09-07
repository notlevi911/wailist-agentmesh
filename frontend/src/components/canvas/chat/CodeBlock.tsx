"use client";
import { useState } from "react";
import { PrismAsyncLight as SyntaxHighlighter } from "react-syntax-highlighter";
import { vscDarkPlus } from "react-syntax-highlighter/dist/esm/styles/prism";

// A fenced ```lang code block from agent/markdown output. Chrome (language
// label + copy button) is its own header row so it reads the same whether or
// not the language was recognised -- an unhighlighted block still needs a
// copy button.

interface CodeBlockProps {
  code: string;
  language?: string;
}

export function CodeBlock({ code, language }: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      // Fire-and-forget: this is a display timer, not state anything else
      // depends on, so no cleanup ref is worth the complexity here.
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable (permissions, insecure context) -- no-op */
    }
  };

  return (
    <div className="chat-code-block">
      <div className="chat-code-block__header">
        <span className="chat-code-block__lang">{language || "text"}</span>
        <button
          type="button"
          className="chat-code-block__copy"
          onClick={copy}
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <div className="chat-code-block__body">
        {language ? (
          <SyntaxHighlighter
            language={language}
            style={vscDarkPlus}
            PreTag="div"
            customStyle={{
              margin: 0,
              padding: "10px 12px",
              background: "transparent",
              fontSize: 12,
            }}
            codeTagProps={{ style: { fontFamily: "var(--font-mono)" } }}
          >
            {code}
          </SyntaxHighlighter>
        ) : (
          <pre className="chat-code-block__plain">
            <code>{code}</code>
          </pre>
        )}
      </div>
    </div>
  );
}
