"use client";
import { useState, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Logo, IconArrow, Tag } from "@/components/ui";
import { SessionPersistError, useAuth } from "@/hooks/useAuth";
import { auth } from "@/lib/api";
import { IS_NATIVE } from "@/lib/nativeAuth";
import { safeNextPath } from "@/lib/routes";
import { authBtn } from "@/components/ui/buttons";

const OAUTH_ERRORS: Record<string, string> = {
  invalid_state: "Login session expired. Please try again.",
  no_code: "Provider did not return an authorization code.",
  token_exchange: "Could not complete sign in with the provider.",
  no_email: "Could not read a verified email from the provider.",
  account_exists:
    "An account with this email already exists. Sign in with your email and password.",
  user_upsert: "Could not create your account. Please try again.",
  token_issue: "Could not issue a session. Please try again.",
  internal: "Something went wrong. Please try again.",
  oauth: "Sign in was cancelled or failed.",
};

// The same idea as OAUTH_ERRORS, for the email and password form.
//
// Everything here failed generically before, including a wrong password --
// which is the single most common thing that happens on this screen, and
// "Something went wrong. Please try again." is close to the least useful thing
// to say about it. Someone who mistyped a password retries the same password.
//
// Still an allowlist, and deliberately so. The rule this screen keeps is that a
// server string is never echoed at whoever is standing in front of it: an
// unrecognised error falls through to the generic message, so a new backend
// error can only ever make this vaguer, never leak. Keys are the exact strings
// backend/internal/api/handlers/auth.go returns; a rename there makes this
// generic again rather than breaking it.
const FORM_ERRORS: Record<string, string> = {
  "invalid credentials": "That email and password do not match.",
  "email already registered":
    "An account with this email already exists. Try signing in instead.",
  "valid email required": "Enter a valid email address.",
  "password must be at least 8 characters":
    "Passwords need to be at least 8 characters.",
  "email and password required": "Enter your email and password.",
  "name required": "Enter your name.",
};

type Mode = "signin" | "signup";

const DEFAULT_DEST = "/workflows";

// middleware redirects protected deep links here as ?next=<path>, so this value
// is attacker-controlled: anyone can hand out /signin?next=<anywhere>. Only a
// same-origin absolute path is allowed through -- "//evil.com" is protocol-
// relative and "https://evil.com" absolute, and either would turn the sign-in
// form into an open redirect that lands a just-authenticated user off-site.
// Backslashes are rejected too: browsers normalize them to forward slashes for
// http(s) URLs, so "/\evil.com" resolves exactly like "//evil.com" and would
// otherwise slip past the checks above.
function safeNext(raw: string | null): string {
  return safeNextPath(raw) ?? DEFAULT_DEST;
}

function nextPath(): string {
  if (typeof window === "undefined") return DEFAULT_DEST;
  return safeNext(new URLSearchParams(window.location.search).get("next"));
}

interface AuthPageProps {
  initialMode?: Mode;
}

export function AuthPage({ initialMode = "signin" }: AuthPageProps) {
  const router = useRouter();
  const { signIn, signUp } = useAuth();
  const [mode, setMode] = useState<Mode>(initialMode);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [org, setOrg] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [showPassword, setShowPassword] = useState(false);

  // Surface OAuth failures the backend redirected back with (?error=...).
  //
  // Keyed on the query rather than read once on mount: in the app a failed
  // sign-in comes back to this screen while it is still open (lib/nativeNav.ts
  // routes it in place), so the reason arrives after mount.
  const code = useSearchParams().get("error");
  useEffect(() => {
    if (code) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- post-mount URL read; a lazy initializer would render the error on the server and break hydration
      setError(OAUTH_ERRORS[code] ?? "Something went wrong. Please try again.");
      // Drop only ?error= -- rewriting to a bare pathname would also discard the
      // ?next= deep link the user is still trying to reach after a failed OAuth
      // attempt, sending them to /workflows once they retry with a password.
      const url = new URL(window.location.href);
      url.searchParams.delete("error");
      window.history.replaceState({}, "", url.pathname + url.search + url.hash);
    }
  }, [code]);

  const handleOAuth = (provider: "github" | "google") => {
    const url = auth.oauthURL(provider);
    if (!url) {
      setError("Social sign in is not configured.");
      return;
    }
    // In the app this must NOT be a page navigation. It would take the WebView
    // off its own https://localhost origin -- blocked by the native CSP, and
    // when it is not blocked, the app bundle is gone with no way back but
    // killing the app. Google refuses OAuth in an embedded WebView anyway.
    // native/oauth.ts opens a Custom Tab instead, and the answer returns as a
    // deep link that boot()'s listener handles, so nothing resumes here.
    if (IS_NATIVE) {
      void import("@/native/oauth")
        .then(({ start }) => start(provider, nextPath()))
        .catch((err) =>
          setError(
            err instanceof Error ? err.message : "Could not open sign in.",
          ),
        );
      return;
    }
    window.location.href = url;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError("");
    try {
      if (mode === "signin") {
        await signIn(email, password);
      } else {
        await signUp(email, password, name, org);
      }
      router.push(nextPath());
    } catch (err) {
      // Generic by default, on purpose: this screen must not echo a server or
      // developer string back at whoever is standing in front of it.
      // SessionPersistError is the exception -- its text is written for the
      // user, it names something only they can act on (sign in again, restart
      // the app), and the sign-in has already been rolled back by the time it
      // arrives, so saying "something went wrong" would leave them retrying a
      // password that was never the problem.
      setError(
        err instanceof SessionPersistError
          ? err.message
          : ((err instanceof Error ? FORM_ERRORS[err.message] : undefined) ??
              "Something went wrong. Please try again."),
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      className="auth-grid"
      style={{
        background: "var(--bg)",
      }}
    >
      {/* Left -- form */}
      <div
        className="auth-form-col"
        style={{
          display: "flex",
          flexDirection: "column",
          background: "var(--bg)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
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
          <div
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: "var(--t-1)",
              color: "var(--fg-dim)",
              whiteSpace: "nowrap",
              flexShrink: 0,
            }}
          >
            v0.4 · testnet
          </div>
        </div>

        <div
          style={{
            flex: 1,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            marginTop: 16,
            marginBottom: 16,
          }}
        >
          <div className="auth-card reveal">
            <h1
              style={{
                margin: 0,
                // Was a flat 32px at every width. On a 320px screen that is
                // most of the line before the sentence has said anything.
                // Floor, slope, ceiling -- the shape the landing hero already
                // uses, an order of magnitude smaller.
                fontSize: "clamp(24px, 7vw, 32px)",
                fontWeight: 500,
                letterSpacing: "-0.025em",
              }}
            >
              {mode === "signin" ? "Welcome back." : "Create your account."}
            </h1>
            <p style={{ marginTop: 8, color: "var(--fg-muted)", fontSize: "var(--t-4)" }}>
              {mode === "signin"
                ? "Sign in to your AgentMesh workspace."
                : "Free testnet access. Mainnet by invite."}
            </p>

            <form
              onSubmit={handleSubmit}
              style={{
                marginTop: 32,
                display: "flex",
                flexDirection: "column",
                gap: "var(--s-4)",
              }}
            >
              {mode === "signup" && (
                <FormField label="Full name">
                  <input
                    style={inputStyle}
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Ada Lovelace"
                  />
                </FormField>
              )}
              {mode === "signup" && (
                <FormField label="Organization">
                  <input
                    style={inputStyle}
                    value={org}
                    onChange={(e) => setOrg(e.target.value)}
                    placeholder="Acme Capital"
                  />
                </FormField>
              )}
              <FormField label="Work email">
                <input
                  style={inputStyle}
                  type="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@company.com"
                />
              </FormField>
              <FormField
                label="Password"
                hint={
                  mode === "signin" ? (
                    <span
                      style={{ color: "var(--fg-muted)", cursor: "pointer" }}
                    >
                      Forgot?
                    </span>
                  ) : (
                    <span
                      style={{
                        color: "var(--fg-dim)",
                        fontFamily: "var(--font-mono)",
                        fontSize: "var(--t-1)",
                      }}
                    >
                      min 12 chars
                    </span>
                  )
                }
              >
                {/* The reveal toggle sits inside the field, so the input keeps
                    room for it rather than running under the button. */}
                <div style={{ position: "relative" }}>
                  <input
                    style={{ ...inputStyle, paddingRight: 60 }}
                    type={showPassword ? "text" : "password"}
                    required
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="•••••••••••"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-pressed={showPassword}
                    aria-label={
                      showPassword ? "Hide password" : "Show password"
                    }
                    style={{
                      position: "absolute",
                      top: 0,
                      right: 0,
                      // Matches the field it sits on, which is now the 44px
                      // floor. At 38 tall and ~40 wide this was the smallest
                      // target on the screen.
                      height: 44,
                      minWidth: 44,
                      padding: "0 10px",
                      background: "transparent",
                      border: "none",
                      color: "var(--fg-muted)",
                      fontSize: "var(--t-1)",
                      fontFamily: "var(--font-mono)",
                      textTransform: "uppercase",
                      letterSpacing: "0.06em",
                      cursor: "pointer",
                    }}
                  >
                    {showPassword ? "Hide" : "Show"}
                  </button>
                </div>
              </FormField>

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
                disabled={loading}
                style={{
                  // 42 was two pixels under the floor, which is the least
                  // defensible way to miss it.
                  height: 44,
                  marginTop: 12,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: "var(--s-2)",
                  background: "var(--accent)",
                  color: "var(--accent-fg)",
                  border: "none",
                  borderRadius: "var(--r-2)",
                  fontSize: "var(--t-4)",
                  fontWeight: 600,
                  fontFamily: "var(--font-sans)",
                  cursor: loading ? "not-allowed" : "pointer",
                  opacity: loading ? 0.7 : 1,
                }}
              >
                {loading
                  ? "Please wait…"
                  : mode === "signin"
                    ? "Sign in"
                    : "Create account"}
                {!loading && <IconArrow size={13} />}
              </button>

              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "var(--s-3)",
                  margin: "8px 0",
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--t-1)",
                  color: "var(--fg-dim)",
                }}
              >
                <div
                  style={{ flex: 1, height: 1, background: "var(--border)" }}
                />
                <span>or</span>
                <div
                  style={{ flex: 1, height: 1, background: "var(--border)" }}
                />
              </div>

              <button
                type="button"
                onClick={() => handleOAuth("github")}
                style={authBtn}
              >
                <span style={{ fontFamily: "var(--font-mono)" }}>⌘</span>{" "}
                Continue with GitHub
              </button>
              <button
                type="button"
                onClick={() => handleOAuth("google")}
                style={authBtn}
              >
                <span style={{ color: "var(--accent)" }}>⬡</span> Continue with
                Google
              </button>
            </form>

            <div
              style={{
                marginTop: 32,
                fontSize: "var(--t-3)",
                color: "var(--fg-muted)",
                textAlign: "center",
              }}
            >
              {mode === "signin" ? (
                <>
                  New here?{" "}
                  <button
                    onClick={() => setMode("signup")}
                    style={{
                      background: "transparent",
                      border: "none",
                      color: "var(--accent)",
                      cursor: "pointer",
                      fontSize: "var(--t-3)",
                      fontFamily: "var(--font-sans)",
                      padding: 0,
                    }}
                  >
                    Create an account
                  </button>
                </>
              ) : (
                <>
                  Have an account?{" "}
                  <button
                    onClick={() => setMode("signin")}
                    style={{
                      background: "transparent",
                      border: "none",
                      color: "var(--accent)",
                      cursor: "pointer",
                      fontSize: "var(--t-3)",
                      fontFamily: "var(--font-sans)",
                      padding: 0,
                    }}
                  >
                    Sign in
                  </button>
                </>
              )}
            </div>
          </div>
        </div>

        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-1)",
            color: "var(--fg-dim)",
            display: "flex",
            justifyContent: "space-between",
          }}
        >
          <span>Google and GitHub sign-in</span>
          <span>Email sign-in</span>
        </div>
      </div>

      {/* Right -- visual */}
      <div
        className="auth-aside"
        style={{
          background: "var(--bg-elev-1)",
          borderLeft: "1px solid var(--border)",
          position: "relative",
          overflow: "hidden",
          backgroundImage:
            "radial-gradient(var(--border-strong) 1px, transparent 1px)",
          backgroundSize: "20px 20px",
        }}
      >
        <AuthVisual />
      </div>
    </div>
  );
}

function AuthVisual() {
  const cards = [
    {
      kicker: "ai agent",
      name: "Support Triage",
      sub: "Gemini · 2 tools",
      tone: "accent",
      delay: "0s",
    },
    {
      kicker: "x402 tool",
      name: "Quote API",
      sub: "Pay per request",
      tone: "magenta",
      delay: "0.15s",
    },
    {
      kicker: "provider",
      name: "Google Gemini",
      sub: "Gemini 2.5 Flash",
      tone: "default",
      delay: "0.30s",
    },
  ];

  return (
    <div
      style={{
        position: "absolute",
        inset: 0,
        padding: 48,
        display: "flex",
        flexDirection: "column",
        justifyContent: "space-between",
      }}
    >
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "var(--s-4)",
          alignItems: "flex-end",
        }}
      >
        {cards.map((c, i) => {
          const accent =
            c.tone === "magenta"
              ? "var(--type-x402)"
              : c.tone === "accent"
                ? "var(--accent)"
                : "var(--fg-muted)";
          const borderColor =
            c.tone === "default"
              ? "var(--border)"
              : `color-mix(in oklab, ${accent} 30%, var(--border))`;
          return (
            <div
              key={i}
              style={{
                width: 280,
                background: "var(--bg-elev-2)",
                border: `1px solid ${borderColor}`,
                borderRadius: "var(--r-3)",
                padding: "12px 14px",
                boxShadow: "0 8px 32px rgba(0,0,0,0.4)",
                opacity: 0,
                animation: `fade-up 0.6s var(--ease) ${c.delay} forwards, float-y 4s ease-in-out ${c.delay} infinite`,
              }}
            >
              <div
                style={{
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--t-1)",
                  color: accent,
                  textTransform: "uppercase",
                  letterSpacing: "0.08em",
                }}
              >
                {c.kicker}
              </div>
              <div style={{ marginTop: 4, fontSize: "var(--t-4)", fontWeight: 500 }}>
                {c.name}
              </div>
              <div
                style={{
                  marginTop: 2,
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--t-1)",
                  color: "var(--fg-muted)",
                }}
              >
                {c.sub}
              </div>
            </div>
          );
        })}
      </div>

      <div className="reveal reveal-delay-3" style={{ maxWidth: 480 }}>
        <Tag>Example workflow</Tag>
        <div
          style={{
            marginTop: 14,
            fontSize: "var(--t-7)",
            fontWeight: 500,
            letterSpacing: "-0.025em",
            lineHeight: 1.15,
          }}
        >
          Build workflows visually.
          <span style={{ color: "var(--accent)" }}>
            {" "}
            Track each run and its cost.
          </span>
        </div>
        <div
          style={{
            marginTop: 16,
            fontFamily: "var(--font-mono)",
            fontSize: "var(--t-1)",
            color: "var(--fg-dim)",
          }}
        >
          x402 API tools · live run logs
        </div>
      </div>
    </div>
  );
}

function FormField({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: "var(--s-2)" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          fontFamily: "var(--font-mono)",
          fontSize: "var(--t-1)",
          color: "var(--fg-muted)",
          textTransform: "uppercase",
          letterSpacing: "0.08em",
        }}
      >
        <span>{label}</span>
        {hint && (
          <span style={{ textTransform: "none", letterSpacing: 0 }}>
            {hint}
          </span>
        )}
      </div>
      {children}
    </label>
  );
}

const inputStyle: React.CSSProperties = {
  // 44px is this app's touch floor -- the same one .am-sheet-grip holds to and
  // responsive.css names twice. An input is a tap target before it is a box.
  height: 44,
  padding: "0 12px",
  width: "100%",
  background: "var(--bg-elev-1)",
  border: "1px solid var(--border)",
  borderRadius: "var(--r-2)",
  color: "var(--fg)",
  // 16px, and not a pixel less, on every pointer. Safari on iOS zooms the page
  // when a field smaller than 16px takes focus, and layout.tsx leaves
  // `maximumScale` unset on purpose -- so the page cannot refuse the zoom, and
  // the only lever left is the font size. It was 13.
  //
  // Unconditional rather than behind `(pointer: coarse)`, for two reasons. The
  // width lives in this object, and a media query in a stylesheet cannot beat
  // an inline style; and there is no `pointer: coarse` block in responsive.css
  // to extend -- `lib/device.ts` uses that query from JavaScript, which is a
  // different thing. Making it conditional would mean either a dead CSS rule
  // or a JS round trip, to keep a 3px difference nobody asked for.
  fontSize: "var(--t-4)",
  fontFamily: "var(--font-sans)",
  outline: "none",
};
