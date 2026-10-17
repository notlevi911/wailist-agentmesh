import { describe, it, expect } from "vitest";
import { buildNodeCatalog, type CatalogTemplate } from "./nodeCatalog";
import { ACTION_TEMPLATES, TOOL_TEMPLATES } from "./data";

const catalog = buildNodeCatalog();

function tpl(type: string, id: string): CatalogTemplate {
  const t = catalog.types.find((x) => x.type === type);
  const found = t?.templates.find((x) => x.id === id);
  if (!found) throw new Error(`no ${type}/${id} in the catalog`);
  return found;
}

describe("buildNodeCatalog", () => {
  // The bug that started this: the builder invented a cron trigger.
  it("offers no cron or schedule trigger", () => {
    const triggers = catalog.types.find((t) => t.type === "trigger")!;
    const ids = triggers.templates.map((t) => t.id);
    expect(ids).not.toContain("cron");
    expect(ids).not.toContain("schedule");
    expect(ids).toEqual(["manual", "chat", "webhook"]);
  });

  it("tells the builder where a timetable actually lives", () => {
    expect(tpl("trigger", "manual").note).toMatch(/schedule/i);
  });

  // The other bug: a JSON Extract node with nowhere to put its path.
  it("gives json_extract its path field", () => {
    const fields = tpl("tool", "json_extract").fields;
    expect(fields.some((f) => f.key === "jsonPath" && f.where === "config")).toBe(true);
  });

  it("covers every palette tool and action", () => {
    const tools = catalog.types.find((t) => t.type === "tool")!.templates.map((t) => t.id);
    const actions = catalog.types.find((t) => t.type === "action")!.templates.map((t) => t.id);
    expect(tools).toEqual(TOOL_TEMPLATES.map((t) => t.id));
    expect(actions).toEqual(ACTION_TEMPLATES.map((t) => t.id));
  });

  it("includes the state and google types the builder could not reach", () => {
    const types = catalog.types.map((t) => t.type);
    expect(types).toContain("state");
    expect(types).toContain("google");
  });

  // Without these presets a "Write State" node silently runs get, and a
  // Tendril node fails with "unknown action".
  it("carries the palette's presets", () => {
    expect(tpl("state", "set").presets).toEqual({ stateOp: "set" });
    expect(tpl("tendril", "tendril_rent").presets?.tendrilAction).toBe("rent");
    expect(tpl("provider", "gemini").presets).toEqual({ model: "gemini-2.5-flash" });
  });

  it("keeps automatic Tendril rentals on the same default budget as the palette", () => {
    const auto = tpl("tendril", "tendril_auto");
    expect(auto.presets?.tendrilAction).toBe("auto");
    expect(auto.presets?.tendrilAmount).toBe("");
    expect(auto.fields.find((field) => field.key === "tendrilAmount")).toMatchObject({
      where: "field", placeholder: "1",
    });
    expect(tpl("tendril", "tendril_topup").presets?.tendrilAmount).toBe("10");
  });

  it("marks every credential as a secret, never a settable field", () => {
    expect(tpl("action", "email").fields.find((f) => f.key === "emailApiKey")?.where).toBe("secret");
    expect(tpl("provider", "openai").fields.find((f) => f.key === "apiKey")?.where).toBe("secret");
    expect(tpl("tool", "http").fields.find((f) => f.key === "httpHeadersJSON")?.where).toBe("secret");
    expect(tpl("action", "slack").fields.find((f) => f.key === "slackWebhookURL")?.where).toBe("secret");
  });

  it("points connectors at where their credential comes from", () => {
    expect(tpl("action", "slack").authDocUrl).toMatch(/^https:\/\//);
  });

  it("warns that router and human agents are plain agents", () => {
    expect(tpl("agent", "router").note).toMatch(/no routing/i);
    expect(tpl("agent", "human").note).toMatch(/no approval/i);
  });

  it("gives Google nodes their account connection", () => {
    const f = tpl("google", "gmail_send").fields.find((x) => x.key === "oauthCredentialID");
    expect(f?.where).toBe("connection");
  });
});
