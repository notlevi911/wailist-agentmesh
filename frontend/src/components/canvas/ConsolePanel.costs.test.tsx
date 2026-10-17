import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ConsolePanel } from "./ConsolePanel";
import type { LogEvent } from "./useRunTranscript";
import type { RunCosts } from "@/lib/runCosts";

const costs: RunCosts = {
  totalUsdMicros: 1_565_000,
  steps: [{ nodeId: "step", totalUsdMicros: 1_565_000,
    byKind: { x402_relay_cost: 65_000, x402_platform_fee: 1_500_000 } }],
};
const log = (status: LogEvent["status"], stepIndex = 0): LogEvent => ({
  nodeId: "step", nodeType: "tool", status, stepIndex,
  output: "result", durationMs: 10, ts: "2026-10-01T12:00:00Z",
});
function panel(logs: LogEvent[], runCosts: RunCosts | null) {
  const el = document.createElement("div");
  el.innerHTML = renderToStaticMarkup(<ConsolePanel open onToggle={() => {}}
    runId="run1" running={false} logs={logs} elapsed={1} done
    deadLetters={[]} costs={runCosts} onResume={() => {}} />);
  return el;
}

describe("console cost integration", () => {
  it("preserves the partial-answer warning alongside the charge", () => {
    const el = panel([log("degraded")], costs);
    expect(el.textContent).toContain("1 step failed, this answer is partial");
    expect(el.textContent).toContain("0/1 nodes succeeded");
    expect(el.textContent).toContain("$1.57 charged");
    expect(el.textContent).not.toContain("run complete");
    expect(el.textContent).not.toContain("run failed");
  });

  it("shows a resumed node's aggregate charge only on its last attempt", () => {
    const el = panel([log("failed"), log("success", 1)], costs);
    expect(el.textContent).toContain("run complete");
    expect(el.textContent).toContain("1/1 nodes succeeded");
    expect(el.textContent).not.toContain("run failed");
    const charges = el.querySelectorAll('[title="platform fee $1.50 · API cost $0.07"]');
    expect(charges).toHaveLength(1);
    expect(charges[0].textContent).toBe("· $1.57");
    expect(el.textContent).toContain("$1.57 charged");
  });

  it("keeps the existing summary when an older server omits costs", () => {
    const el = panel([log("success")], null);
    expect(el.textContent).toContain("run complete");
    expect(el.textContent).toContain("1/1 nodes succeeded");
    expect(el.textContent).not.toContain("charged");
    expect(el.textContent).not.toContain("$");
  });
});
