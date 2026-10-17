import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AccountSection } from "./Account";

const changePassword = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api", () => ({ auth: { changePassword } }));
const user = { id: "owner", email: "owner@example.test", name: "Owner", orgName: "Team", needsOnboarding: false, hasPassword: true, createdAt: "2026-01-01T00:00:00Z" };
afterEach(cleanup);
beforeEach(() => changePassword.mockReset().mockResolvedValue(undefined));
function fill(label: string, value: string) { fireEvent.change(screen.getByLabelText(label), { target: { value } }); }

it("saves profile fields and surfaces a rejected save", async () => {
  const save = vi.fn().mockRejectedValueOnce(new Error("Could not save profile")).mockResolvedValue(undefined);
  render(<AccountSection user={user} saveProfile={save} disabled={false} />);
  fill("Name", " Updated "); fill("Organization", " New ");
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Could not save"));
  expect(save).toHaveBeenCalledWith("Updated", "New");
  fireEvent.click(screen.getByRole("button", { name: "Save profile" }));
  await waitFor(() => expect(screen.getByText("Profile saved.")).toBeTruthy());
});

it("requires matching bounded passwords and clears secrets after rejection", async () => {
  changePassword.mockRejectedValueOnce(new Error("current password is incorrect"));
  render(<AccountSection user={user} saveProfile={vi.fn()} disabled={false} />);
  fill("Current password for password change", "old-password");
  fill("New password", "é".repeat(37)); fill("Repeat new password", "é".repeat(37));
  const submit = screen.getByRole("button", { name: "Change password" }) as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  fill("New password", "new-password"); fill("Repeat new password", "different-password");
  expect(submit.disabled).toBe(true);
  fill("Repeat new password", "new-password");
  fireEvent.click(submit);
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("current password is incorrect"));
  for (const label of ["Current password for password change", "New password", "Repeat new password"]) expect((screen.getByLabelText(label) as HTMLInputElement).value).toBe("");
  expect(changePassword).toHaveBeenCalledExactlyOnceWith("old-password", "new-password");
});

it("prevents duplicate submissions and states that sessions remain active", async () => {
  let finish!: () => void;
  changePassword.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
  render(<AccountSection user={user} saveProfile={vi.fn()} disabled={false} />);
  fill("Current password for password change", "old-password"); fill("New password", "new-password"); fill("Repeat new password", "new-password");
  const form = screen.getByRole("form", { name: "Change password" });
  fireEvent.submit(form); fireEvent.submit(form);
  expect(changePassword).toHaveBeenCalledOnce();
  finish();
  await waitFor(() => expect(screen.getByText(/Existing signed-in sessions remain active/)).toBeTruthy());
});

it("does not offer password creation to provider accounts", () => {
  render(<AccountSection user={{ ...user, hasPassword: false }} saveProfile={vi.fn()} disabled={false} />);
  expect(screen.queryByLabelText("New password")).toBeNull();
  expect(screen.getByText(/Manage your password with that provider/)).toBeTruthy();
});

