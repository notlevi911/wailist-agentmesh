"use client";

import { InfiniteGrid } from "@/components/ui/infinite-grid";
import { TextScramble } from "@/components/ui/text-scramble";
import { ShinyButton } from "@/components/ui/shiny-button";

export function Hero() {
  const scrollToWaitlist = () => {
    document.getElementById("waitlist")?.scrollIntoView({ behavior: "smooth" });
  };

  return (
    <InfiniteGrid className="min-h-screen">
      {/* Glow blobs */}
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="absolute left-1/2 top-[-80px] h-[500px] w-[500px] -translate-x-1/2 rounded-full bg-[#22c55e]/[0.04] blur-[140px]" />
        <div className="absolute bottom-[-60px] right-[-80px] h-[320px] w-[320px] rounded-full bg-[#6d28d9]/[0.03] blur-[120px]" />
      </div>

      <div className="relative flex min-h-screen flex-col items-center justify-center px-4 text-center">
        {/* Badge */}
        <div className="mb-8 inline-flex items-center gap-2 rounded-full border border-[#1c1c1c] bg-[#0f0f0f] px-4 py-1.5 text-xs text-[#4b5563]">
          <span className="h-1.5 w-1.5 rounded-full bg-[#22c55e]" />
          Algorand Testnet · GoPlausible x402
        </div>

        {/* Heading */}
        <h1
          className="mb-4 text-6xl font-bold text-foreground md:text-7xl lg:text-8xl"
          style={{ letterSpacing: "-2px" }}
        >
          Agent
          <TextScramble
            text="Mesh"
            autoPlay
            delay={600}
            className="inline-block"
            style={{
              background: "linear-gradient(135deg, #6b21a8 0%, #9333ea 45%, #7c3aed 100%)",
              WebkitBackgroundClip: "text",
              WebkitTextFillColor: "transparent",
              backgroundClip: "text",
            }}
          />
        </h1>

        {/* Tagline */}
        <p className="mb-5 text-xs font-medium tracking-[3px] text-primary md:text-sm">
          BUILD · FUND · WIRE · RUN
        </p>

        {/* Sub-copy */}
        <p className="mb-10 max-w-xs text-sm leading-relaxed text-[#6b7280]">
          The visual canvas for autonomous AI agent networks on Algorand.
        </p>

        {/* CTA */}
        <ShinyButton onClick={scrollToWaitlist}>
          Get Early Access
        </ShinyButton>
      </div>
    </InfiniteGrid>
  );
}
