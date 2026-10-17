import { expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn(), remove: vi.fn(), drain: vi.fn() }));
vi.mock("@capacitor/preferences", () => ({ Preferences: state }));
vi.mock("./nativeGeofence", () => ({ Geofence: { drainNativeQueue: state.drain } }));
import { pending, clear } from "./queue";

it("waits for an in-flight queue drain before removing all local coordinates", async () => {
  let release!: (value: { value: string }) => void;
  state.drain.mockReturnValue(new Promise((resolve) => { release = resolve; }));
  state.get.mockResolvedValue({ value: "[]" });
  state.set.mockResolvedValue(undefined);
  state.remove.mockResolvedValue(undefined);
  const reading = pending();
  await Promise.resolve();
  const deleting = clear();
  expect(state.remove).not.toHaveBeenCalled();
  release({ value: JSON.stringify([{ workflowId: "deleted", lat: 1, lng: 2, recordedAt: new Date().toISOString() }]) });
  await Promise.all([reading, deleting]);
  expect(state.remove).toHaveBeenCalledWith({ key: "agentmesh.geofence.queue" });
  expect(state.remove.mock.invocationCallOrder[0]).toBeGreaterThan(state.set.mock.invocationCallOrder.at(-1)!);
});
