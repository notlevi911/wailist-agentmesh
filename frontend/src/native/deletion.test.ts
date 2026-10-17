import { afterEach, beforeEach, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ token: vi.fn(), optIn: vi.fn(), push: vi.fn(), geofences: vi.fn() }));
vi.mock("./auth", () => ({ clearTokenIf: state.token }));
vi.mock("./pushPrefs", () => ({ clearOptedIn: state.optIn }));
vi.mock("./push", () => ({ disablePush: state.push }));
vi.mock("./geofence", () => ({ clearAccountGeofences: state.geofences }));
import { clearDeletedAccount } from "./deletion";

beforeEach(() => {
  for (const fn of Object.values(state)) fn.mockReset().mockResolvedValue(undefined);
});
afterEach(() => vi.clearAllMocks());

it("clears the deleted session, device registration, consent and location data", async () => {
  await clearDeletedAccount("deleted-session");
  expect(state.token).toHaveBeenCalledWith("deleted-session");
  for (const fn of Object.values(state)) expect(fn).toHaveBeenCalledOnce();
});

it("attempts every cleanup even if the notification provider is unavailable", async () => {
  state.push.mockRejectedValueOnce(new Error("provider unavailable"));
  await expect(clearDeletedAccount("deleted-session")).rejects.toThrow("Device cleanup incomplete");
  for (const fn of Object.values(state)) expect(fn).toHaveBeenCalledOnce();
});
