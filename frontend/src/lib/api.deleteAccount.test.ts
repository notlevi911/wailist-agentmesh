import { afterEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ native: false }));
vi.mock("./nativeAuth", () => ({
  get IS_NATIVE() { return state.native; },
  authHeaders: () => state.native ? { Authorization: "Bearer device-session" } : {},
}));

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
  state.native = false;
});

describe("auth.deleteAccount", () => {
  it.each([false, true])("uses the authenticated API transport (native=%s)", async (native) => {
    state.native = native;
    vi.stubEnv("NEXT_PUBLIC_API_URL", "https://backend.example.test");
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    const { auth } = await import("./api");
    await auth.deleteAccount("DELETE", "password");
    expect(fetch).toHaveBeenCalledExactlyOnceWith(
      native ? "https://backend.example.test/auth/me" : "/api/auth/me",
      expect.objectContaining({
        method: "DELETE",
        credentials: native ? "omit" : "include",
        headers: native ? { "Content-Type": "application/json", Authorization: "Bearer device-session" } : { "Content-Type": "application/json" },
        body: JSON.stringify({ confirmation: "DELETE", password: "password" }),
      }),
    );
  });

  it.each([401, 409, 500])("surfaces rejection status %s", async (status) => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "/api");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "Deletion rejected" }), { status })));
    const { auth } = await import("./api");
    await expect(auth.deleteAccount("DELETE", "password")).rejects.toThrow("Deletion rejected");
  });

  it("refuses a success page that did not confirm deletion", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "/api");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("<html>Sign in</html>")));
    const { auth } = await import("./api");
    await expect(auth.deleteAccount("DELETE", "password")).rejects.toThrow("Could not delete account");
  });

  it("does not simulate deletion when no backend is configured", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const { auth } = await import("./api");
    await expect(auth.deleteAccount("DELETE", "password")).rejects.toThrow("connected backend");
    expect(fetch).not.toHaveBeenCalled();
  });
});
