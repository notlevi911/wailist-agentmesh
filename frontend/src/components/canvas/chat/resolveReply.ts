import { settledUsdOf, type LogEvent, type X402Payment } from "../useRunTranscript";

// Turns a run's raw log events into the one thing a non-technical reader
// actually wants: the agent's answer, plus a one-line summary of what it cost.
//
// This exists because the answer was already in the transcript and was being
// shown as a collapsed "▸ response · 1.2 KB" toggle -- correct for a debugger,
// useless for someone who just asked a question. Everything here is a pure
// function of the logs so it can be unit-tested without a live run.

export interface RunSummary {
  /** The prose to show in the assistant bubble. */
  text: string;
  /** True when the run failed; the bubble renders in the danger tone. */
  isError: boolean;
  /** Successful tool / tool402 steps, for the activity strip. */
  toolCount: number;
  /** Total settled spend in USD across every x402 payment in the run. */
  spendUSD: number;
}

/**
 * Pull display text out of one node's output.
 *
 * Agent outputs come in three shapes, all produced by ExecuteAgent:
 *   - a bare string (BYOK, no payments)
 *   - { message, x402Payments? }
 *   - { message, x402Payments?, platformKeyUsage } (platform key)
 * so `.message` is checked before falling back to the whole payload.
 */
function textFromOutput(output: unknown): string {
  if (output === null || output === undefined) return "";
  if (typeof output === "string") return output;
  if (typeof output === "object") {
    const rec = output as Record<string, unknown>;
    for (const key of ["message", "text", "output", "answer"]) {
      const v = rec[key];
      if (typeof v === "string" && v.trim() !== "") return v;
    }
    // A paid step nests the endpoint's own body under `response`; only worth
    // showing once the agent-shaped keys above have all missed.
    if (typeof rec.response === "string") return rec.response;
    try {
      return JSON.stringify(output, null, 2);
    } catch {
      return "";
    }
  }
  return String(output);
}

/** Best-effort human-readable error out of a failed step's output. */
function errorFromOutput(output: unknown): string {
  if (typeof output === "string" && output.trim() !== "") return output;
  if (output && typeof output === "object") {
    const rec = output as Record<string, unknown>;
    for (const key of ["error", "message", "reason"]) {
      const v = rec[key];
      if (typeof v === "string" && v.trim() !== "") return v;
    }
  }
  return "The run failed without returning a reason.";
}

/**
 * Total USD charged for one step, 0 when the step paid for nothing.
 *
 * Delegates to settledUsdOf so the activity strip and the usage page's
 * settlement rows can never disagree about what a run cost again.
 */
function spendOf(log: LogEvent): number {
  return settledUsdOf(log.output) ?? 0;
}

/**
 * Sum settled spend across a run's logs, deduplicated by tx id.
 *
 * A run-level pre-fund publishes one funding row carrying the FULL amount
 * that actually settled on-chain, then every tool call it covers repeats
 * that same tx id with just its own slice (see runner.go's
 * prependRunFundingReceipt) -- summing every row naively double- or
 * triple-counts the same money. First occurrence wins, matching
 * lib/settlements.ts, since the funding row is always published first and
 * carries the accurate total. Rows with no tx id (a v2/relay settlement that
 * returned none) are never deduplicated against each other -- each is still
 * a real, distinct charge.
 */
function totalSpend(logs: LogEvent[]): number {
  const seenTxIds = new Set<string>();
  let total = 0;
  for (const l of logs) {
    const usd = spendOf(l);
    if (usd === 0) continue;
    const txId = (l.output as Partial<X402Payment> | null)?.txId;
    if (txId) {
      if (seenTxIds.has(txId)) continue;
      seenTxIds.add(txId);
    }
    total += usd;
  }
  return total;
}

/**
 * Collapse `logs` to each node's most recent entry, in original (chronological)
 * order.
 *
 * A resumed run re-executes any node that previously dead-lettered, which
 * logs a SECOND entry for that same node id rather than replacing the first
 * (see engine.Runner.execute -> InsertRunLog: every attempt gets its own
 * row, GetRunLogs returns the full history). Without this collapse, a node
 * that failed on attempt 1 and then succeeded on the resumed attempt still
 * has a "failed" entry sitting in `logs` -- and the headline/answer search
 * below, which only cares about the FINAL outcome per node, would find that
 * stale failure and report the whole run as failed even though it went on
 * to succeed.
 *
 * Exported for any other consumer that computes a run-level summary from
 * the same `logs` array (e.g. ConsolePanel's own terminal banner) -- a
 * second, un-collapsed scan over `logs` would reintroduce exactly this bug
 * for that consumer alone.
 */
export function latestPerNode(logs: LogEvent[]): LogEvent[] {
  const lastIndexOfNode = new Map<string, number>();
  logs.forEach((l, i) => lastIndexOfNode.set(l.nodeId, i));
  return logs.filter((l, i) => lastIndexOfNode.get(l.nodeId) === i);
}

export function resolveReply(logs: LogEvent[]): RunSummary {
  const toolCount = logs.filter(
    (l) =>
      (l.nodeType === "tool" || l.nodeType === "tool402") &&
      l.status === "success" &&
      !(l.output as Partial<X402Payment> | null)?.isFundingReceipt,
  ).length;
  const spendUSD = totalSpend(logs);

  // Only each node's final outcome decides the headline/answer -- see
  // latestPerNode's doc comment for why a raw scan over `logs` is wrong once
  // a resumed run can log the same node twice.
  const final = latestPerNode(logs);

  // A failure is the headline: showing an earlier node's partial output as if
  // it were the answer would be a lie about what happened.
  const failed = [...final].reverse().find((l) => l.status === "failed");
  if (failed) {
    return {
      text: errorFromOutput(failed.output),
      isError: true,
      toolCount,
      spendUSD,
    };
  }

  // The agent's answer is the reply whenever there is one. Search backwards:
  // a workflow with several agents ends on the last one to speak.
  const agent = [...final]
    .reverse()
    .find((l) => l.nodeType === "agent" && l.status === "success");
  if (agent) {
    const text = textFromOutput(agent.output);
    if (text.trim() !== "") {
      return { text, isError: false, toolCount, spendUSD };
    }
  }

  // No agent ran (or it answered with nothing) -- fall back to whatever the
  // last successful step produced, so a non-agent workflow still says
  // something rather than rendering an empty bubble.
  const last = [...final].reverse().find((l) => l.status === "success");
  const fallback = last ? textFromOutput(last.output) : "";
  return {
    text: fallback.trim() === "" ? "The run finished." : fallback,
    isError: false,
    toolCount,
    spendUSD,
  };
}
