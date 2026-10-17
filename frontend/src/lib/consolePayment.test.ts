import { afterEach, describe, expect, it, vi } from "vitest";
import { prism, PrismRunError } from "./prism";
import { helixbox, HelixboxRunError } from "./helixbox";

vi.mock("./nativeAuth", () => ({ IS_NATIVE: false, authHeaders: () => ({}) }));
afterEach(() => vi.unstubAllGlobals());

const clients = [
  { name: "Prism", path: "/prism/run", call: () => prism.run("code-review-fast", {}), error: PrismRunError },
  { name: "repository review", path: "/prism/repo/review", call: () => prism.repoReview("example/project", "main", "fast", ["one.go"]), error: PrismRunError },
  { name: "HelixBox", path: "/helixbox/run", call: () => helixbox.run("cli-hour", { code: "test-session" }), error: HelixboxRunError },
];

describe.each(clients)("$name payment errors", ({ path, call, error }) => {
  it.each([
    [402, "spending_limit", "spending_limit"],
    [402, "insufficient_credits", "insufficient_credits"],
    [402, "unknown", null],
    [402, undefined, null],
    [502, "spending_limit", null],
  ])("preserves only a recognized refusal for status %s and code %s", async (status, code, expected) => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "Request refused", code }), { status }));
    vi.stubGlobal("fetch", fetch);
    const rejected = await call().catch((e: unknown) => e);
    expect(rejected).toBeInstanceOf(error);
    expect(rejected).toMatchObject({ message: "Request refused", status, code: expected });
    expect(fetch).toHaveBeenCalledExactlyOnceWith(expect.stringContaining(path), expect.objectContaining({ method: "POST", credentials: "include" }));
  });

  it("keeps the status when an error body cannot be decoded", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("unavailable", { status: 503 })));
    const rejected = await call().catch((e: unknown) => e);
    expect(rejected).toBeInstanceOf(error);
    expect(rejected).toMatchObject({ status: 503 });
  });
});
