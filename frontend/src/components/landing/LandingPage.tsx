"use client";
import { useRef, useEffect } from "react";
import { useRouter } from "next/navigation";
import { Logo, Tag, IconArrow } from "@/components/ui";
import { WAITLIST_COUNT } from "@/lib/data";
import { waitlist } from "@/lib/api";
import { AppNav } from "@/components/nav/AppNav";
import { LANDING_NAV_ITEMS } from "@/lib/nav";
import { siGithub } from "simple-icons";
import {
  SimpleIconMark,
  SlackMark,
  TendrilMark,
} from "@/components/brand/marks";

interface LandingPageProps {
  signedIn: boolean;
}

export function LandingPage({ signedIn }: LandingPageProps) {
  const router = useRouter();
  const scrollRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  // Fade video in/out at loop boundaries
  useEffect(() => {
    const v = videoRef.current;
    if (!v) return;
    let rafId: number;
    let cancelled = false;

    const tick = () => {
      if (cancelled || !v) return;
      const t = v.currentTime;
      const d = v.duration || 0;
      if (d > 0) {
        if (t < 0.5) v.style.opacity = String(t / 0.5);
        else if (t > d - 0.5)
          v.style.opacity = String(Math.max(0, (d - t) / 0.5));
        else v.style.opacity = "1";
      }
      rafId = requestAnimationFrame(tick);
    };

    v.style.opacity = "0";
    let restartId: ReturnType<typeof setTimeout> | undefined;
    const onPlay = () => {
      rafId = requestAnimationFrame(tick);
    };
    const onEnded = () => {
      v.style.opacity = "0";
      restartId = setTimeout(() => {
        if (!cancelled) {
          v.currentTime = 0;
          v.play().catch(() => {});
        }
      }, 100);
    };
    v.addEventListener("play", onPlay);
    v.addEventListener("ended", onEnded);
    v.play().catch(() => {});
    // Named handlers so the listeners (and the pending restart timer) are torn
    // down on unmount -- anonymous ones leaked across StrictMode re-mounts.
    return () => {
      cancelled = true;
      cancelAnimationFrame(rafId);
      clearTimeout(restartId);
      v.removeEventListener("play", onPlay);
      v.removeEventListener("ended", onEnded);
    };
  }, []);

  // IntersectionObserver for scroll-reveal
  useEffect(() => {
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((e) => {
          if (e.isIntersecting) e.target.classList.add("visible");
        });
      },
      { threshold: 0.12 },
    );
    document.querySelectorAll(".in-view").forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, []);

  const scrollToId = (id: string) => {
    const c = scrollRef.current;
    if (!c) return;
    const el = c.querySelector(`#${id}`);
    if (!el) return;
    c.scrollTo({
      top: Math.max(0, (el as HTMLElement).offsetTop - 16),
      behavior: "smooth",
    });
  };

  const openStudio = () => {
    if (signedIn) router.push("/workflows");
    else router.push("/signin");
  };

  return (
    <div
      style={{
        height: "100dvh",
        position: "relative",
        background: "hsl(260 90% 2%)",
        overflow: "hidden",
      }}
    >
      {/* Background video */}
      <video
        ref={videoRef}
        src="https://d8j0ntlcm91z4.cloudfront.net/user_38xzZboKViGWJOttwIXH07lWA1P/hf_20260328_065045_c44942da-53c6-4804-b734-f9e07fc22e08.mp4"
        muted
        playsInline
        autoPlay
        loop
        style={{
          position: "fixed",
          inset: 0,
          width: "100%",
          height: "100%",
          objectFit: "cover",
          opacity: 0,
          transition: "opacity 0.05s linear",
          zIndex: 0,
          pointerEvents: "none",
        }}
      />

      {/* Scroll container */}
      <div
        ref={scrollRef}
        style={{
          position: "relative",
          zIndex: 1,
          height: "100dvh",
          overflowY: "auto",
          overflowX: "hidden",
          scrollBehavior: "smooth",
        }}
      >
        <HeroSection
          openStudio={openStudio}
          signedIn={signedIn}
          scrollToId={scrollToId}
          scrollRef={scrollRef}
        />
        <LandingPillars />
        <LandingFlow />
        <LandingWaitlist />
        <LandingFooter />
      </div>
    </div>
  );
}

// ── Hero ──────────────────────────────────────────────────────────────────
function HeroSection({
  openStudio,
  signedIn,
  scrollToId,
  scrollRef,
}: {
  openStudio: () => void;
  signedIn: boolean;
  scrollToId: (id: string) => void;
  scrollRef: React.RefObject<HTMLDivElement | null>;
}) {
  const router = useRouter();

  return (
    <section
      style={{
        position: "relative",
        minHeight: "100dvh",
        display: "flex",
        flexDirection: "column",
        background: "transparent",
      }}
    >
      {/* Readability bloom behind headline */}
      <div
        style={{
          position: "absolute",
          top: "50%",
          left: "50%",
          transform: "translate(-50%, -50%)",
          width: 984,
          height: 527,
          opacity: 0.88,
          background: "hsl(260 60% 4%)",
          filter: "blur(82px)",
          pointerEvents: "none",
          zIndex: 1,
        }}
      />

      <div
        style={{
          position: "relative",
          zIndex: 10,
          display: "flex",
          flexDirection: "column",
          flex: 1,
          minHeight: "100dvh",
        }}
      >
        {/* Nav — above the hero content's z-index 12, because the open sheet is
            fixed over the whole viewport and this wrapper's z-index is the one
            that orders it against the headline. */}
        <div style={{ position: "relative", zIndex: 20 }}>
          <AppNav
            variant="landing"
            items={LANDING_NAV_ITEMS}
            pathname=""
            scrollContainer={scrollRef}
            onSelect={(item) => {
              if (item.sectionId) scrollToId(item.sectionId);
            }}
            brand={<Logo size={20} />}
            renderInlineLink={({ item, onClick }) => (
              <button
                onClick={onClick}
                style={{
                  background: "transparent",
                  border: "none",
                  cursor: "pointer",
                  color: "rgba(242, 240, 247, 0.9)",
                  fontSize: 14,
                  fontWeight: 400,
                  fontFamily: "var(--font-sans)",
                  padding: "8px 14px",
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 6,
                  whiteSpace: "nowrap",
                  transition: "color .15s",
                }}
                onMouseEnter={(e) => (e.currentTarget.style.color = "#fff")}
                onMouseLeave={(e) =>
                  (e.currentTarget.style.color = "rgba(242, 240, 247, 0.9)")
                }
              >
                {item.label}
              </button>
            )}
            actions={
              <>
                {/* Sign Up drops at the same width as the links and reappears
                    in the sheet with them; Open Studio is the one action that
                    stays in the bar at every width. */}
                {!signedIn && (
                  <button
                    onClick={() => router.push("/signup")}
                    className="liquid-glass hide-md"
                    style={{
                      padding: "8px 18px",
                      borderRadius: 999,
                      background: "rgba(255,255,255,0.04)",
                      color: "rgba(242, 240, 247, 0.92)",
                      fontSize: 13,
                      fontWeight: 500,
                      border: "none",
                      cursor: "pointer",
                      fontFamily: "var(--font-sans)",
                      whiteSpace: "nowrap",
                      flexShrink: 0,
                    }}
                  >
                    Sign Up
                  </button>
                )}
                <button
                  onClick={openStudio}
                  style={{
                    padding: "8px 18px",
                    borderRadius: 999,
                    background: "var(--accent)",
                    color: "var(--accent-fg)",
                    fontSize: 13,
                    fontWeight: 600,
                    border: "none",
                    cursor: "pointer",
                    fontFamily: "var(--font-sans)",
                    whiteSpace: "nowrap",
                    flexShrink: 0,
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 6,
                  }}
                >
                  Open Studio <IconArrow size={11} />
                </button>
              </>
            }
            sheetFooter={
              !signedIn ? (
                <button
                  className="appnav__link"
                  onClick={() => router.push("/signup")}
                >
                  Sign Up
                </button>
              ) : null
            }
          />
          <div
            style={{
              height: 1,
              marginTop: 3,
              background:
                "linear-gradient(90deg, transparent, rgba(242, 240, 247, 0.20), transparent)",
            }}
          />
        </div>

        {/* Hero content */}
        <div
          className="lp-hero"
          style={{
            flex: 1,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            position: "relative",
          }}
        >
          <div
            style={{
              position: "relative",
              zIndex: 12,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              textAlign: "center",
            }}
          >
            <h1
              style={{
                margin: 0,
                fontFamily: "var(--font-sans)",
                fontWeight: 400,
                // Only the floor was wrong. At 375px, 16vw is 60px but the old
                // 80px floor overrode it and pushed the wordmark edge to edge.
                // Slope and ceiling are untouched, so desktop is unchanged.
                fontSize: "clamp(44px, 16vw, 220px)",
                lineHeight: 1.02,
                letterSpacing: "-0.024em",
                color: "rgba(242, 240, 247, 0.98)",
              }}
            >
              Agent<span className="hero-gradient">Mesh</span>
            </h1>
            <p
              style={{
                margin: 0,
                marginTop: 9,
                maxWidth: 460,
                fontSize: 18,
                lineHeight: 1.55,
                color: "rgba(242, 240, 247, 0.82)",
                opacity: 0.85,
                fontFamily: "var(--font-sans)",
                letterSpacing: "-0.005em",
              }}
            >
              The visual canvas for autonomous{" "}
              {/* Forced break is a desktop typographic choice; on a phone it
                  fights the natural wrap, so it drops out below `sm`. The
                  explicit space above is load-bearing: with the <br> hidden
                  there is otherwise nothing separating the two words. */}
              <br className="hide-sm" />
              agent networks.
            </p>
            <div style={{ display: "flex", gap: 12, marginTop: 25 }}>
              <button
                onClick={openStudio}
                className="liquid-glass"
                style={{
                  padding: "24px 29px",
                  borderRadius: 999,
                  background: "rgba(255,255,255,0.04)",
                  color: "#fff",
                  fontSize: 15,
                  fontWeight: 500,
                  border: "none",
                  cursor: "pointer",
                  fontFamily: "var(--font-sans)",
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 8,
                }}
              >
                Open Studio <IconArrow size={13} />
              </button>
            </div>
          </div>
        </div>

        {/* Logo marquee */}
        <LogoMarquee />
      </div>
    </section>
  );
}

function LogoMarquee() {
  // Real, working integrations only -- Tavily/Firecrawl/Neon never had a
  // functioning connector behind them (invented x402 hostnames / no case
  // in ExecuteAction). Tendril is a genuine live x402 compute-rental
  // integration; Slack and GitHub are real connectors in the Actions tab.
  //
  // Each mark is imported on its own rather than through the canvas node's
  // template->icon table: that table statically pulls in ~23 simple-icons so
  // the bundler can drop the rest, and this page draws exactly one of them.
  // Slack is hand-drawn because simple-icons no longer ships it, and Tendril
  // because it publishes no icon to any package -- see components/brand/marks.
  const logos = [
    { name: "Tendril", mark: <TendrilMark size={13} /> },
    { name: "Slack", mark: <SlackMark size={13} /> },
    { name: "GitHub", mark: <SimpleIconMark icon={siGithub} size={13} /> },
  ];
  const doubled = [...logos, ...logos];

  return (
    <div className="lp-marquee" style={{ position: "relative", zIndex: 11 }}>
      <div className="lp-marquee-row">
        <div
          style={{
            flex: "0 0 auto",
            color: "rgba(242, 240, 247, 0.5)",
            fontSize: 13,
            lineHeight: 1.4,
            fontFamily: "var(--font-sans)",
          }}
        >
          Built with best-in-class
          <br />
          agent tooling
        </div>
        <div className="marquee-mask" style={{ flex: 1, overflow: "hidden" }}>
          <div className="marquee-track">
            {doubled.map((l, i) => (
              <div
                key={i}
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 10,
                  flexShrink: 0,
                }}
              >
                <span
                  className="liquid-glass"
                  style={{
                    width: 24,
                    height: 24,
                    borderRadius: 8,
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    color: "rgba(242, 240, 247, 0.95)",
                  }}
                >
                  {l.mark}
                </span>
                <span
                  style={{
                    fontFamily: "var(--font-sans)",
                    fontSize: 16,
                    fontWeight: 600,
                    color: "rgba(242, 240, 247, 0.95)",
                    letterSpacing: "-0.01em",
                  }}
                >
                  {l.name}
                </span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

// ── Pillars ───────────────────────────────────────────────────────────────
function LandingPillars() {
  const pillars = [
    {
      tag: "01",
      kicker: "build",
      title: "Design agent workflows visually.",
      body: "Drag triggers, agents, providers, tools, and actions onto a canvas. Connect them like Lego. The graph IS the execution graph.",
      glyph: "⬡",
    },
    {
      tag: "02",
      kicker: "fund",
      title: "Budgets at deploy, not signup.",
      body: "Each agent gets its own spending budget the moment you deploy. Top it up manually, watch usage tick live.",
      glyph: "◎",
    },
    {
      tag: "03",
      kicker: "wire",
      title: "Paid APIs as tools.",
      body: "Any metered HTTP endpoint becomes a tool node. Priced at call-time, settled per call, accountable per agent.",
      glyph: "⟡",
    },
    {
      tag: "04",
      kicker: "run",
      title: "Agent-to-agent messages, audit-ready.",
      body: "Inter-agent messages produce verifiable receipts. Replay any run. Audit-friendly by default.",
      glyph: "◈",
    },
  ];
  return (
    <section
      id="pillars"
      style={{
        padding: "var(--lp-section-pad)",
        borderTop: "1px solid var(--border)",
        position: "relative",
        zIndex: 1,
        background: "rgba(4, 3, 12, 0.55)",
      }}
    >
      <div style={{ maxWidth: 1280, margin: "0 auto" }}>
        <div className="in-view" style={{ marginBottom: 72 }}>
          <Tag>what is agentmesh</Tag>
          <h2
            style={{
              margin: "16px 0 0",
              fontSize: 48,
              fontWeight: 500,
              letterSpacing: "-0.028em",
              maxWidth: 680,
              fontFamily: "var(--font-sans)",
              lineHeight: 1.12,
            }}
          >
            Visual workflow design for autonomous agents.
          </h2>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "var(--lp-pillars-cols)",
            gap: 20,
          }}
        >
          {pillars.map((p, i) => (
            <div
              key={i}
              className="in-view"
              style={{ transitionDelay: `${i * 90}ms` }}
            >
              <div
                style={{
                  height: "100%",
                  padding: "36px 36px 40px",
                  background: "rgba(255,255,255,0.035)",
                  border: "1px solid rgba(255,255,255,0.08)",
                  borderRadius: 16,
                  backdropFilter: "blur(8px)",
                  position: "relative",
                  overflow: "hidden",
                  transition: "border-color .2s, box-shadow .2s",
                }}
                onMouseEnter={(e) => {
                  (e.currentTarget as HTMLElement).style.borderColor =
                    "rgba(167,140,250,0.28)";
                  (e.currentTarget as HTMLElement).style.boxShadow =
                    "0 0 40px rgba(167,140,250,0.07)";
                }}
                onMouseLeave={(e) => {
                  (e.currentTarget as HTMLElement).style.borderColor =
                    "rgba(255,255,255,0.08)";
                  (e.currentTarget as HTMLElement).style.boxShadow = "none";
                }}
              >
                <div
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    right: 0,
                    height: 1,
                    background:
                      "linear-gradient(90deg, transparent, var(--accent), transparent)",
                    opacity: 0.4,
                  }}
                />
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 10,
                    marginBottom: 28,
                  }}
                >
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      color: "var(--accent)",
                      padding: "3px 9px",
                      border: "1px solid var(--accent-line)",
                      borderRadius: 999,
                      background: "var(--accent-soft)",
                      letterSpacing: "0.06em",
                    }}
                  >
                    {p.tag}
                  </span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 11,
                      color: "var(--fg-dim)",
                      letterSpacing: "0.08em",
                      textTransform: "uppercase",
                    }}
                  >
                    · {p.kicker}
                  </span>
                </div>
                <h3
                  style={{
                    margin: 0,
                    fontSize: 22,
                    fontWeight: 500,
                    letterSpacing: "-0.022em",
                    lineHeight: 1.25,
                  }}
                >
                  {p.title}
                </h3>
                <p
                  style={{
                    margin: "14px 0 0",
                    color: "var(--fg-muted)",
                    fontSize: 14.5,
                    lineHeight: 1.65,
                  }}
                >
                  {p.body}
                </p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

// ── Flow steps ────────────────────────────────────────────────────────────
function LandingFlow() {
  const steps = [
    {
      k: "01",
      label: "Drop nodes",
      body: "Trigger, agent, provider, tools, action, end.",
      glyph: "⊕",
    },
    {
      k: "02",
      label: "Wire ports",
      body: "Provider + tools attach to agent's bottom ports.",
      glyph: "⟡",
    },
    {
      k: "03",
      label: "Deploy",
      body: "Agents get their own spending budgets at this moment.",
      glyph: "◎",
    },
    {
      k: "04",
      label: "Fund + run",
      body: "Top up agent budgets. Watch usage settle live.",
      glyph: "▶",
    },
  ];
  return (
    <section
      id="flow"
      style={{
        borderTop: "1px solid var(--border)",
        background: "rgba(4, 3, 12, 0.62)",
        padding: "112px 32px",
        position: "relative",
        zIndex: 1,
      }}
    >
      <div style={{ maxWidth: 1280, margin: "0 auto" }}>
        <div className="in-view" style={{ marginBottom: 64 }}>
          <Tag>flow</Tag>
          <h2
            style={{
              margin: "16px 0 0",
              fontSize: 40,
              fontWeight: 500,
              letterSpacing: "-0.025em",
              fontFamily: "var(--font-sans)",
              lineHeight: 1.15,
            }}
          >
            Zero to running pipeline in four moves.
          </h2>
        </div>
        <div
          className="in-view"
          style={{
            display: "grid",
            gridTemplateColumns: "var(--lp-steps-cols)",
            gap: 14,
          }}
        >
          {steps.map((s, i) => (
            <div key={i} style={{ position: "relative" }}>
              {i < steps.length - 1 && (
                <div
                  style={{
                    position: "absolute",
                    top: 42,
                    right: -10,
                    zIndex: 2,
                    color: "var(--fg-dim)",
                    fontSize: 16,
                    pointerEvents: "none",
                  }}
                >
                  ›
                </div>
              )}
              <div
                style={{
                  height: "100%",
                  padding: "28px 24px 32px",
                  background: "rgba(255,255,255,0.03)",
                  border: "1px solid rgba(255,255,255,0.07)",
                  borderRadius: 14,
                  backdropFilter: "blur(6px)",
                  transition: "border-color .2s, background .2s",
                }}
                onMouseEnter={(e) => {
                  (e.currentTarget as HTMLElement).style.borderColor =
                    "rgba(167,140,250,0.22)";
                  (e.currentTarget as HTMLElement).style.background =
                    "rgba(255,255,255,0.055)";
                }}
                onMouseLeave={(e) => {
                  (e.currentTarget as HTMLElement).style.borderColor =
                    "rgba(255,255,255,0.07)";
                  (e.currentTarget as HTMLElement).style.background =
                    "rgba(255,255,255,0.03)";
                }}
              >
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    marginBottom: 24,
                  }}
                >
                  <span
                    style={{
                      width: 36,
                      height: 36,
                      borderRadius: 10,
                      background: "var(--accent-soft)",
                      border: "1px solid var(--accent-line)",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      fontSize: 15,
                      color: "var(--accent)",
                    }}
                  >
                    {s.glyph}
                  </span>
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: 10,
                      color: "var(--fg-dim)",
                      letterSpacing: "0.1em",
                    }}
                  >
                    {s.k}
                  </span>
                </div>
                <div
                  style={{
                    fontSize: 17,
                    fontWeight: 600,
                    letterSpacing: "-0.018em",
                    marginBottom: 10,
                  }}
                >
                  {s.label}
                </div>
                <div
                  style={{
                    color: "var(--fg-muted)",
                    fontSize: 13,
                    lineHeight: 1.6,
                  }}
                >
                  {s.body}
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

// ── Waitlist ──────────────────────────────────────────────────────────────
function LandingWaitlist() {
  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const email = data.get("email") as string;
    try {
      await waitlist.join(email);
      alert("Thanks! We'll be in touch.");
    } catch {
      alert("Thanks! We'll be in touch.");
    }
  };

  return (
    <section
      id="waitlist"
      style={{
        borderTop: "1px solid var(--border)",
        padding: "var(--lp-section-pad)",
        position: "relative",
        zIndex: 1,
        background: "rgba(4, 3, 12, 0.55)",
      }}
    >
      <div
        className="in-view"
        style={{ maxWidth: 540, margin: "0 auto", textAlign: "center" }}
      >
        <Tag>early access</Tag>
        <h2
          style={{
            margin: "20px 0 14px",
            fontSize: 48,
            fontWeight: 500,
            letterSpacing: "-0.028em",
            fontFamily: "var(--font-sans)",
            lineHeight: 1.1,
          }}
        >
          Join the waitlist.
        </h2>
        <p
          style={{
            color: "var(--fg-muted)",
            fontSize: 15,
            lineHeight: 1.6,
            marginBottom: 40,
          }}
        >
          {WAITLIST_COUNT}+ teams in queue. Early access opens in cohorts;
          it&apos;s free to start.
        </p>
        <div
          style={{
            padding: "32px 32px 28px",
            background: "rgba(255,255,255,0.04)",
            border: "1px solid rgba(255,255,255,0.09)",
            borderRadius: 18,
            backdropFilter: "blur(12px)",
            boxShadow:
              "0 0 60px rgba(167,140,250,0.07), inset 0 1px 0 rgba(255,255,255,0.06)",
          }}
        >
          <form onSubmit={handleSubmit} style={{ display: "flex", gap: 8 }}>
            <input
              name="email"
              type="email"
              required
              placeholder="you@company.com"
              style={{
                height: 46,
                flex: 1,
                background: "rgba(255,255,255,0.05)",
                border: "1px solid rgba(255,255,255,0.1)",
                borderRadius: "var(--r-2)",
                color: "var(--fg)",
                fontFamily: "var(--font-sans)",
                fontSize: 14,
                padding: "0 12px",
                outline: "none",
              }}
            />
            <button
              type="submit"
              style={{
                height: 46,
                padding: "0 22px",
                fontSize: 13,
                fontWeight: 600,
                background: "var(--accent)",
                color: "var(--accent-fg)",
                border: "none",
                borderRadius: "var(--r-2)",
                cursor: "pointer",
                fontFamily: "var(--font-sans)",
                boxShadow: "0 0 20px var(--accent-glow)",
                whiteSpace: "nowrap",
              }}
            >
              Request access
            </button>
          </form>
          <div
            style={{
              display: "flex",
              justifyContent: "center",
              gap: 32,
              marginTop: 24,
              paddingTop: 20,
              borderTop: "1px solid rgba(255,255,255,0.06)",
            }}
          >
            {[
              { v: `${WAITLIST_COUNT}+`, l: "teams in queue" },
              { v: "free", l: "to start" },
            ].map((s, i) => (
              <div key={i} style={{ textAlign: "center" }}>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 15,
                    fontWeight: 600,
                    color: "var(--accent)",
                  }}
                >
                  {s.v}
                </div>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: 10,
                    color: "var(--fg-dim)",
                    marginTop: 3,
                    textTransform: "uppercase",
                    letterSpacing: "0.06em",
                  }}
                >
                  {s.l}
                </div>
              </div>
            ))}
          </div>
        </div>
        <div
          style={{
            marginTop: 18,
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--fg-dim)",
          }}
        >
          no spam · cohort invites only
        </div>
      </div>
    </section>
  );
}

// ── Footer ────────────────────────────────────────────────────────────────
function LandingFooter() {
  const linkStyle: React.CSSProperties = {
    color: "inherit",
    textDecoration: "none",
  };
  return (
    <footer
      style={{
        borderTop: "1px solid var(--border)",
        padding: "28px 32px",
        position: "relative",
        zIndex: 1,
        background: "rgba(4, 3, 12, 0.70)",
      }}
    >
      <div
        style={{
          maxWidth: 1280,
          margin: "0 auto",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          flexWrap: "wrap",
          gap: 12,
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--fg-dim)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <Logo size={14} />
          <span>
            © {new Date().getFullYear()} AgentMesh · SOUBHAGYA SADHUKHAN
          </span>
        </div>
        <div style={{ display: "flex", gap: 20, flexWrap: "wrap" }}>
          <a href="/terms" style={linkStyle}>
            Terms &amp; Conditions
          </a>
          <a href="/refund-policy" style={linkStyle}>
            Refund Policy
          </a>
          <a
            href="https://github.com/notlevi911/agentmesh"
            target="_blank"
            rel="noreferrer"
            style={linkStyle}
          >
            GitHub ↗
          </a>
        </div>
      </div>
    </footer>
  );
}
