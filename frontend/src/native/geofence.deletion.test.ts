import { expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ nativeClear: vi.fn(), queueClear: vi.fn(), removeListener: vi.fn() }));
vi.mock("@capacitor/network", () => ({ Network: { addListener: async () => ({ remove: state.removeListener }) } }));
vi.mock("./nativeGeofence", () => ({ Geofence: {
  hasPermission: async () => ({ granted: true }),
  addGeofence: async () => {},
  clearAccountData: state.nativeClear,
} }));
vi.mock("./queue", () => ({ pending: async () => [], clear: state.queueClear }));
import { start, clearAccountGeofences } from "./geofence";

it("disarms persisted fences and clears queued fixes even when the bridge reports failure", async () => {
  await start({ workflowId: "owned", lat: 1, lng: 2, radiusM: 200 });
  state.nativeClear.mockRejectedValueOnce(new Error("OS unavailable"));
  await expect(clearAccountGeofences()).rejects.toThrow("Location cleanup incomplete");
  expect(state.nativeClear).toHaveBeenCalledOnce();
  expect(state.queueClear).toHaveBeenCalledOnce();
  expect(state.removeListener).toHaveBeenCalledOnce();
  state.nativeClear.mockResolvedValue(undefined);
  await clearAccountGeofences();
  expect(state.removeListener).toHaveBeenCalledOnce();
});
