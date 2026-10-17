import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SettingsPage } from "./SettingsPage";
import type { UserSettings } from "@/lib/api";

const api = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), publish: vi.fn(), revision: 0,
  user: { id: "owner", email: "owner@example.test", name: "Owner", orgName: "", needsOnboarding: false, hasPassword: false } }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: vi.fn() }) }));
vi.mock("@/components/Topbar", () => ({ Topbar: () => <nav>Account navigation</nav> }));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ user: api.user, completeOnboarding: vi.fn(), deleteAccount: vi.fn() }),
  getAccountRevision: () => api.revision, publishAccountThreshold: api.publish,
}));
vi.mock("@/lib/api", () => ({ settings: { get: api.get, update: api.update } }));
vi.mock("./sections/Account", () => ({ AccountSection: () => null }));
vi.mock("./sections/Connections", () => ({ ConnectionsSection: () => null }));
vi.mock("./sections/Developer", () => ({ DeveloperSection: () => null }));
const preferences: UserSettings = { lowBalanceUsdMicros: 5_000_000, maxCallSpendUsdMicros: null,
  limits: { minCallSpendUsdMicros: 50_000, maxCallSpendUsdMicros: 1_000_000_000, maxLowBalanceUsdMicros: Number.MAX_SAFE_INTEGER } };
beforeEach(() => { api.revision++; api.user.id = "owner"; api.get.mockReset().mockResolvedValue(preferences); api.update.mockReset(); api.publish.mockClear(); });
afterEach(cleanup);

it("keeps deletion usable when loading preferences fails and supports retry", async () => {
  api.get.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(preferences);
  render(<SettingsPage />);
  await screen.findByText("Could not load spending preferences.");
  expect(screen.getByLabelText("Type DELETE to confirm")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry preferences" }));
  await screen.findByLabelText("Low-balance threshold (USD)");
  expect(screen.queryByLabelText(/currency/i)).toBeNull();
});

it("publishes a saved threshold immediately without accepting another field's stale response", async () => {
  api.update.mockResolvedValue({ ...preferences, lowBalanceUsdMicros: 1_234_567 });
  render(<SettingsPage />);
  const field = await screen.findByLabelText("Low-balance threshold (USD)");
  fireEvent.change(field, { target: { value: "1.234567" } });
  fireEvent.click(screen.getByRole("button", { name: "Save balance warning" }));
  await screen.findByText("Balance warning saved.");
  expect(api.publish).toHaveBeenLastCalledWith("owner", 1_234_567, api.revision);
  api.publish.mockClear();
  api.update.mockResolvedValue({ ...preferences, maxCallSpendUsdMicros: null });
  fireEvent.click(screen.getByRole("button", { name: "Save per-call limit" }));
  await screen.findByText("Per-call limit saved.");
  expect(api.publish).not.toHaveBeenCalled();
});

it("ignores a save response after changing accounts", async () => {
  let finish!: (value: UserSettings) => void;
  api.update.mockReturnValue(new Promise<UserSettings>((resolve) => { finish = resolve; }));
  const view = render(<SettingsPage />);
  const field = await screen.findByLabelText("Low-balance threshold (USD)");
  fireEvent.change(field, { target: { value: "99" } });
  fireEvent.click(screen.getByRole("button", { name: "Save balance warning" }));
  api.user = { ...api.user, id: "next-owner" }; api.revision++;
  view.rerender(<SettingsPage />);
  await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2));
  await screen.findByLabelText("Low-balance threshold (USD)");
  api.publish.mockClear();
  await act(async () => finish({ ...preferences, lowBalanceUsdMicros: 99_000_000 }));
  expect(api.publish).not.toHaveBeenCalled();
  expect((screen.getByLabelText("Low-balance threshold (USD)") as HTMLInputElement).value).toBe("5");
});

