import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";

const state = vi.hoisted(() => ({
  native: false,
  me: vi.fn(),
  deleteAccount: vi.fn(),
  setToken: vi.fn(),
  signedOut: vi.fn(),
  clearNative: vi.fn(),
  clearStorage: vi.fn(),
  resetCredits: vi.fn(),
}));
vi.mock("@/lib/api", async (original) => ({
  ...await original<typeof import("@/lib/api")>(),
  auth: { me: state.me, deleteAccount: state.deleteAccount },
}));
vi.mock("@/lib/nativeAuth", () => ({
  get IS_NATIVE() { return state.native; },
  authReady: Promise.resolve(),
  getAuthToken: () => "current-session",
  setAuthToken: state.setToken,
}));
vi.mock("@/native", () => ({ shell: { onSignedOut: state.signedOut } }));
vi.mock("@/native/deletion", () => ({ clearDeletedAccount: state.clearNative }));
vi.mock("@/lib/accountDeletion", () => ({ clearDeletedAccountStorage: state.clearStorage }));
vi.mock("@/lib/credits/store", () => ({ resetCredits: state.resetCredits, setAccountThreshold: vi.fn() }));
import { useAuth } from "./useAuth";

const user = { id: "owner", email: "owner@example.test", hasPassword: true };
beforeEach(() => {
  vi.clearAllMocks();
  state.me.mockReset().mockResolvedValue(user);
  state.deleteAccount.mockReset().mockResolvedValue(undefined);
  state.clearNative.mockReset().mockResolvedValue(undefined);
  state.clearStorage.mockReset();
  state.native = false;
  document.cookie = "agentmesh_ui=; Path=/; Max-Age=0";
});
afterEach(() => cleanup());

it.each([false, true])("clears the session only after deletion succeeds (native=%s)", async (native) => {
  state.native = native;
  const { result } = renderHook(() => useAuth());
  await waitFor(() => expect(result.current.signedIn).toBe(true));
  await act(() => result.current.deleteAccount("DELETE", "password"));
  expect(state.deleteAccount).toHaveBeenCalledWith("DELETE", "password");
  expect(result.current.user).toBeNull();
  expect(result.current.signedIn).toBe(false);
  expect(document.cookie).not.toContain("agentmesh_ui=1");
  expect(state.resetCredits).toHaveBeenCalledOnce();
  expect(state.clearStorage).toHaveBeenCalledOnce();
  if (native) {
    expect(state.setToken).toHaveBeenCalledWith(null);
    expect(state.clearNative).toHaveBeenCalledWith("current-session");
    expect(state.signedOut).not.toHaveBeenCalled();
  } else {
    expect(state.setToken).not.toHaveBeenCalled();
  }
});

it("keeps the current session on a rejected deletion", async () => {
  state.deleteAccount.mockRejectedValueOnce(new Error("active machines"));
  const { result } = renderHook(() => useAuth());
  await waitFor(() => expect(result.current.signedIn).toBe(true));
  await act(async () => {
    await expect(result.current.deleteAccount("DELETE", "password")).rejects.toThrow("active machines");
  });
  expect(result.current.signedIn).toBe(true);
  expect(result.current.user).toEqual(user);
  expect(document.cookie).toContain("agentmesh_ui=1");
  expect(state.resetCredits).not.toHaveBeenCalled();
  expect(state.setToken).not.toHaveBeenCalled();
  expect(state.clearStorage).not.toHaveBeenCalled();
  expect(state.clearNative).not.toHaveBeenCalled();
});

it("reports incomplete device cleanup after server deletion without restoring the session", async () => {
  state.native = true;
  state.clearNative.mockRejectedValueOnce(new Error("device unavailable"));
  const log = vi.spyOn(console, "error").mockImplementation(() => {});
  const { result } = renderHook(() => useAuth());
  await waitFor(() => expect(result.current.signedIn).toBe(true));
  await act(async () => {
    expect(await result.current.deleteAccount("DELETE", "password")).toBe(true);
  });
  expect(result.current.user).toBeNull();
  expect(state.clearStorage).toHaveBeenCalledOnce();
  expect(log).toHaveBeenCalled();
  log.mockRestore();
});

it("ignores a session check that resolves after account deletion", async () => {
  let resolve!: (value: unknown) => void;
  state.me.mockReturnValue(new Promise((done) => { resolve = done; }));
  const { result } = renderHook(() => useAuth());
  await waitFor(() => expect(state.me).toHaveBeenCalled());
  await act(() => result.current.deleteAccount("DELETE", "password"));
  await act(async () => resolve(user));
  expect(result.current.signedIn).toBe(false);
  expect(result.current.user).toBeNull();
  expect(document.cookie).not.toContain("agentmesh_ui=1");
});
