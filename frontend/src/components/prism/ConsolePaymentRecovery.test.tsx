import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { PrismConsolePage } from "./PrismConsolePage";
import { PrismRepoReview } from "./PrismRepoReview";
import { HelixboxConsolePage } from "@/components/helixbox/HelixboxConsolePage";
import type { PrismEndpoint } from "@/lib/prism";

const navigation = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => navigation }));
vi.mock("@/components/Topbar", () => ({ Topbar: () => <nav>Application navigation</nav> }));
vi.mock("@/lib/nativeAuth", () => ({ IS_NATIVE: false, authHeaders: () => ({}) }));
const endpoint: PrismEndpoint = { id: "code-review-fast", task: "code-review", tier: "fast", title: "Code review", description: "Review source", path: "/code-review-fast", method: "POST", amountMicros: 100_000, verified: "documented", fields: [{ name: "raw_url", label: "File URL", kind: "text", required: true, placeholder: "https://example.test/file.go" }] };
const fee = 1_500_000;
beforeEach(() => navigation.push.mockClear());
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

async function submit(kind: string, code: string, status = 402) {
  const fetch = vi.fn(async (url: string) => {
    if (url.endsWith("/prism/endpoints")) return Response.json({ provider: "Prism", tasks: [{ key: "code-review", title: "Code review", description: "Review source" }], endpoints: [endpoint], platformFeeUsdMicros: fee, maxFileBytes: 100_000 });
    if (url.endsWith("/helixbox/endpoints")) return Response.json({ provider: "HelixBox", endpoints: [{ id: "cli-hour", title: "CLI hour", description: "Terminal session", blurb: "One hour", path: "/cli-hour", method: "POST", amountMicros: 100_000, durationSeconds: 3600, accessLevel: "cli", fields: [{ name: "code", label: "Session code", kind: "text", required: true }] }], platformFeeUsdMicros: fee });
    if (url.endsWith("/prism/repo/files")) return Response.json({ owner: "example", name: "project", ref: "main", files: [{ path: "one.go", size: 100 }], reviewableCount: 1, maxFiles: 30, platformFeeTotal: fee });
    return Response.json({ error: "Request refused", code }, { status });
  });
  vi.stubGlobal("fetch", fetch);
  if (kind === "repository") {
    render(<PrismRepoReview endpoint={endpoint} platformFee={fee} />);
    fireEvent.change(screen.getByPlaceholderText("github.com/owner/repo"), { target: { value: "example/project" } });
    fireEvent.click(screen.getByRole("button", { name: "Find files" }));
    fireEvent.click(await screen.findByRole("button", { name: /^Review ·/ }));
  } else if (kind === "Prism") {
    render(<PrismConsolePage />);
    fireEvent.change(await screen.findByPlaceholderText("https://example.test/file.go"), { target: { value: "https://example.test/one.go" } });
    fireEvent.click(screen.getByRole("button", { name: /^Run ·/ }));
  } else {
    render(<HelixboxConsolePage />);
    fireEvent.change(await screen.findByLabelText("Session code"), { target: { value: "test-session" } });
    fireEvent.click(screen.getByRole("button", { name: /^Buy ·/ }));
  }
  await screen.findByText("Request refused");
  return fetch;
}

for (const kind of ["Prism", "repository", "HelixBox"]) {
  it(`${kind} directs a saved ceiling refusal to Settings through the real client`, async () => {
    await submit(kind, "spending_limit");
    expect(screen.getByText(/You were not charged/)).toBeTruthy();
    expect(screen.queryByText("Add credits")).toBeNull();
    const recovery = screen.getByRole(kind === "repository" ? "link" : "button", { name: "Review spending limit" });
    if (kind === "repository") expect(recovery.getAttribute("href")).toBe("/settings");
    else { fireEvent.click(recovery); expect(navigation.push).toHaveBeenCalledWith("/settings"); }
  });

  it(`${kind} preserves top-up recovery for insufficient credit`, async () => {
    await submit(kind, "insufficient_credits");
    expect(screen.getByText(/You were not charged/)).toBeTruthy();
    expect(screen.queryByText("Review spending limit")).toBeNull();
    const recovery = screen.getByRole(kind === "repository" ? "link" : "button", { name: "Add credits" });
    if (kind === "repository") expect(recovery.getAttribute("href")).toBe("/billing");
    else { fireEvent.click(recovery); expect(navigation.push).toHaveBeenCalledWith("/billing"); }
  });

  it(`${kind} does not suggest buying credit for an unknown refusal`, async () => {
    await submit(kind, "unknown");
    await waitFor(() => expect(screen.queryByText("Add credits")).toBeNull());
    expect(screen.queryByText("Review spending limit")).toBeNull();
  });
}
