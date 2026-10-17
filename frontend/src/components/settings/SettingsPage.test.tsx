import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { AuthUser } from "@/lib/api";

const state = vi.hoisted(() => ({
  user: null as AuthUser | null,
  loading: false,
  offline: false,
  retry: vi.fn(),
  deleteAccount: vi.fn(),
  router: { replace: vi.fn() },
}));
vi.mock("next/navigation", () => ({ useRouter: () => state.router }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => state, getAccountRevision: () => 0, publishAccountThreshold: vi.fn() }));
vi.mock("@/lib/api", () => ({ settings: { get: () => new Promise(() => {}) } }));
vi.mock("./sections/Account", () => ({ AccountSection: () => null }));
vi.mock("./sections/Connections", () => ({ ConnectionsSection: () => null }));
vi.mock("./sections/Developer", () => ({ DeveloperSection: () => null }));
vi.mock("@/components/Topbar", () => ({ Topbar: () => <nav>Account navigation</nav> }));
import { SettingsPage } from "./SettingsPage";

beforeEach(() => {
  state.user = { id: "owner", email: "owner@example.test", name: "Owner", orgName: "", needsOnboarding: false, hasPassword: true };
  state.loading = false;
  state.offline = false;
  state.deleteAccount.mockReset().mockResolvedValue(undefined);
  state.router.replace.mockReset();
});
afterEach(() => cleanup());

function confirm(value = "DELETE") {
  fireEvent.change(screen.getByLabelText("Type DELETE to confirm"), { target: { value } });
}

function password(value = "correct-password") {
  fireEvent.change(screen.getByLabelText("Current password"), { target: { value } });
}

describe("account deletion Settings", () => {
  it("requires exact confirmation and a current password for password accounts", () => {
    render(<SettingsPage />);
    const button = screen.getByRole("button", { name: "Delete my account" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    confirm();
    expect(button.disabled).toBe(true);
    password();
    expect(button.disabled).toBe(false);
    for (const value of ["delete", " DELETE", "DELETE ", "Delete", ""]) {
      confirm(value);
      expect(button.disabled).toBe(true);
    }
    expect(state.deleteAccount).not.toHaveBeenCalled();
    expect(screen.getByText(/owner@example.test/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Back to account" }).getAttribute("href")).toBe("/account");
  });

  it("submits once and navigates only after successful deletion", async () => {
    let resolve!: () => void;
    state.deleteAccount.mockReturnValue(new Promise<void>((done) => { resolve = done; }));
    render(<SettingsPage />);
    confirm();
    password();
    const form = screen.getByLabelText("Type DELETE to confirm").closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(state.deleteAccount).toHaveBeenCalledExactlyOnceWith("DELETE", "correct-password");
    expect((screen.getByRole("button", { name: "Deleting account..." }) as HTMLButtonElement).disabled).toBe(true);
    expect(state.router.replace).not.toHaveBeenCalled();
    resolve();
    await waitFor(() => expect(state.router.replace).toHaveBeenCalledWith("/signin"));
  });

  it("allows OAuth accounts to confirm without a password", async () => {
    state.user!.hasPassword = false;
    render(<SettingsPage />);
    expect(screen.queryByLabelText("Current password")).toBeNull();
    confirm();
    fireEvent.click(screen.getByRole("button", { name: "Delete my account" }));
    await waitFor(() => expect(state.router.replace).toHaveBeenCalledWith("/signin"));
    expect(state.deleteAccount).toHaveBeenCalledWith("DELETE", "");
  });

  it("explains local cleanup failures without presenting deletion as retryable", async () => {
    state.deleteAccount.mockResolvedValueOnce(true);
    render(<SettingsPage />);
    confirm();
    password();
    fireEvent.click(screen.getByRole("button", { name: "Delete my account" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Your server account is deleted"));
    expect(screen.queryByRole("button", { name: "Delete my account" })).toBeNull();
    expect(screen.getByRole("link", { name: "Go to sign in" }).getAttribute("href")).toBe("/signin");
    expect(state.router.replace).not.toHaveBeenCalled();
  });

  it.each([
    "current password is incorrect",
    "release all active machines before deleting your account",
    "Could not delete account. Try again.",
  ])("preserves the page after rejection: %s", async (message) => {
    state.deleteAccount.mockRejectedValueOnce(new Error(message));
    render(<SettingsPage />);
    confirm();
    password();
    fireEvent.click(screen.getByRole("button", { name: "Delete my account" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(message));
    expect(state.router.replace).not.toHaveBeenCalled();
    expect((screen.getByLabelText("Current password") as HTMLInputElement).value).toBe("");
    password();
    fireEvent.click(screen.getByRole("button", { name: "Delete my account" }));
    await waitFor(() => expect(state.router.replace).toHaveBeenCalledWith("/signin"));
  });

  it("does not assume that missing password requirements mean OAuth", () => {
    delete state.user!.hasPassword;
    render(<SettingsPage />);
    expect(screen.queryByRole("button", { name: "Delete my account" })).toBeNull();
    expect(screen.getByRole("alert").textContent).toContain("Could not verify");
  });

  it("waits for authentication", () => {
    state.loading = true;
    render(<SettingsPage />);
    expect(screen.getByRole("status").textContent).toContain("Loading");
    expect(screen.queryByLabelText("Type DELETE to confirm")).toBeNull();
  });

  it("offers a retry while offline", () => {
    state.offline = true;
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(state.retry).toHaveBeenCalled();
    expect(screen.queryByLabelText("Type DELETE to confirm")).toBeNull();
  });

  it("redirects a signed-out visitor", async () => {
    state.user = null;
    render(<SettingsPage />);
    await waitFor(() => expect(state.router.replace).toHaveBeenCalledWith("/signin?next=%2Fsettings"));
    expect(screen.queryByLabelText("Type DELETE to confirm")).toBeNull();
  });

  it("does not carry deletion confirmation or passwords into another account", () => {
    const view = render(<SettingsPage />);
    confirm(); password();
    state.user = { ...state.user!, id: "another-owner", email: "another@example.test" };
    view.rerender(<SettingsPage />);
    expect((screen.getByLabelText("Type DELETE to confirm") as HTMLInputElement).value).toBe("");
    expect((screen.getByLabelText("Current password") as HTMLInputElement).value).toBe("");
    expect((screen.getByRole("button", { name: "Delete my account" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
