"use client";
import { useEffect } from "react";
import { useRouter } from "next/navigation";

// The backend OAuth flow now sets an HttpOnly cookie and redirects straight to
// /workflows. This page is only reached when that redirect fails or the user
// lands here directly (e.g. stale bookmark). Just forward them on.
export default function AuthCallbackPage() {
  const router = useRouter();

  useEffect(() => {
    router.replace("/workflows");
  }, [router]);

  return (
    <div
      style={{
        height: "100dvh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "var(--bg)",
        color: "var(--fg-muted)",
        fontFamily: "var(--font-mono)",
        fontSize: 13,
      }}
    >
      Signing you in…
    </div>
  );
}
