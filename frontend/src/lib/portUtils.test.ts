import { describe, expect, it } from "vitest";
import { isValidConnection } from "./portUtils";
import { WorkflowNode } from "./types";

function node(id: string, type: WorkflowNode["type"]): WorkflowNode {
  return { id, type, x: 0, y: 0 };
}

describe("isValidConnection", () => {
  // tool/tool402 are both attach-eligible (bottom "model"/"tools" ports on
  // an agent) AND flow-eligible (feeding an agent's "in" port directly, as
  // a standalone trigger→tool→agent step) -- the toPort actually being
  // dragged onto must decide which rule applies, not just the from/to
  // node types, or the attach check swallows the flow case before it's
  // ever reached.
  it("allows a tool dragged onto an agent's flow 'in' port", () => {
    expect(isValidConnection(node("t1", "tool"), "out", node("a1", "agent"), "in")).toBe(true);
  });

  it("allows a tool402 dragged onto an agent's flow 'in' port", () => {
    expect(isValidConnection(node("t1", "tool402"), "out", node("a1", "agent"), "in")).toBe(true);
  });

  it("still allows a tool attached onto an agent's 'tools' port", () => {
    expect(isValidConnection(node("t1", "tool"), "top", node("a1", "agent"), "tools")).toBe(true);
  });

  it("still allows a provider attached onto an agent's 'model' port", () => {
    expect(isValidConnection(node("p1", "provider"), "top", node("a1", "agent"), "model")).toBe(true);
  });

  it("rejects a provider dragged onto an agent's flow 'in' port (provider stays attach-only)", () => {
    expect(isValidConnection(node("p1", "provider"), "top", node("a1", "agent"), "in")).toBe(false);
  });

  it("rejects a node type that isn't attach-eligible onto an agent's 'model' port", () => {
    expect(isValidConnection(node("g1", "google"), "top", node("a1", "agent"), "model")).toBe(false);
  });
});

// The agent's two bottom sockets take different things. They used to share
// one rule accepting provider|tool|tool402 into either, so the canvas allowed
// wiring that graph.go then dropped at run time with only a server-side log
// line -- the edge saved, the run behaved as though it were not there.
describe("agent attach ports are not interchangeable", () => {
  const agent = node("a", "agent");
  const src = (type: WorkflowNode["type"]) => node(type, type);

  it("rejects flow and input ports used as attachment sources", () => {
    for (const fromPort of ["in", "out", "model", "tools"] as const) {
      expect(isValidConnection(src("provider"), fromPort, agent, "model")).toBe(false);
      expect(isValidConnection(src("tool"), fromPort, agent, "tools")).toBe(false);
    }
  });

  it("requires the flow output while retaining State connections", () => {
    expect(isValidConnection(src("tool"), "top", agent, "in")).toBe(false);
    expect(isValidConnection(src("state"), "in", src("end"), "in")).toBe(false);
    expect(isValidConnection(src("state"), "out", src("end"), "in")).toBe(true);
    expect(isValidConnection(src("trigger"), "out", src("state"), "in")).toBe(true);
  });

  it("takes only a provider in the model socket", () => {
    expect(isValidConnection(src("provider"), "top", agent, "model")).toBe(true);
    expect(isValidConnection(src("tool"), "top", agent, "model")).toBe(false);
    expect(isValidConnection(src("tool402"), "top", agent, "model")).toBe(false);
    expect(isValidConnection(src("tendril"), "top", agent, "model")).toBe(false);
    expect(isValidConnection(src("google"), "top", agent, "model")).toBe(false);
    expect(isValidConnection(src("action"), "top", agent, "model")).toBe(false);
  });

  it("takes only tools in the tools socket", () => {
    expect(isValidConnection(src("tool"), "top", agent, "tools")).toBe(true);
    expect(isValidConnection(src("tool402"), "top", agent, "tools")).toBe(true);
    expect(isValidConnection(src("provider"), "top", agent, "tools")).toBe(false);
    expect(isValidConnection(src("tendril"), "top", agent, "tools")).toBe(false);
    expect(isValidConnection(src("google"), "top", agent, "tools")).toBe(false);
  });

  it("refuses either socket on a node that is not an agent", () => {
    for (const port of ["model", "tools"] as const) {
      expect(isValidConnection(src("provider"), "top", src("action"), port)).toBe(false);
      expect(isValidConnection(src("tool"), "top", src("end"), port)).toBe(false);
    }
  });

  // The ordering guard: these two are attachable AND valid flow sources, so
  // splitting the attach branch must not have cost them the flow case.
  it("still lets a tool sit in the flow chain", () => {
    expect(isValidConnection(src("tool402"), "out", agent, "in")).toBe(true);
    expect(isValidConnection(src("tool"), "out", src("end"), "in")).toBe(true);
  });
});
