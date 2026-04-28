"use client";

import { useState } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ShinyButton } from "@/components/ui/shiny-button";
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

  return (
    <section id="waitlist" className="mx-auto max-w-lg px-6 py-24">
      <p className="mb-2 text-xs font-semibold tracking-[2.5px] text-[#4b5563] uppercase">
        Early Access
      </p>
      <h2 className="mb-8 text-3xl font-bold text-foreground" style={{ letterSpacing: "-0.5px" }}>
        Join the waitlist
      </h2>

      <Card className="border-[#1c1c1c] bg-[#0f0f0f]">
        <CardContent className="p-6">
          <form onSubmit={handleSubmit} className="flex flex-col gap-5">
            {/* Email */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">Email address</label>
              <Input
                type="email"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                className="border-[#222] bg-[#141414] text-sm text-foreground placeholder:text-[#2a2a2a] focus-visible:ring-primary/40"
              />
              {error && <p className="text-xs text-red-400">{error}</p>}
            </div>

            {/* Invite code */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">
                Invite code{" "}
                <span className="text-[#2a2a2a]">(optional)</span>
              </label>
              <InputOTP maxLength={6} value={inviteCode} onChange={setInviteCode}>
                <InputOTPGroup className="gap-2">
                  {Array.from({ length: 6 }).map((_, i) => (
                    <InputOTPSlot
                      key={i}
                      index={i}
                      className="h-10 w-10 rounded-md border-[#222] bg-[#141414] text-foreground"
                    />
                  ))}
                </InputOTPGroup>
              </InputOTP>
            </div>

            {/* Use case */}
            <div className="flex flex-col gap-1.5">
              <label className="text-xs text-[#6b7280]">
                What will you use AgentMesh for?
              </label>
              <Textarea
                placeholder="Describe your use case..."
                value={useCase}
                onChange={(e) => setUseCase(e.target.value)}
                rows={3}
                className="resize-none border-[#222] bg-[#141414] text-sm text-foreground placeholder:text-[#2a2a2a] focus-visible:ring-primary/40"
              />
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
