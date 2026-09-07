import { WorkflowNode, NodeType, PortCoord, PortName, EdgeKind } from "./types";
import { NODE_TYPES } from "./data";

type PortFn = (W: number, H: number) => PortCoord;

const PORT_POS: Record<NodeType, Partial<Record<PortName, PortFn>>> = {
  trigger: { out: (W, H) => ({ x: W, y: H / 2 }) },
  agent: {
    in: () => ({ x: 0, y: 38 }),
    out: (W) => ({ x: W, y: 38 }),
    model: (W, H) => ({ x: W * 0.28, y: H }),
    tools: (W, H) => ({ x: W * 0.72, y: H }),
  },
  // provider is deliberately attach-only: as a standalone flow step the
  // backend's NodeTypeProvider case is a no-op passthrough (runner.go just
  // returns rc.Message() -- it never actually calls the LLM outside of an
  // agent attaching it), so giving it in/out ports would let you draw a
  // flow connection that silently does nothing.
  provider: { top: (W) => ({ x: W / 2, y: 0 }) },
  // tool is dual-port like tool402/tendril below: "top" to attach to an
  // agent, "in"/"out" to sit directly in a flow chain -- ExecuteTool (the
  // real backend dispatch for calc/datetime/http) runs identically either
  // way, so both wirings are genuinely functional, not just visually legal.
  tool: {
    top: (W) => ({ x: W / 2, y: 0 }),
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
  // tool402 can be wired either way: "top" as a tool attached to an agent
  // (existing behavior), or "in"/"out" as a standalone step directly in the
  // trigger→…→end flow chain — the node already renders both sets of ports.
  tool402: {
    top: (W) => ({ x: W / 2, y: 0 }),
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
  action: {
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
  // state is a plain flow step, like action: it reads or writes one saved
  // value and passes control on. It never attaches to an agent.
  state: {
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
  end: { in: (W, H) => ({ x: 0, y: H / 2 }) },
  // Flow-only like action -- Gmail/Sheets/Calendar/Drive nodes were never
  // made agent-attachable on the backend (only provider/tool/tool402 are
  // attach-eligible below), so there's no "top" port to offer here.
  google: {
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
  // Same dual-port shape as tool402: "top" for a future agent attach, "in"/
  // "out" for the standalone trigger→rent→end flow this ships today.
  tendril: {
    top: (W) => ({ x: W / 2, y: 0 }),
    in: (W, H) => ({ x: 0, y: H / 2 }),
    out: (W, H) => ({ x: W, y: H / 2 }),
  },
};

export function portWorld(node: WorkflowNode, port: PortName): PortCoord {
  const t = NODE_TYPES[node.type];
  if (!t) return { x: node.x, y: node.y };
  const portMap = PORT_POS[node.type];
  const fn = portMap?.[port];
  if (!fn) return { x: node.x + t.w / 2, y: node.y + t.h / 2 };
  const p = fn(t.w, t.h);
  return { x: node.x + p.x, y: node.y + p.y };
}

// kind picks which source port an edge rendered from for dual-port types
// (tool, tool402, tendril): "top" when attached to an agent, "out" when it's
// a flow step -- every other type has exactly one source port regardless of
// kind, and "out" is correct for all of them too.
//
// This used to return "top" for provider/tool/tool402/tendril unconditionally,
// even when kind was "flow" -- so a tool402/tendril flow edge rendered
// starting from the top-center dot instead of the right-edge one, even
// though the connection itself was semantically valid. Fixed by only
// special-casing the "attach" kind.
export function portForFrom(n: WorkflowNode, kind: EdgeKind = "flow"): PortName {
  if (kind === "attach") return "top";
  return "out";
}

export function portForTo(n: WorkflowNode): PortName {
  if (
    n.type === "agent" ||
    n.type === "action" ||
    n.type === "state" ||
    n.type === "end"
  )
    return "in";
  return "in";
}

export function isValidConnection(
  from: WorkflowNode,
  fromPort: PortName,
  to: WorkflowNode,
  toPort: PortName,
): boolean {
  // attach: provider/tool/tool402 → agent bottom ports. Gated on toPort
  // first, not just from/to type -- tool and tool402 are ALSO valid flow
  // sources into an agent's "in" port (see the flow branch below), so
  // matching on from/to type alone would swallow that case here and return
  // false before the flow branch ever runs, even though it's listed as
  // valid there.
  if (toPort === "model" || toPort === "tools") {
    return (
      (from.type === "provider" ||
        from.type === "tool" ||
        from.type === "tool402") &&
      to.type === "agent"
    );
  }
  // flow: trigger/agent/action/state/tool/tool402/tendril → agent/action/
  // state/end/tool/tool402/tendril (in port). tool402 and tool are included
  // on both sides so an x402 endpoint or a plain tool (HTTP/calc/datetime)
  // can sit directly in the flow chain (e.g. trigger → tool → end), not
  // just hang off an agent as an attached tool — the runner already
  // executes both as a standalone step (see runner.go's NodeTypeTool402 and
  // NodeTypeTool cases). tendril mirrors it for the same reason: a
  // standalone Tendril workflow is trigger → rent → end, not an
  // agent-attached resource (agent-driven Tendril attach is deliberately
  // out of scope for now). state is a plain flow step like action -- never
  // attached to an agent. provider is deliberately excluded here -- see
  // PORT_POS's comment on it in portUtils.ts above: a standalone provider
  // flow step is a backend no-op, so it stays attach-only.
  if (toPort === "in") {
    return (
      (
        [
          "trigger",
          "agent",
          "action",
          "state",
          "tool",
          "tool402",
          "tendril",
          "google",
        ] as NodeType[]
      ).includes(from.type) &&
      (
        [
          "agent",
          "action",
          "state",
          "end",
          "tool",
          "tool402",
          "tendril",
          "google",
        ] as NodeType[]
      ).includes(to.type)
    );
  }
  return false;
}
