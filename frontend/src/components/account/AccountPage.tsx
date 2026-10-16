"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Topbar } from "@/components/Topbar";
import { NotificationsSheet } from "@/components/notifications/NotificationsSheet";
import { useAuth } from "@/hooks/useAuth";
import { useCredits } from "@/lib/credits/store";
import { IS_NATIVE } from "@/lib/nativeAuth";

// The account as a tab: who is signed in, what is left to spend, and the
// places that hang off an account. The top bar's account menu stays as well;
// this is the same set of things where a thumb can reach them.
export function AccountPage() {
  const router = useRouter();
  const { user, signOut } = useAuth();
  const { balanceUSD, balanceKnown, refreshBalance } = useCredits();
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const notificationsRef = useRef<HTMLButtonElement>(null);

  // Re-read on open, so spend from runs made elsewhere shows here.
  useEffect(() => {
    void refreshBalance();
  }, [refreshBalance]);

  const name = user?.name?.trim() || "-";
  const email = user?.email ?? "-";
  const orgName = user?.orgName?.trim() || "Personal workspace";
  const initial = (
    user?.name?.trim()?.[0] ??
    user?.email?.trim()?.[0] ??
    "?"
  ).toUpperCase();

  const handleSignOut = async () => {
    await signOut();
    // replace, not push: Back from the signed-out screen would otherwise
    // return to a page this account can no longer load.
    router.replace("/signin");
  };

  return (
    <div
      className="am-viewport"
      style={{
        height: "100dvh",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background: "var(--bg)",
      }}
    >
      <Topbar />
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <main style={page}>
          <h1 style={title}>Account</h1>

          <section aria-label="Profile" style={profile}>
            <span aria-hidden="true" style={avatar}>
              {initial}
            </span>
            <span style={{ minWidth: 0 }}>
              <span style={profileName}>{name}</span>
              <span style={profileLine}>{email}</span>
              <span style={profileLine}>{orgName}</span>
            </span>
          </section>

          <ul style={list}>
            <li>
              <Link href="/settings#delete-title" className="account-row" style={row}>
                <span>Delete account</span>
                <Chevron />
              </Link>
            </li>
            <li>
              <Link href="/billing" className="account-row" style={row}>
                <span>Credits</span>
                <span style={rowValue}>
                  {balanceKnown ? `$${balanceUSD.toFixed(2)}` : "-"}
                  <Chevron />
                </span>
              </Link>
            </li>
            {/* No Usage row: Usage is a tab of its own on the bottom bar. */}
            {/* Native only, as in the top bar's menu: a browser has no push
                token to register. */}
            {IS_NATIVE && (
              <li>
                <button
                  type="button"
                  ref={notificationsRef}
                  className="account-row"
                  style={row}
                  onClick={() => setNotificationsOpen(true)}
                >
                  <span>Notifications</span>
                  <span style={rowValue}>
                    <Chevron />
                  </span>
                </button>
              </li>
            )}
            {/* On both web and native, deliberately: Google Play requires the
                privacy policy to be reachable from inside the app, and on the
                web it is simply useful. Not gated like Notifications. */}
            <li>
              <Link href="/privacy" className="account-row" style={row}>
                <span>Privacy Policy</span>
                <span style={rowValue}>
                  <Chevron />
                </span>
              </Link>
            </li>
          </ul>

          <button
            type="button"
            className="account-row"
            style={{ ...row, marginTop: 24, color: "var(--danger)" }}
            onClick={handleSignOut}
          >
            Sign out
          </button>
        </main>
      </div>

      <style>{ACCOUNT_CSS}</style>
      {notificationsOpen && (
        <NotificationsSheet
          onClose={() => setNotificationsOpen(false)}
          returnFocusTo={notificationsRef}
        />
      )}
    </div>
  );
}

function Chevron() {
  return (
    <span aria-hidden="true" style={{ color: "var(--fg-dim)", fontSize: "var(--t-4)" }}>
      ›
    </span>
  );
}

// Press feedback and focus rings, which inline styles cannot express.
const ACCOUNT_CSS = `
.account-row {
  transition: background 0.12s var(--ease), transform 0.12s var(--ease);
}
.account-row:active { transform: scale(0.98); }
.account-row:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
@media (prefers-reduced-motion: reduce) {
  .account-row { transition: none; }
  .account-row:active { transform: none; }
}
`;

const page: React.CSSProperties = {
  maxWidth: 560,
  margin: "0 auto",
  padding: "20px 16px 40px",
};

const title: React.CSSProperties = {
  font: "600 20px/1.3 var(--font-sans)",
  color: "var(--fg)",
  margin: 0,
};

const profile: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "var(--s-4)",
  margin: "20px 0 24px",
  padding: "14px 12px",
  borderRadius: "var(--r-2)",
  border: "1px solid var(--border)",
  background: "var(--bg-elev-1)",
};

const avatar: React.CSSProperties = {
  width: 40,
  height: 40,
  borderRadius: "var(--r-full)",
  background: "var(--accent)",
  color: "var(--accent-fg)",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  font: "700 15px/1 var(--font-sans)",
  flexShrink: 0,
};

const profileName: React.CSSProperties = {
  display: "block",
  font: "600 15px/1.3 var(--font-sans)",
  color: "var(--fg)",
  overflowWrap: "anywhere",
};

const profileLine: React.CSSProperties = {
  display: "block",
  font: "400 12px/1.5 var(--font-sans)",
  color: "var(--fg-muted)",
  overflowWrap: "anywhere",
};

const list: React.CSSProperties = {
  listStyle: "none",
  margin: 0,
  padding: 0,
  display: "grid",
  gap: "var(--s-2)",
};

const row: React.CSSProperties = {
  width: "100%",
  minHeight: 52,
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: "var(--s-4)",
  padding: "0 14px",
  borderRadius: "var(--r-2)",
  border: "1px solid var(--border)",
  background: "var(--bg-elev-1)",
  color: "var(--fg)",
  font: "500 14px/1.3 var(--font-sans)",
  textAlign: "left",
  textDecoration: "none",
  cursor: "pointer",
};

const rowValue: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "var(--s-3)",
  font: "500 13px/1 var(--font-mono)",
  fontVariantNumeric: "tabular-nums",
  color: "var(--fg-muted)",
};
