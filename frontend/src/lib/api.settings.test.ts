import { afterEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ native: false }));
vi.mock("./nativeAuth", () => ({
  get IS_NATIVE() { return state.native; },
  authHeaders: () => state.native ? { Authorization: "Bearer device-session" } : {},
}));
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); vi.resetModules(); state.native = false; });

it.each([false, true])("uses authenticated settings and password transport, native=%s", async (native) => {
  state.native = native;
  vi.stubEnv("NEXT_PUBLIC_API_URL", "https://backend.example.test");
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ lowBalanceUsdMicros: 1 }), { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const { settings, auth } = await import("./api");
  await settings.update({ maxCallSpendUsdMicros: null });
  expect(fetch).toHaveBeenCalledWith(native ? "https://backend.example.test/settings" : "/api/settings", expect.objectContaining({
    method: "PATCH", body: '{"maxCallSpendUsdMicros":null}', credentials: native ? "omit" : "include",
    headers: expect.objectContaining({ "Content-Type": "application/json", ...(native ? { Authorization: "Bearer device-session" } : {}) }),
  }));
  fetch.mockResolvedValue(new Response(null, { status: 204 }));
  await auth.changePassword("old-secret", "new-secret");
  expect(fetch).toHaveBeenLastCalledWith(native ? "https://backend.example.test/auth/password" : "/api/auth/password", expect.objectContaining({
    method: "POST", body: '{"currentPassword":"old-secret","newPassword":"new-secret"}',
  }));
});

it("distinguishes failed credential listing and removal from success", async () => {
  vi.stubEnv("NEXT_PUBLIC_API_URL", "/api");
  const fetch = vi.fn().mockResolvedValue(new Response("{}", { status: 500 }));
  vi.stubGlobal("fetch", fetch);
  const { oauth2 } = await import("./api");
  await expect(oauth2.listCredentials()).rejects.toThrow("Could not load");
  expect(fetch).toHaveBeenCalledWith("/api/oauth2/credentials", expect.anything());
  await expect(oauth2.deleteCredential("account/id")).rejects.toThrow("Could not disconnect");
  expect(fetch).toHaveBeenLastCalledWith("/api/oauth2/credentials/account%2Fid", expect.objectContaining({ method: "DELETE" }));
});

it("surfaces settings and password rejections", async () => {
  vi.stubEnv("NEXT_PUBLIC_API_URL", "/api");
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response('{"error":"current password is incorrect"}', { status: 401 }))));
  const { settings, auth } = await import("./api");
  await expect(settings.get()).rejects.toThrow();
  await expect(settings.update({ lowBalanceUsdMicros: 0 })).rejects.toThrow();
  await expect(auth.changePassword("wrong", "new-password")).rejects.toThrow("current password is incorrect");
});

it("does not simulate preference or password saves without a backend", async () => {
  vi.stubEnv("NEXT_PUBLIC_API_URL", "");
  const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
  const { settings, auth } = await import("./api");
  await expect(settings.update({ lowBalanceUsdMicros: 0 })).rejects.toThrow("connected backend");
  await expect(auth.changePassword("old-password", "new-password")).rejects.toThrow("connected backend");
  expect(fetch).not.toHaveBeenCalled();
});

