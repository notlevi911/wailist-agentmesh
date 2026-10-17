import { afterEach, expect, it, vi } from "vitest";
import { clearDeletedAccountStorage } from "./accountDeletion";
import { getSessionsSnapshot, refreshSessions } from "./helixbox";

afterEach(() => { localStorage.clear(); vi.restoreAllMocks(); });

it("removes cached personal data and clears the receipt cache", () => {
  const keys = ["agentmesh_checkout_phone", "agentmesh_credits_v1", "agentmesh_helixbox_sessions_v2",
    "agentmesh_lastrun_workflow-1", "agentmesh_settlements_user-1", "agentmesh_helixbox_sessions_v1"];
  for (const key of keys) localStorage.setItem(key, "private data");
  localStorage.setItem("agentmesh_helixbox_sessions_v2", '[{"code":"private","expiresAt":1}]');
  refreshSessions();
  expect(getSessionsSnapshot()).toHaveLength(1);
  localStorage.setItem("unrelated", "keep");
  clearDeletedAccountStorage();
  for (const key of keys) expect(localStorage.getItem(key)).toBeNull();
  expect(getSessionsSnapshot()).toEqual([]);
  expect(localStorage.getItem("unrelated")).toBe("keep");
});

it("reports storage failures so deletion cannot claim device cleanup succeeded", () => {
  localStorage.setItem("agentmesh_checkout_phone", "123");
  vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("storage unavailable"); });
  expect(clearDeletedAccountStorage).toThrow("storage unavailable");
});
