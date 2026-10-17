import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConnectionsSection } from "./Connections";

const api = vi.hoisted(() => ({ listCredentials: vi.fn(), deleteCredential: vi.fn() }));
vi.mock("@/lib/api", () => ({ oauth2: api }));
const credential = { id: "credential", provider: "google", accountLabel: "owner@example.test", scopes: "", createdAt: "2026-01-01T00:00:00Z", expiresAt: "" };
beforeEach(() => { api.listCredentials.mockReset().mockResolvedValue([credential]); api.deleteCredential.mockReset(); });
afterEach(cleanup);
it("keeps a connection visible after failed removal and removes it only after confirmed success", async () => {
  api.deleteCredential.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
  render(<ConnectionsSection disabled={false} />);
  fireEvent.click(await screen.findByRole("button", { name: "Disconnect owner@example.test" }));
  expect(api.deleteCredential).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnect" }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Could not disconnect"));
  expect(screen.getByText("owner@example.test")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnect" }));
  await waitFor(() => expect(screen.getByText(/No accounts connected here/)).toBeTruthy());
  expect(api.deleteCredential).toHaveBeenCalledTimes(2);
});
it("shows a load failure and allows retry without claiming an empty list", async () => {
  api.listCredentials.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
  render(<ConnectionsSection disabled={false} />);
  await screen.findByRole("alert");
  expect(screen.queryByText(/No accounts connected here/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Retry connections" }));
  await screen.findByText(/No accounts connected here/);
});

