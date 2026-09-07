import { describe, it, expect } from "vitest";
import { LABEL_BUTTONS, ICON_BUTTONS } from "./buttons";

// Guards the CAUSE of a bug whose EFFECT this test cannot see.
//
// jsdom performs no layout -- scrollHeight is always 0 here -- so no unit test
// can detect a label actually rendering outside its box. What it CAN do is hold
// every shared button to the style contract that makes the overflow impossible
// in the first place, which is where the bug came from.
//
// The effect is checked in a real browser. Paste this at 800 / 900 / 1100 /
// 1280 on each route; both arrays must come back empty:
//
//   (() => {
//     const spill = [], clip = [];
//     for (const el of document.querySelectorAll('button, a, input, [role="tab"]')) {
//       const cs = getComputedStyle(el), h = parseFloat(cs.height);
//       if (el.clientHeight > 0 && el.scrollHeight > h + 1) spill.push(el.textContent);
//       if (el.clientWidth > 0 && el.scrollWidth > el.clientWidth + 1) clip.push(el.textContent);
//     }
//     return { spill, clip };
//   })()
//
// Note 800px is the narrowest width worth testing: below 768 the app switches
// to its handheld viewer, which hides most of these buttons entirely.

describe("label button contract", () => {
  for (const [name, style] of Object.entries(LABEL_BUTTONS)) {
    describe(name, () => {
      it("never wraps its label", () => {
        expect(style.whiteSpace).toBe("nowrap");
      });

      it("does not shrink -- the row wraps instead", () => {
        expect(style.flexShrink).toBe(0);
      });

      // The safety net. A fixed height is what let wrapped text render through
      // the border; minHeight grows the box instead.
      it("sizes with minHeight, never a fixed height", () => {
        expect(style.height).toBeUndefined();
        expect(typeof style.minHeight).toBe("number");
      });
    });
  }

  it("covers every label button the app uses", () => {
    // Fails loudly if a style is added to the module but not to the family,
    // which would otherwise slip past every assertion above.
    expect(Object.keys(LABEL_BUTTONS).sort()).toEqual([
      "authBtn",
      "ghostBtn",
      "ghostBtnSm",
      "primaryBtn",
      "primaryBtnSm",
      "rowBtn",
    ]);
  });
});

describe("icon button contract", () => {
  for (const [name, style] of Object.entries(ICON_BUTTONS)) {
    describe(name, () => {
      // Icon buttons keep a fixed square on purpose: no label to wrap, and they
      // sit in stacks where an odd one out would be obvious.
      it("is a fixed square", () => {
        expect(style.width).toBe(style.height);
        expect(typeof style.height).toBe("number");
      });

      it("does not shrink", () => {
        expect(style.flexShrink).toBe(0);
      });
    });
  }
});
