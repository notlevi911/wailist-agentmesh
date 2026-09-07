import { describe, it, expect } from "vitest";
import { toSpeechText } from "./speechText";

describe("toSpeechText", () => {
  it("passes a plain sentence through unchanged", () => {
    expect(toSpeechText("Gold is about $2,640/oz.")).toBe(
      "Gold is about $2,640/oz.",
    );
  });

  it("leaves two separate dollar amounts alone instead of reading them as one equation", () => {
    expect(toSpeechText("Gold is $2,640, silver is $30.")).toBe(
      "Gold is $2,640, silver is $30.",
    );
  });

  it("replaces a fenced code block with a spoken placeholder", () => {
    const raw = "Here you go:\n```js\nconst x = 1;\n```\nThat's it.";
    expect(toSpeechText(raw)).toBe("Here you go:\n(code omitted)\nThat's it.");
  });

  it("unwraps inline code, keeping the content", () => {
    expect(toSpeechText("Run `npm install` first.")).toBe(
      "Run npm install first.",
    );
  });

  it("unwraps bold and italic without leaving marker characters", () => {
    expect(toSpeechText("This is **bold** and this is *italic*.")).toBe(
      "This is bold and this is italic.",
    );
  });

  it("unwraps bold and italic written with underscores", () => {
    expect(toSpeechText("This is __bold__ and this is _italic_.")).toBe(
      "This is bold and this is italic.",
    );
  });

  it("leaves intraword underscores alone (identifiers, not emphasis)", () => {
    expect(toSpeechText("Check user_id and snake_case_name.")).toBe(
      "Check user_id and snake_case_name.",
    );
  });

  it("leaves spaced-out asterisks alone (multiplication, not emphasis)", () => {
    expect(toSpeechText("So 5 * 3 and 2 * 4 = 8, roughly.")).toBe(
      "So 5 * 3 and 2 * 4 = 8, roughly.",
    );
    // A real emphasis span next to arithmetic still unwraps.
    expect(toSpeechText("Result is *exactly* 5 * 3.")).toBe(
      "Result is exactly 5 * 3.",
    );
  });

  it("unwraps a markdown link to its label", () => {
    expect(toSpeechText("See [the docs](https://example.com) for more.")).toBe(
      "See the docs for more.",
    );
  });

  it("strips heading markers", () => {
    expect(toSpeechText("## Summary\nEverything worked.")).toBe(
      "Summary\nEverything worked.",
    );
  });

  it("strips list markers", () => {
    const raw = "Steps:\n- first\n- second\n3. third";
    expect(toSpeechText(raw)).toBe("Steps:\nfirst\nsecond\nthird");
  });

  it("replaces LaTeX with a spoken placeholder", () => {
    expect(toSpeechText("The area is $A = \\pi r^2$ exactly.")).toBe(
      "The area is (equation) exactly.",
    );
  });

  it("collapses long blank runs and repeated spaces", () => {
    expect(toSpeechText("Line one.\n\n\n\nLine two.   Padded.")).toBe(
      "Line one.\n\nLine two. Padded.",
    );
  });
});
