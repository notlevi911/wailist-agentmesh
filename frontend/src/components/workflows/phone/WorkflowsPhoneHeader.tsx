"use client";
import { useRouter } from "next/navigation";
import { useCredits } from "@/lib/credits/store";
import { formatDollars } from "@/lib/workflowMeta";

const usd = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
});

// The top of the phone Workflows screen: the title with a way to top up, then
// what the workspace holds on the left and what is left to spend on the right.
// The page reads the balance on mount and on pull-to-refresh; this only shows
// it.
export function WorkflowsPhoneHeader({
  total,
  shown,
  spend,
  known = true,
  spendKnown = true,
  showBalance = true,
}: {
  total: number;
  // How many survive the search and filter. Saying "1 of 6" keeps the count
  // from contradicting the list underneath it.
  shown: number;
  // Dollars spent across every workflow, over the same 30 days the list counts.
  spend: number;
  // False until the list has loaded, and after it failed to. Zeros then would
  // read as an empty account rather than one that could not be read.
  known?: boolean;
  // Separate from `known` because the two can disagree: the list itself
  // arrives fine while the runs/spend aggregation behind it fails, and then
  // the count is real but the total is not.
  spendKnown?: boolean;
  showBalance?: boolean;
}) {
  const router = useRouter();
  const { balanceUSD, balanceKnown } = useCredits();
  const narrowed = shown !== total;
  const count = narrowed ? `${shown} of ${total}` : `${total} total`;
  // Spoken in place of the visible text, so it has to say the same thing.
  const spokenCount = narrowed
    ? `${shown} of ${total} workflows shown`
    : `${total} workflows`;
  const spent = formatDollars(spend);
  const summary = !known
    ? "Workflows not loaded"
    : spendKnown
      ? `${spokenCount}, ${spent} spent in the last 30 days`
      : `${spokenCount}, spend not loaded`;
  const balance = balanceKnown ? usd.format(balanceUSD) : "-";
  return (
    <header className="wfp-head">
      <div className="wfp-head__top">
        <h1 className="wfp-title">Workflows</h1>
      </div>
      <div className="wfp-head__facts">
        <span className="wfp-head__summary" aria-label={summary}>
          {known ? count : "-"} <span aria-hidden>·</span>{" "}
          {known && spendKnown ? spent : "-"} spent
        </span>
        {/* Two dollar figures on one line: this one says which it is. It is
            also the way to top up: the balance is what adding credits
            changes, so tapping it opens Credits. A separate "Add credits"
            button beside the title looked out of place on a screen this
            quiet. The chevron says it can be tapped; the label says where
            it goes.

            The spoken name starts with exactly what is shown ("Credit
            $12.50"), so someone using voice control can say what they see
            (WCAG 2.5.3, label in name). */}
        {showBalance && <button
          type="button"
          className="wfp-head__balance"
          aria-label={
            balanceKnown
              ? `Credit ${balance}, add credits`
              : `Credit ${balance}, balance not loaded yet, add credits`
          }
          onClick={() => router.push("/billing")}
        >
          <span className="wfp-head__balance-label" aria-hidden>
            Credit
          </span>{" "}
          {balance}
          <span className="wfp-head__balance-chevron" aria-hidden>
            ›
          </span>
        </button>}
      </div>
    </header>
  );
}
