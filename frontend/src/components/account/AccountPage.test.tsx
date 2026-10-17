import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";

// Auth and credits are stubbed with a signed-in account. IS_NATIVE is a getter
// so each test can choose the build it runs as: a mocked module is created once
// and kept, so a plain value would stay whatever the first import saw.
const state = vi.hoisted(() => ({
  native: false,
  push: vi.fn(),
  replace: vi.fn(),
  signOut: vi.fn(),
  refreshBalance: vi.fn(),
}));

vi.mock("@/lib/nativeAuth", () => ({
  get IS_NATIVE() {
    return state.native;
  },
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: state.push, replace: state.replace }),
}));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    user: {
      id: "u-1",
      name: "Dana",
      email: "dana@example.com",
      orgName: "Harbor Labs",
      needsOnboarding: false,
    },
    signOut: state.signOut,
  }),
}));
vi.mock("@/lib/credits/store", () => ({
  useCredits: () => ({
    balanceUSD: 12.5,
    balanceKnown: true,
    refreshBalance: state.refreshBalance,
  }),
}));
vi.mock("@/components/Topbar", () => ({ Topbar: () => null }));
vi.mock("@/components/notifications/NotificationsSheet", () => ({
  NotificationsSheet: () => <div role="dialog">notifications</div>,
}));

async function renderPage() {
  const { AccountPage } = await import("./AccountPage");
  render(<AccountPage />);
}

beforeEach(() => {
  state.signOut.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  state.native = false;
  vi.clearAllMocks();
  vi.resetModules();
});

describe("AccountPage", () => {
  it("shows who is signed in, the balance and where to go from here", async () => {
    await renderPage();

    expect(screen.getByText("Dana")).toBeTruthy();
    expect(screen.getByText("dana@example.com")).toBeTruthy();
    expect(screen.getByText("Harbor Labs")).toBeTruthy();

    const credits = screen.getByRole("link", { name: /Credits/ });
    expect(credits.getAttribute("href")).toBe("/billing");
    expect(credits.textContent).toContain("$12.50");
    // Usage is a tab on the bottom bar, so Account does not repeat it.
    expect(screen.queryByRole("link", { name: /Usage/ })).toBeNull();
    expect(state.refreshBalance).toHaveBeenCalled();
    expect(screen.getByRole("link", { name: "Delete account" }).getAttribute("href")).toBe("/settings#delete-title");
  });

  it("leaves Notifications out on the web", async () => {
    await renderPage();
    expect(screen.queryByRole("button", { name: /Notifications/ })).toBeNull();
  });

  it("opens the notification settings in the native app", async () => {
    state.native = true;
    await renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Notifications/ }));
    expect(screen.getByRole("dialog").textContent).toBe("notifications");
  });

  it("signs out to the sign-in screen, without leaving this page behind", async () => {
    await renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    // replace, not push: Back must not return to an account page this
    // session can no longer load.
    await waitFor(() => expect(state.replace).toHaveBeenCalledWith("/signin"));
    expect(state.push).not.toHaveBeenCalled();
    expect(state.signOut).toHaveBeenCalledTimes(1);
  });
});
