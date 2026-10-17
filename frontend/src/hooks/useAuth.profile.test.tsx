import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { AuthUser } from "@/lib/api";

const state = vi.hoisted(() => ({ me: vi.fn(), update: vi.fn() }));
vi.mock("@/lib/api", async (original) => ({
  ...await original<typeof import("@/lib/api")>(),
  auth: { me: state.me, updateProfile: state.update, signOut: async () => {} },
}));
vi.mock("@/lib/nativeAuth", () => ({
  IS_NATIVE: false, authReady: Promise.resolve(), getAuthToken: () => null, setAuthToken: vi.fn(),
}));
import { useAuth, getAccountRevision, publishAccountThreshold } from "./useAuth";
import { readCredits, resetCredits } from "@/lib/credits/store";

let sequence = 0;
let user: AuthUser;
beforeEach(() => {
  user = { id: `profile-${++sequence}`, name: "Original", orgName: "Team", email: "owner@example.test", needsOnboarding: false, hasPassword: true, createdAt: "2026-01-01T00:00:00Z", lowBalanceUsdMicros: 7_654_321 };
  state.me.mockReset().mockResolvedValue(user);
  state.update.mockReset();
  resetCredits();
});
afterEach(cleanup);

it("bootstraps the threshold without opening Settings and shares profile edits across hooks", async () => {
  const first = renderHook(useAuth);
  const second = renderHook(useAuth);
  await waitFor(() => expect(second.result.current.user?.id).toBe(user.id));
  expect(readCredits().lowBalanceThresholdUSD).toBe(7.654321);
  state.update.mockResolvedValue({ id: user.id, name: "Changed", orgName: "New team", needsOnboarding: false });
  await act(() => first.result.current.completeOnboarding("Changed", "New team"));
  expect(first.result.current.user).toMatchObject({ name: "Changed", hasPassword: true, createdAt: user.createdAt });
  expect(second.result.current.user?.name).toBe("Changed");
});

it("does not overwrite newer profile and threshold values with an older account read", async () => {
  const first = renderHook(useAuth);
  await waitFor(() => expect(first.result.current.user).not.toBeNull());
  let release!: (user: AuthUser) => void;
  state.me.mockReturnValueOnce(new Promise<AuthUser>((resolve) => { release = resolve; }));
  const late = renderHook(useAuth);
  await waitFor(() => expect(state.me).toHaveBeenCalledTimes(2));
  state.update.mockResolvedValue({ ...user, name: "Saved" });
  await act(() => first.result.current.completeOnboarding("Saved", "Team"));
  act(() => publishAccountThreshold(user.id, 1_234_567, getAccountRevision()));
  await act(async () => release(user));
  expect(late.result.current.user?.name).toBe("Saved");
  expect(readCredits().lowBalanceThresholdUSD).toBe(1.234567);
});

it("drops profile saves and preference callbacks from a signed-out account", async () => {
  const old = renderHook(useAuth);
  await waitFor(() => expect(old.result.current.user).not.toBeNull());
  let release!: (user: AuthUser) => void;
  state.update.mockReturnValue(new Promise<AuthUser>((resolve) => { release = resolve; }));
  let saving!: Promise<void>;
  act(() => { saving = old.result.current.completeOnboarding("Late", "Old"); });
  const revision = getAccountRevision();
  await act(() => old.result.current.signOut());
  state.me.mockResolvedValue({ ...user, id: "new-owner", name: "New owner", lowBalanceUsdMicros: 2_000_000 });
  const current = renderHook(useAuth);
  await waitFor(() => expect(current.result.current.user?.id).toBe("new-owner"));
  await act(async () => { release({ ...user, name: "Late" }); await saving; });
  act(() => publishAccountThreshold(user.id, 99_000_000, revision));
  expect(current.result.current.user?.name).toBe("New owner");
  expect(old.result.current.user).toBeNull();
  expect(readCredits().lowBalanceThresholdUSD).toBe(2);
});

it("preserves the profile after a rejected save", async () => {
  const hook = renderHook(useAuth);
  await waitFor(() => expect(hook.result.current.user).not.toBeNull());
  state.update.mockRejectedValue(new Error("offline"));
  await act(async () => { await expect(hook.result.current.completeOnboarding("Wrong", "Wrong")).rejects.toThrow("offline"); });
  expect(hook.result.current.user).toEqual(user);
});


it("accepts newer server values on a read begun after a local save", async () => {
  const first = renderHook(useAuth);
  await waitFor(() => expect(first.result.current.user).not.toBeNull());
  state.update.mockResolvedValue({ ...user, name: "Local save" });
  await act(() => first.result.current.completeOnboarding("Local save", "Team"));
  act(() => publishAccountThreshold(user.id, 1_000_000, getAccountRevision()));
  state.me.mockResolvedValue({ ...user, name: "Changed elsewhere", lowBalanceUsdMicros: 3_000_000 });
  const later = renderHook(useAuth);
  await waitFor(() => expect(later.result.current.user?.name).toBe("Changed elsewhere"));
  expect(readCredits().lowBalanceThresholdUSD).toBe(3);
});
