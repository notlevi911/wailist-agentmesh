"use client";

import { useRouter } from "next/navigation";
import { IconWallet } from "@/components/ui";
import { UpcomingRuns } from "@/components/runs/UpcomingRuns";
import { useCredits } from "@/lib/credits/store";
import styles from "./WorkflowOverview.module.css";

export function WorkflowOverview() {
  const router = useRouter();
  const { balanceUSD, balanceKnown } = useCredits();

  return (
    <div className={styles.overview}>
      <section className={styles.credit} aria-label="Credit balance">
        <div className={styles.creditHeading}>
          <span className={styles.creditLabel}>
            <IconWallet size={13} /> Credit balance
          </span>
          <button
            type="button"
            className={styles.creditAdd}
            aria-label="Add credits"
            title="Add credits"
            onClick={() => router.push("/billing")}
          >
            <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>
        </div>
        <p className={styles.creditBalance}>
          {balanceKnown ? `$${balanceUSD.toFixed(2)}` : "-"}
        </p>
        <p className={styles.creditHint}>
          {balanceKnown ? "Spent as your agents call paid tools and models." : "Loading balance…"}
        </p>
      </section>
      <UpcomingRuns limit={2} title="Upcoming runs" presentation="overview" />
    </div>
  );
}
