import { describe, it, expect } from "vitest";
import { PALETTE_TABS, CREATE_META, type TabId } from "./paletteTabs";
import { STATE_TEMPLATES } from "@/lib/data";

const TAB_IDS = PALETTE_TABS.map((t) => t.id);

describe("CREATE_META covers every tab", () => {
  // The bug behind issue #198: CREATE_META had no "state" key, so selecting the
  // State tab rendered <CreateRow meta={undefined}> and threw on `meta.name`,
  // taking the whole canvas route down. Record<string, T> hid it from tsc --
  // indexing by string yields T, never T | undefined -- so pin it in a test as
  // well as in the type.
  it.each(TAB_IDS)("has a create-row for the %s tab", (id) => {
    expect(CREATE_META[id]).toBeDefined();
  });

  // These are the exact expressions PalettePanel reads off the create-row: the
  // card's headline, and the aria-label on the button. Both must resolve to
  // real text for every tab, not just be non-undefined.
  it.each(TAB_IDS)("resolves a display name for the %s tab", (id) => {
    const meta = CREATE_META[id];
    expect(meta.name ?? meta.label).toBeTruthy();
    expect(meta.sub).toBeTruthy();
  });

  // Every create-row drops a node, so it must carry the type the tab is for --
  // a mismatch would silently add the wrong node type to the canvas.
  it.each(PALETTE_TABS.map((t) => [t.id, t.type] as const))(
    "drops a %s create-row as a %s node",
    (id, type) => {
      expect(CREATE_META[id].type).toBe(type);
    },
  );

  // The other direction: no metadata left behind for a tab that no longer
  // exists (as would have happened to "tendril" without the keyed Record).
  it("has no create-row for a tab that does not exist", () => {
    for (const key of Object.keys(CREATE_META)) {
      expect(TAB_IDS).toContain(key as TabId);
    }
  });
});

describe("the state tab", () => {
  // A dropped state node should already know its operation so the inspector
  // opens asking only for a key -- true for the four templates via `map`, and
  // it has to be true of the custom create-row too.
  it("gives the create-row a concrete default operation", () => {
    expect(CREATE_META.state.stateOp).toBe("get");
    expect(CREATE_META.state.template).toBe("get");
  });

  it("maps each template to its matching stateOp", () => {
    const tab = PALETTE_TABS.find((t) => t.id === "state")!;
    const mapped = STATE_TEMPLATES.map(
      tab.map as (it: (typeof STATE_TEMPLATES)[0]) => { stateOp?: string },
    );
    expect(mapped.map((m) => m.stateOp)).toEqual([
      "get",
      "set",
      "increment",
      "delete",
    ]);
  });
});

describe("the tendril tab", () => {
  // Issue #198 part 2: renting machines moved to the Tendril console, so the
  // library no longer offers tendril nodes. Existing saved workflows still
  // render and edit theirs -- TENDRIL_TEMPLATES stays exported for
  // nodes/index.tsx and Inspector.tsx -- this only removes the way to make a
  // new one from the palette.
  it("is gone from the tab strip", () => {
    expect(TAB_IDS).not.toContain("tendril");
  });

  it("is gone from the create-rows", () => {
    expect(Object.keys(CREATE_META)).not.toContain("tendril");
  });
});
