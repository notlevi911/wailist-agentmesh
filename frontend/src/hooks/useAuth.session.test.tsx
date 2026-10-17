import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

// AuthPage mounts useAuth and lets the form be submitted while that mount's own
// check is still in flight. On the web the session is the agentmesh_ui cookie
// plus the server's HttpOnly one, so a late "no session" answer must not clear
// what the sign-in just wrote.
const state = vi.hoisted(() => ({ me: vi.fn(), signIn: vi.fn() }));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    auth: {
      ...actual.auth,
      me: state.me,
      signIn: state.signIn,
      signOut: vi.fn(async () => {}),
    },
  };
});
vi.mock("@/lib/nativeAuth", () => ({
  IS_NATIVE: false,
  authReady: Promise.resolve(),
  getAuthToken: () => null,
  setAuthToken: vi.fn(),
}));
vi.mock("@/lib/credits/store", () => ({ resetCredits: vi.fn(), setAccountThreshold: vi.fn() }));

import { AuthCheckError } from "@/lib/api";
import { useAuth } from "./useAuth";

const cookie = () => document.cookie.includes("agentmesh_ui=1");

beforeEach(() => {
  document.cookie = "agentmesh_ui=; Path=/; Max-Age=0";
  state.me.mockReset();
  state.signIn.mockReset().mockResolvedValue(null);
});
afterEach(() => vi.clearAllMocks());

describe("a session check that lands after the session changed", () => {
  it("does not clear the cookie a sign-in wrote while it was in flight", async () => {
    let refuse!: (err: unknown) => void;
    state.me.mockReturnValue(
      new Promise((_, reject) => {
        refuse = reject;
      }),
    );
    const { result } = renderHook(() => useAuth());
    await waitFor(() => expect(state.me).toHaveBeenCalled());

    // The form is submitted while the mount check is still waiting.
    await act(async () => {
      await result.current.signIn("dana@example.com", "hunter2");
    });
    expect(cookie()).toBe(true);
    expect(result.current.signedIn).toBe(true);

    // The older check now answers "no session". It is about the session from
    // before the sign-in, so it must change nothing.
    await act(async () => {
      refuse(new AuthCheckError(401));
    });
    expect(cookie()).toBe(true);
    expect(result.current.signedIn).toBe(true);
  });

  // The mirror image: a check that succeeds must not put a signed-out user
  // back to signed in.
  it("does not revive a session that was signed out while it was in flight", async () => {
    let answer!: (user: unknown) => void;
    state.me.mockReturnValue(
      new Promise((resolve) => {
        answer = resolve;
      }),
    );
    const { result } = renderHook(() => useAuth());
    await waitFor(() => expect(state.me).toHaveBeenCalled());

    await act(async () => {
      await result.current.signOut();
    });
    expect(cookie()).toBe(false);

    await act(async () => {
      answer({ id: "u-1", email: "dana@example.com", name: "Dana" });
    });
    expect(cookie()).toBe(false);
    expect(result.current.signedIn).toBe(false);
  });

  // The ordinary case still works: no session change, so the answer applies.
  it("still signs out when nothing else changed", async () => {
    state.me.mockRejectedValue(new AuthCheckError(401));
    document.cookie = "agentmesh_ui=1; Path=/";
    const { result } = renderHook(() => useAuth());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.signedIn).toBe(false);
    expect(cookie()).toBe(false);
  });
});
