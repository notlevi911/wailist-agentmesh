import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import DeleteAccountPage from "./page";
import { config } from "@/middleware";

afterEach(cleanup);

it("offers deletion on the web without requiring the installed app", () => {
  render(<DeleteAccountPage />);
  expect(screen.getByRole("heading", { level: 1 }).textContent).toContain("AgentMesh");
  expect(screen.getByRole("link", { name: "Sign in to delete your account" }).getAttribute("href"))
    .toBe("/signin?next=%2Fsettings");
  expect(screen.getByRole("link", { name: /Request deletion help/ }).getAttribute("href"))
    .toBe("mailto:privacy@agent-mesh.app?subject=AgentMesh%20account%20deletion");
  expect(screen.getByText(/Release any active machines/)).toBeTruthy();
  expect(screen.getByText(/Public blockchain transactions cannot be erased/)).toBeTruthy();
  expect(config.matcher.some((route) => route.startsWith("/delete-account"))).toBe(false);
});
