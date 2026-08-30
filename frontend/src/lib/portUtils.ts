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
  provider: { top: (W) => ({ x: W / 2, y: 0 }) },
  tool: { top: (W) => ({ x: W / 2, y: 0 }) },
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

// kind picks which of tool402's two source ports an edge rendered from —
// "top" when it's attached to an agent as a tool, "out" when it's a flow
// step. Every other type has exactly one source port regardless of kind.
export function portForFrom(
  n: WorkflowNode,
  kind: EdgeKind = "flow",
): PortName {
  if (kind === "attach") return "top";
  if (
    n.type === "trigger" ||
    n.type === "agent" ||
    n.type === "action" ||
    n.type === "state"
  )
    return "out";
  if (n.type === "provider" || n.type === "tool" || n.type === "tool402")
    return "top";
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
  // attach: provider/tool/tool402 → agent bottom ports
  if (
    (from.type === "provider" ||
      from.type === "tool" ||
      from.type === "tool402") &&
    to.type === "agent"
  ) {
    return toPort === "model" || toPort === "tools";
  }
  // flow: trigger/agent/action/state/tool402 → agent/action/state/end/tool402
  // tool402 is included on both sides so an x402 endpoint can sit directly
  // in the flow chain (e.g. trigger → tool402 → end), not just hang off an
  // agent as an attached tool — the runner already executes tool402 as a
  // standalone step (see runner.go's NodeTypeTool402 case).
  if (toPort === "in") {
    return (
      (
        ["trigger", "agent", "action", "state", "tool402"] as NodeType[]
      ).includes(from.type) &&
      (["agent", "action", "state", "end", "tool402"] as NodeType[]).includes(
        to.type,
      )
    );
  }
  return false;
}
