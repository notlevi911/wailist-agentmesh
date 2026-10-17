import { WorkflowNode } from "@/lib/types";
import {
  TRIGGER_TEMPLATES,
  AGENT_TEMPLATES,
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  ACTION_TEMPLATES,
  STATE_TEMPLATES,
  END_TEMPLATES,
  GOOGLE_TEMPLATES,
} from "@/lib/data";

// The Library panel's tab strip and, for each tab, how one of its templates
// becomes a droppable node. Kept out of PalettePanel.tsx because it is plain
// data with plain mappers -- no JSX -- and vitest only collects `*.test.ts`,
// so living here is what makes it testable at all (same split as
// panelSizing.ts / viewport.ts and their tests).
export const PALETTE_TABS = [
  {
    id: "triggers",
    label: "Triggers",
    items: () => TRIGGER_TEMPLATES,
    type: "trigger",
    dotColor: "mute" as const,
    map: (it: (typeof TRIGGER_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "trigger",
      template: it.id,
      label: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "agents",
    label: "Agents",
    items: () => AGENT_TEMPLATES,
    type: "agent",
    dotColor: "accent" as const,
    map: (it: (typeof AGENT_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "agent",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "providers",
    label: "Providers",
    items: () => PROVIDER_TEMPLATES,
    type: "provider",
    dotColor: "accent" as const,
    map: (it: (typeof PROVIDER_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "provider",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.model,
      model: it.model,
    }),
  },
  {
    id: "tools",
    label: "Tools",
    items: () => TOOL_TEMPLATES,
    type: "tool",
    dotColor: "mute" as const,
    map: (it: (typeof TOOL_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "tool",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    // No preset list: every x402 endpoint is different money moving to a
    // different real place, so this tab offers only the "New x402
    // Endpoint" custom creator below (paste URL -> Discover probes the
    // live 402 challenge for method/price). A prior preset list here
    // (Tavily/Firecrawl/etc.) pointed at invented hostnames nothing
    // real answers to -- removed rather than fixed in place.
    id: "x402",
    label: "x402",
    items: () => [] as never[],
    type: "tool402",
    dotColor: "magenta" as const,
    map: (): Partial<WorkflowNode> => ({ type: "tool402" }),
  },
  {
    id: "actions",
    label: "Actions",
    items: () => ACTION_TEMPLATES,
    type: "action",
    dotColor: "mute" as const,
    map: (it: (typeof ACTION_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "action",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "state",
    label: "State",
    items: () => STATE_TEMPLATES,
    type: "state",
    dotColor: "info" as const,
    map: (it: (typeof STATE_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "state",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
      // The dropped node already knows its operation -- the palette entry
      // IS the choice of operation, so the inspector opens on a node that
      // only needs a key, not a mode decision first.
      stateOp: it.id as NonNullable<WorkflowNode["stateOp"]>,
    }),
  },
  {
    id: "google",
    label: "Google",
    items: () => GOOGLE_TEMPLATES,
    type: "google",
    dotColor: "accent" as const,
    map: (it: (typeof GOOGLE_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "google",
      template: it.id,
      name: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
  {
    id: "end",
    label: "End",
    items: () => END_TEMPLATES,
    type: "end",
    dotColor: "mute" as const,
    map: (it: (typeof END_TEMPLATES)[0]): Partial<WorkflowNode> => ({
      type: "end",
      template: it.id,
      label: it.name,
      icon: it.icon,
      sub: it.desc,
    }),
  },
] as const;

export type TabId = (typeof PALETTE_TABS)[number]["id"];

// The dashed "+" create-row at the top of each tab. Keyed by TabId rather than
// by `string`: a Record<string, T> hands back T (not T | undefined) for any
// key, which is exactly how the missing "state" entry shipped as a render-time
// TypeError instead of a build failure. Keyed this way, adding a tab without a
// create-row -- or leaving one behind after removing a tab -- does not compile.
export const CREATE_META: Record<TabId, Partial<WorkflowNode>> = {
  triggers: {
    type: "trigger",
    custom: true,
    label: "Custom Trigger",
    icon: "✳",
    sub: "define your own",
  },
  agents: {
    type: "agent",
    custom: true,
    name: "Custom Agent",
    icon: "◇",
    sub: "your own agent",
    systemPrompt: "You are a helpful agent.",
  },
  providers: {
    type: "provider",
    custom: true,
    name: "Custom Provider",
    icon: "+",
    sub: "model + API key",
    model: "custom-model",
  },
  tools: {
    type: "tool",
    custom: true,
    name: "Custom Tool",
    icon: "⟶",
    sub: "any HTTP endpoint",
  },
  x402: {
    type: "tool402",
    custom: true,
    name: "New x402 Endpoint",
    icon: "✦",
    sub: "paste URL · auto-price",
  },
  actions: {
    type: "action",
    custom: true,
    name: "Custom Action",
    icon: "✦",
    sub: "your own action",
  },
  // Like Google below, the four state operations are already listed
  // individually, so this create-row defaults to the most common one (read).
  // stateOp is set for the same reason the state tab's `map` sets it: a node
  // that lands on the canvas should already know its operation, leaving the
  // inspector to ask only for a key.
  state: {
    type: "state",
    custom: true,
    name: "Custom Read State",
    icon: "▤",
    sub: "or drag a specific op below",
    template: "get",
    stateOp: "get",
  },
  // Google has no "custom operation" concept -- all 11 real ones (Gmail/
  // Sheets/Calendar/Drive) are already listed individually below. This
  // create-row defaults to the most common one (send) purely so dragging
  // the dashed "+" card behaves consistently with every other tab; picking
  // a different Google row below is the normal way to get a different op.
  google: {
    type: "google",
    custom: true,
    name: "Custom Gmail Send",
    icon: "✉",
    sub: "or drag a specific op below",
    template: "gmail_send",
  },
  end: {
    type: "end",
    custom: true,
    label: "Custom End",
    icon: "■",
    sub: "define your own",
  },
};
