"use client";

import { useState, useEffect } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ShinyButton } from "@/components/ui/shiny-button";
import { ShinyInputWrapper } from "@/components/ui/shiny-input-wrapper";
import { Textarea } from "@/components/ui/textarea";
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp";
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerDescription,
} from "@/components/ui/drawer";

export function WaitlistForm() {
  const [email, setEmail] = useState("");
  const [inviteCode, setInviteCode] = useState("");
  const [useCase, setUseCase] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState(false);
  const [count, setCount] = useState<number | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError("");

    const res = await fetch("/api/waitlist", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email,
        use_case: useCase || null,
        invite_code: inviteCode || null,
      }),
    });

    const data = await res.json();
    setLoading(false);

    if (!res.ok) {
      setError(data.error ?? "Something went wrong");
    } else {
      setSuccess(true);
    }
  };

  useEffect(() => {
    let mounted = true;
    fetch("/api/waitlist/count")
      .then((r) => r.json())
      .then((j) => {
        if (!mounted) return;
        if (typeof j.count === "number") setCount(j.count);
      })
      .catch(() => {
        if (!mounted) return;
        setCount(null);
      });
    return () => {
      mounted = false;
    };
  }, []);

  return (
    <section id="waitlist" className="mx-auto max-w-lg px-6 py-24">
      <p className="mb-2 text-xs font-semibold tracking-[2.5px] text-[#4b5563] uppercase">
        Early Access
      </p>
      <div className="mb-4 flex items-baseline justify-between">
        <h2 className="text-3xl font-bold text-foreground" style={{ letterSpacing: "-0.5px" }}>
          Join the waitlist
        </h2>
        <div className="text-sm text-[#6b7280]">
          {count !== null ? `${100 + count} waitlisted` : "100+ waitlisted"}
        </div>
      </div>

      <Card className="border-[#1c1c1c] bg-[#0f0f0f]">
        <CardContent className="p-6">
          <form onSubmit={handleSubmit} className="flex flex-col gap-5">
            {/* Email */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">Email address</label>
              <ShinyInputWrapper>
                <Input
                  type="email"
                  placeholder="you@example.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  className="border-0 bg-transparent text-sm text-foreground placeholder:text-[#2a2a2a] outline-none shadow-none focus-visible:ring-0 focus-visible:border-0 focus-visible:shadow-none focus-visible:outline-none"
                />
              </ShinyInputWrapper>
              {error && <p className="text-xs text-red-400">{error}</p>}
            </div>

            {/* Invite code */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">
                Invite code{" "}
                <span className="text-[#2a2a2a]">(optional)</span>
              </label>
              <ShinyInputWrapper className="px-3 py-2">
                <InputOTP maxLength={6} value={inviteCode} onChange={setInviteCode}>
                  <InputOTPGroup className="gap-2">
                    {Array.from({ length: 6 }).map((_, i) => (
                      <InputOTPSlot
                        key={i}
                        index={i}
                        className="h-9 w-9 rounded-md border-[#333] bg-[#1a1a1a] text-foreground focus:ring-0 data-[active=true]:ring-0 data-[active=true]:shadow-none data-[active=true]:border-transparent data-[active=true]:outline-none"
                      />
                    ))}
                  </InputOTPGroup>
                </InputOTP>
              </ShinyInputWrapper>
            </div>

            {/* Use case */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">
                What will you use AgentMesh for?
              </label>
              <ShinyInputWrapper>
                <Textarea
                  placeholder="Describe your use case..."
                  value={useCase}
                  onChange={(e) => setUseCase(e.target.value)}
                  rows={3}
                  className="resize-none border-0 bg-transparent text-sm text-foreground placeholder:text-[#2a2a2a] outline-none shadow-none focus-visible:ring-0 focus-visible:border-0 focus-visible:shadow-none focus-visible:outline-none"
                />
              </ShinyInputWrapper>
            </div>

            <ShinyButton type="submit" disabled={loading} className="w-full justify-center">
              {loading ? "Submitting..." : "Request Early Access"}
            </ShinyButton>

            <p className="text-center text-xs text-[#2a2a2a]">
              No spam. Early access to the testnet pipeline builder.
            </p>
          </form>
        </CardContent>
      </Card>

      <Drawer open={success} onOpenChange={setSuccess}>
        <DrawerContent className="border-[#1c1c1c] bg-[#0f0f0f]">
          <DrawerHeader className="pb-10 pt-8 text-center">
            <div className="mx-auto mb-5 flex h-12 w-12 items-center justify-center rounded-full bg-primary/10">
              <span className="text-xl text-primary">✓</span>
            </div>
            <DrawerTitle className="text-xl font-bold text-foreground">
              You&apos;re on the list
            </DrawerTitle>
            <DrawerDescription className="mt-2 text-sm text-[#6b7280]">
              We&apos;ll reach out when early access opens. Watch your inbox.
            </DrawerDescription>
          </DrawerHeader>
        </DrawerContent>
      </Drawer>
    </section>
  );
}
