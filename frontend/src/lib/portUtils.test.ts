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
