"use client";
import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useRouter, usePathname } from "next/navigation";
import { Logo, Hairline, ghostBtnSm } from "@/components/ui";
import { useAuth } from "@/hooks/useAuth";
import { AppNav } from "@/components/nav/AppNav";
import { APP_NAV_ITEMS, type NavItem } from "@/lib/nav";
import { useIsHandheld } from "@/hooks/useIsHandheld";
import { IS_NATIVE } from "@/lib/nativeAuth";
import { NotificationsSheet } from "@/components/notifications/NotificationsSheet";

// Shared application top bar. Rendered identically on every authed page so the
// brand cluster, primary navigation, and account menu never drift between routes.
export function Topbar() {
  const router = useRouter();
  const pathname = usePathname();
  const { signOut, user, completeOnboarding } = useAuth();
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  // The account button, so the notifications sheet can hand focus back to
  // something that still exists. The menu item that opens it does not: the
  // menu closes in the same update.
  const menuTriggerRef = useRef<HTMLButtonElement>(null);

  // Avatar shows the first letter of the signed-in user's name, falling back
  // to the email local part while auth is still loading or for an OAuth
  // account that hasn't completed onboarding yet.
  const initial = (
    user?.name?.trim()?.[0] ??
    user?.email?.trim()?.[0] ??
    "?"
  ).toUpperCase();

  const orgName = user?.orgName?.trim() || "Personal workspace";

  // Account menu opens two ways: hovering with a mouse (soft — closes shortly
  // after the pointer leaves the avatar and panel) or clicking/tapping (pinned —
  // survives mouse-leave, closes on outside press, Escape, or item selection).
  // Touch pointers skip the hover path entirely, so mobile is tap-only.
  const [menuState, setMenuState] = useState<"closed" | "hover" | "pinned">(
    "closed",
  );
  const menuOpen = menuState !== "closed";
  const menuRef = useRef<HTMLDivElement>(null);
  const hoverCloseTimer = useRef<number | null>(null);

  const cancelHoverClose = useCallback(() => {
    if (hoverCloseTimer.current != null) {
      window.clearTimeout(hoverCloseTimer.current);
      hoverCloseTimer.current = null;
    }
  }, []);

  const onMenuPointerEnter = (e: React.PointerEvent) => {
    if (e.pointerType === "touch") return;
    cancelHoverClose();
    setMenuState((s) => (s === "closed" ? "hover" : s));
  };
  // Grace delay so a brief slip off the menu doesn't snap it shut.
  const onMenuPointerLeave = (e: React.PointerEvent) => {
    if (e.pointerType === "touch") return;
    cancelHoverClose();
    hoverCloseTimer.current = window.setTimeout(() => {
      setMenuState((s) => (s === "hover" ? "closed" : s));
    }, 160);
  };

  useEffect(() => cancelHoverClose, [cancelHoverClose]);

  useEffect(() => {
    if (!menuOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuState("closed");
    };
    const onPointer = (e: PointerEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node))
        setMenuState("closed");
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [menuOpen]);

  // The sheet on a phone and the inline links on a desktop come from the same
  // manifest, so a route marked desktopOnly is dropped here rather than in
  // lib/nav.ts, which stays a plain list.
  const handheld = useIsHandheld();
  const navItems = useMemo(
    () =>
      handheld ? APP_NAV_ITEMS.filter((i) => !i.desktopOnly) : APP_NAV_ITEMS,
    [handheld],
  );

  const handleSignOut = async () => {
    await signOut();
    // replace, not push: Back from the signed-out screen would otherwise
    // return to a page this account can no longer load.
    router.replace("/signin");
  };

  return (
    <>
      <AppNav
        items={navItems}
        pathname={pathname}
        onSelect={(item: NavItem, options) => {
          if (item.href) {
            if (options?.replace) router.replace(item.href);
            else router.push(item.href);
          }
        }}
        renderInlineLink={({ item, active, onClick }) => (
          <NavLink label={item.label} active={active} onClick={onClick} />
        )}
        brand={
          <div
            style={{ display: "flex", alignItems: "center", gap: "var(--s-4)" }}
          >
            <button
              onClick={() => router.push("/")}
              style={{
                background: "transparent",
                border: "none",
                cursor: "pointer",
                padding: 0,
              }}
            >
              <Logo size={18} />
            </button>
            {/* The workspace switcher drops out on narrow screens — it is the
              widest element in the cluster and the least load-bearing — and
              the divider before it goes too, so the logo is not left trailing
              a rule with nothing after it. */}
            <Hairline className="hide-md" vertical length={22} />
            <div
              className="hide-md"
              style={{
                display: "flex",
                alignItems: "center",
                gap: "var(--s-3)",
              }}
            >
              <button style={ghostBtnSm}>{orgName} ▾</button>
            </div>
          </div>
        }
        actions={
          <>
            {/* Separates wayfinding from identity. Drops out with the inline
              links, so the avatar is not left trailing a stray rule. */}
            <Hairline className="hide-md" vertical length={22} />
            <div
              className="profile-menu"
              ref={menuRef}
              onPointerEnter={onMenuPointerEnter}
              onPointerLeave={onMenuPointerLeave}
            >
              <button
                className="profile-menu__trigger"
                ref={menuTriggerRef}
                aria-haspopup="true"
                aria-expanded={menuOpen}
                aria-label="Account menu"
                onClick={() =>
                  setMenuState((s) => (s === "pinned" ? "closed" : "pinned"))
                }
              >
                {initial}
              </button>
              {menuOpen && (
                <div className="profile-menu__panel">
                  <div className="profile-menu__card">
                    <div
                      style={{
                        padding: "12px 14px",
                        display: "flex",
                        alignItems: "center",
                        gap: "var(--s-3)",
                      }}
                    >
                      <div
                        style={{
                          width: 28,
                          height: 28,
                          borderRadius: "var(--r-full)",
                          background: "var(--accent)",
                          color: "var(--accent-fg)",
                          display: "inline-flex",
                          alignItems: "center",
                          justifyContent: "center",
                          fontSize: "var(--t-1)",
                          fontWeight: 700,
                          flexShrink: 0,
                        }}
                      >
                        {initial}
                      </div>
                      <div style={{ minWidth: 0 }}>
                        <div
                          style={{
                            fontSize: "var(--t-3)",
                            fontWeight: 600,
                            color: "var(--fg)",
                          }}
                        >
                          {user?.name?.trim() || "-"}
                        </div>
                        <div
                          style={{
                            fontSize: "var(--t-1)",
                            color: "var(--fg-dim)",
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {user?.email ?? "-"}
                        </div>
                      </div>
                    </div>
                    <div className="profile-menu__divider" />
                    <button
                      className="profile-menu__item"
                      onClick={() => {
                        setMenuState("closed");
                        router.push("/settings");
                      }}
                    >
                      Settings
                    </button>
                    <button
                      className="profile-menu__item"
                      onClick={() => {
                        setMenuState("closed");
                        router.push("/settings#delete-title");
                      }}
                    >
                      Delete account
                    </button>
                    {IS_NATIVE && (
                      <>
                        <div className="profile-menu__divider" />
                        <button
                          className="profile-menu__item"
                          onClick={() => {
                            setMenuState("closed");
                            setNotificationsOpen(true);
                          }}
                        >
                          Notifications
                        </button>
                      </>
                    )}
                    <div className="profile-menu__divider" />
                    <button
                      className="profile-menu__item profile-menu__item--danger"
                      onClick={() => {
                        setMenuState("closed");
                        handleSignOut();
                      }}
                    >
                      Sign out
                    </button>
                  </div>
                </div>
              )}
            </div>
          </>
        }
      />
      {user?.needsOnboarding && (
        <OnboardingModal onComplete={completeOnboarding} />
      )}
      {notificationsOpen && (
        <NotificationsSheet
          onClose={() => setNotificationsOpen(false)}
          returnFocusTo={menuTriggerRef}
        />
      )}
    </>
  );
}

// Shown once, right after a first-time OAuth sign-in — Google/GitHub only
// hand back a verified email, never a name or organization, so the account
// exists with both blank (see backend Me's needsOnboarding) until the user
// fills this in. Not dismissable: there's no cancel/skip, since every other
// account (password signup) already has both by the time it can sign in.
function OnboardingModal({
  onComplete,
}: {
  onComplete: (name: string, org: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [org, setOrg] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setSaving(true);
    setError("");
    try {
      await onComplete(name.trim(), org.trim());
    } catch {
      setError("Something went wrong. Please try again.");
      setSaving(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Finish setting up your account"
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0, 0, 0, 0.5)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1000,
        // Keeps the form clear of the system bars and screen edges. On a
        // landscape phone the form is taller than the screen and scrolls.
        padding:
          "calc(16px + var(--safe-top)) calc(16px + var(--safe-right)) calc(16px + var(--safe-bottom)) calc(16px + var(--safe-left))",
      }}
    >
      <form
        onSubmit={handleSubmit}
        style={{
          width: "min(340px, 100%)",
          maxHeight: "100%",
          overflowY: "auto",
          boxSizing: "border-box",
          background: "var(--bg-elev-1)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-2)",
          padding: 24,
          display: "flex",
          flexDirection: "column",
          gap: "var(--s-4)",
        }}
      >
        {/* Was "Welcome — one more step" over "Tell us who you are so your
            teammates recognize you." Two lines for a form whose first field
            is already labelled "Full name": the heading named no task and
            the caption restated the label under it. */}
        <div
          style={{
            fontSize: "var(--t-4)",
            fontWeight: 600,
            color: "var(--fg)",
          }}
        >
          Add your name
        </div>
        <label
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "var(--s-2)",
          }}
        >
          <span style={{ fontSize: "var(--t-2)", color: "var(--fg-muted)" }}>
            Full name
          </span>
          <input
            autoFocus
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Ada Lovelace"
            className="am-touch"
            style={onboardingInputStyle}
          />
        </label>
        <label
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "var(--s-2)",
          }}
        >
          <span style={{ fontSize: "var(--t-2)", color: "var(--fg-muted)" }}>
            Organization (optional)
          </span>
          <input
            value={org}
            onChange={(e) => setOrg(e.target.value)}
            placeholder="Acme Capital"
            className="am-touch"
            style={onboardingInputStyle}
          />
        </label>
        {error && (
          <div
            style={{
              color: "var(--danger)",
              fontSize: "var(--t-2)",
              fontFamily: "var(--font-mono)",
            }}
          >
            {error}
          </div>
        )}
        <button
          type="submit"
          className="am-touch"
          disabled={saving || !name.trim()}
          style={{
            height: 38,
            marginTop: 4,
            background: "var(--accent)",
            color: "var(--accent-fg)",
            border: "none",
            borderRadius: "var(--r-2)",
            fontSize: "var(--t-3)",
            fontWeight: 600,
            fontFamily: "var(--font-sans)",
            cursor: saving || !name.trim() ? "not-allowed" : "pointer",
            opacity: saving || !name.trim() ? 0.7 : 1,
          }}
        >
          {saving ? "Saving…" : "Continue"}
        </button>
      </form>
    </div>
  );
}

const onboardingInputStyle: React.CSSProperties = {
  height: 36,
  padding: "0 10px",
  background: "var(--bg-elev-2)",
  border: "1px solid var(--border)",
  borderRadius: "var(--r-2)",
  color: "var(--fg)",
  fontSize: "var(--t-3)",
  fontFamily: "var(--font-sans)",
};

// Top-bar navigation link. Active route is filled + full-contrast; others are
// muted and lighten on hover, so the bar always signals "you are here".
function NavLink({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      className="am-nav-link"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      // Mouse only: on a tablet, where these links show, a tap would leave the
      // link highlighted.
      onPointerEnter={(e) => {
        if (!active && e.pointerType === "mouse")
          e.currentTarget.style.background = "var(--bg-elev-2)";
      }}
      onMouseLeave={(e) => {
        if (!active) e.currentTarget.style.background = "transparent";
      }}
      style={{
        fontSize: "var(--t-2)",
        fontWeight: 500,
        background: active ? "var(--bg-elev-3)" : "transparent",
        border: "none",
        borderRadius: "var(--r-2)",
        color: active ? "var(--fg)" : "var(--fg-muted)",
        cursor: "pointer",
        fontFamily: "var(--font-sans)",
        display: "inline-flex",
        alignItems: "center",
        gap: "var(--s-2)",
        transition: "background .15s var(--ease), color .15s var(--ease)",
      }}
    >
      {label}
    </button>
  );
}
