import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

const push = vi.hoisted(() => vi.fn());
const replace = vi.hoisted(() => vi.fn());
const device = vi.hoisted(() => ({ native: false }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace }),
  usePathname: () => "/workflows",
}));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ user: { name: "Morgan", email: "morgan@example.test" } }),
}));
vi.mock("@/hooks/useIsHandheld", () => ({ useIsHandheld: () => false }));
vi.mock("@/lib/nativeAuth", () => ({ get IS_NATIVE() { return device.native; } }));
import { Topbar } from "./Topbar";

beforeEach(() => { device.native = false; });
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it("opens account deletion directly from the signed-in profile icon", () => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  render(<Topbar />);
  expect(screen.queryByRole("button", { name: "Delete account" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete account" }));
  expect(push).toHaveBeenCalledWith("/settings#delete-title");
  expect(screen.queryByRole("button", { name: "Delete account" })).toBeNull();
});

it("opens Settings from the same profile menu", () => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  render(<Topbar />);
  fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(push).toHaveBeenCalledWith("/settings");
  expect(screen.queryByRole("button", { name: "Settings" })).toBeNull();
});

it("replaces the native menu entry when selecting Usage", () => {
  device.native = true;
  vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  window.history.replaceState({ __NA: true }, "", "/settings");
  render(<Topbar />);
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  fireEvent.click(screen.getAllByRole("button", { name: "Usage" }).at(-1)!);
  expect(replace).toHaveBeenCalledWith("/usage");
  expect(push).not.toHaveBeenCalled();
  expect(window.history.state.__amSheet).toBeUndefined();
});
