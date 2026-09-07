// Turns an assistant reply's raw text into something worth reading aloud.
//
// ChatMessage renders message.text through MarkdownContent's react-markdown
// AST (#107), but that AST is a rendering pipeline, not a text one -- there's
// no ready-made "flatten this to spoken words" walk over hast/mdast nodes to
// reuse, and building one would still need its own rules for what a heading,
// a fence, or a LaTeX span sounds like. Simpler to work from the same raw
// markdown string via regex: strip syntax that would otherwise be read
// character-by-character ("asterisk asterisk bold asterisk asterisk"), and
// drop code/math to a short spoken placeholder rather than reading either
// verbatim.

const FENCED_CODE = /```[\s\S]*?```/g;
const INLINE_CODE = /`([^`]+)`/g;
const LATEX_BLOCK = /\$\$[\s\S]*?\$\$|\\\[[\s\S]*?\\\]/g;
// A closing `$` immediately followed by a digit is almost always a second,
// unrelated currency amount rather than the end of the same LaTeX span (e.g.
// "Gold is $2,640, silver is $30." has no math in it at all) — this is the
// same heuristic Pandoc uses to disambiguate inline math from prices.
const LATEX_INLINE = /\$[^$\n]+\$(?!\d)|\\\([\s\S]*?\\\)/g;
const IMAGE = /!\[([^\]]*)\]\([^)]*\)/g;
const LINK = /\[([^\]]*)\]\([^)]*\)/g;
const HEADING = /^ {0,3}#{1,6}\s+/gm;
const BLOCKQUOTE = /^ {0,3}>\s?/gm;
const LIST_MARKER = /^ {0,3}(?:[-*+]|\d+\.)\s+/gm;
const HR = /^ {0,3}(?:-{3,}|\*{3,}|_{3,})\s*$/gm;

// Underscore emphasis follows CommonMark's intraword rule: `_` doesn't open
// or close emphasis in the middle of a word, so `user_id` and
// `snake_case_name` — common in an agent's replies — pass through untouched.
//
// Asterisk emphasis instead follows CommonMark's whitespace-flanking rule: a
// `*` cannot open a span when followed by whitespace, nor close one when
// preceded by it. The `(?!\s)` / `(?<!\s)` guards enforce that, so spaced-out
// multiplication like "5 * 3 and 2 * 4" is left alone rather than collapsed to
// "5  3 and 2  4".
const BOLD_ITALIC_STAR = /\*\*\*(?!\s)(.+?)(?<!\s)\*\*\*/g;
const BOLD_ITALIC_USCORE = /(?<!\w)___(.+?)___(?!\w)/g;
const BOLD_STAR = /\*\*(?!\s)(.+?)(?<!\s)\*\*/g;
const BOLD_USCORE = /(?<!\w)__(.+?)__(?!\w)/g;
const ITALIC_STAR = /\*(?!\s)([^*\n]+?)(?<!\s)\*/g;
const ITALIC_USCORE = /(?<!\w)_(.+?)_(?!\w)/g;
const STRIKETHROUGH = /~~(.+?)~~/g;

const BLANK_RUN = /\n{3,}/g;
const SPACE_RUN = /[ \t]{2,}/g;
// The code/equation placeholders are padded with a space on each side so they
// don't fuse with adjacent words, but that padding is stray whitespace when
// the placeholder instead sits next to a newline.
const LINE_EDGE_SPACE = /[ \t]+\n|\n[ \t]+/g;

/**
 * Plain-text version of `raw`, safe to hand to `SpeechSynthesisUtterance`.
 * Pure and DOM-free so it can be unit-tested without a browser.
 */
export function toSpeechText(raw: string): string {
  let text = raw;

  text = text.replace(FENCED_CODE, " (code omitted) ");
  text = text.replace(LATEX_BLOCK, " (equation) ");
  text = text.replace(LATEX_INLINE, " (equation) ");
  text = text.replace(INLINE_CODE, "$1");
  text = text.replace(IMAGE, "$1");
  text = text.replace(LINK, "$1");
  text = text.replace(HEADING, "");
  text = text.replace(BLOCKQUOTE, "");
  text = text.replace(HR, "");
  text = text.replace(LIST_MARKER, "");
  text = text.replace(BOLD_ITALIC_STAR, "$1");
  text = text.replace(BOLD_ITALIC_USCORE, "$1");
  text = text.replace(BOLD_STAR, "$1");
  text = text.replace(BOLD_USCORE, "$1");
  text = text.replace(ITALIC_STAR, "$1");
  text = text.replace(ITALIC_USCORE, "$1");
  text = text.replace(STRIKETHROUGH, "$1");
  text = text.replace(LINE_EDGE_SPACE, "\n");
  text = text.replace(SPACE_RUN, " ");
  text = text.replace(BLANK_RUN, "\n\n");

  return text.trim();
}
