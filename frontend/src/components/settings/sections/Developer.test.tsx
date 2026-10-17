import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { DeveloperSection } from "./Developer";

const list = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", () => ({ BASE: "/api", workflows: { list } }));
beforeEach(() => { list.mockReset().mockResolvedValue([{ id: "workflow-id", name: "Webhook workflow" }]); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it("describes the deployed webhook and secret requirements and copies only the endpoint", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(<DeveloperSection />);
  const input = await screen.findByLabelText("Webhook endpoint") as HTMLInputElement;
  expect(screen.getByText(/X-Webhook-Secret/).textContent).toContain("Only deployed workflows with a webhook trigger");
  expect(input.value).toBe(`POST ${window.location.origin}/api/run/workflow-id`);
  fireEvent.click(screen.getByRole("button", { name: "Copy endpoint" }));
  await screen.findByText("Endpoint copied.");
  expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/api/run/workflow-id`);
});
it("surfaces workflow listing failure instead of an empty list", async () => {
  list.mockRejectedValue(new Error("offline"));
  render(<DeveloperSection />);
  await screen.findByRole("alert");
  expect(screen.queryByText("No workflows yet.")).toBeNull();
});
