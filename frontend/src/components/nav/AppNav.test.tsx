import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AppNav } from "./AppNav";
import type { NavItem } from "@/lib/nav";

const device = vi.hoisted(() => ({ native: true }));
vi.mock("@/lib/nativeAuth", () => ({ get IS_NATIVE() { return device.native; } }));
vi.mock("@/hooks/useIsHandheld", () => ({ useIsHandheld: () => false }));

beforeEach(() => {
  device.native = true;
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  window.history.replaceState({ page: "settings", __NA: true }, "", "/settings");
});

afterEach(() => {
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function navigation(onSelect: (item: NavItem, options?: { replace: boolean }) => void) {
  return render(<AppNav
    items={[{ label: "Usage", href: "/usage" }, { label: "Settings", href: "/settings" }]}
    brand={<span>AgentMesh</span>}
    pathname="/settings"
    onSelect={onSelect}
    renderInlineLink={({ item, onClick }) => <button onClick={onClick}>Inline {item.label}</button>}
  />);
}

it("keeps a delayed route selection and reuses the sheet entry", () => {
  const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
  const initialLength = window.history.length;
  navigation((item, options) => {
    window.setTimeout(() => {
      window.history[options?.replace ? "replaceState" : "pushState"]({ page: "usage", __NA: true }, "", item.href);
    }, 50);
  });

  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  expect(window.history.length).toBe(initialLength + 1);
  fireEvent.click(screen.getByRole("button", { name: "Usage" }));
  act(() => { vi.advanceTimersByTime(1); });
  expect(back).not.toHaveBeenCalled();
  act(() => { vi.advanceTimersByTime(50); });
  expect(window.location.pathname).toBe("/usage");
  expect(window.history.length).toBe(initialLength + 1);
  expect(window.history.state).toEqual({ page: "usage", __NA: true });
});

it("keeps desktop inline navigation as a normal push", () => {
  device.native = false;
  const onSelect = vi.fn();
  const push = vi.spyOn(window.history, "pushState");
  navigation(onSelect);
  fireEvent.click(screen.getByRole("button", { name: "Inline Usage" }));
  expect(onSelect).toHaveBeenCalledWith({ label: "Usage", href: "/usage" }, { replace: false });
  expect(push).not.toHaveBeenCalled();
});

it("closes a same-route selection without another navigation", () => {
  const onSelect = vi.fn();
  const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
  navigation(onSelect);
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  act(() => { vi.runAllTimers(); });
  expect(onSelect).not.toHaveBeenCalled();
  expect(back).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Menu" }).getAttribute("aria-expanded")).toBe("false");
});

it("still removes the menu entry on Escape", () => {
  const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
  navigation(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  fireEvent.keyDown(document, { key: "Escape" });
  act(() => { vi.runAllTimers(); });
  expect(back).toHaveBeenCalledTimes(1);
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Menu" }));
});

it("closes on Back without navigating or traversing history twice", () => {
  const onSelect = vi.fn();
  const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
  navigation(onSelect);
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  act(() => { window.dispatchEvent(new PopStateEvent("popstate")); });
  act(() => { vi.runAllTimers(); });
  expect(back).not.toHaveBeenCalled();
  expect(onSelect).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "Menu" }).getAttribute("aria-expanded")).toBe("false");
});

it("keeps section selection separate from route replacement", () => {
  const onSelect = vi.fn();
  const back = vi.spyOn(window.history, "back").mockImplementation(() => {});
  render(<AppNav
    items={[{ label: "Overview", sectionId: "overview" }]}
    brand={<span>AgentMesh</span>}
    pathname=""
    onSelect={onSelect}
    renderInlineLink={() => null}
  />);
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  fireEvent.click(screen.getByRole("button", { name: "Overview" }));
  act(() => { vi.runAllTimers(); });
  expect(onSelect).toHaveBeenCalledWith({ label: "Overview", sectionId: "overview" });
  expect(back).toHaveBeenCalledTimes(1);
  expect(window.location.pathname).toBe("/settings");
});

it("exposes the disclosure target only while its links are available", () => {
  const { container } = navigation(vi.fn());
  const trigger = screen.getByRole("button", { name: "Menu" });
  expect(trigger.hasAttribute("aria-controls")).toBe(false);
  expect(screen.queryByRole("button", { name: "Usage" })).toBeNull();

  fireEvent.click(trigger);
  const target = document.getElementById(trigger.getAttribute("aria-controls")!);
  expect(target?.contains(screen.getByRole("button", { name: "Usage" }))).toBe(true);
  expect(screen.getByRole("button", { name: "Settings" }).getAttribute("aria-current")).toBe("page");

  fireEvent.click(trigger);
  expect(trigger.getAttribute("aria-expanded")).toBe("false");
  expect(trigger.hasAttribute("aria-controls")).toBe(false);
  expect(container.querySelector(".appnav__sheet")).toBeNull();
  expect(screen.queryByRole("button", { name: "Usage" })).toBeNull();
});

it("closes when focus leaves the disclosure without taking focus back", () => {
  navigation(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  const outside = screen.getByRole("button", { name: "Inline Usage" });
  act(() => { outside.focus(); });
  expect(document.activeElement).toBe(outside);
  expect(screen.getByRole("button", { name: "Menu" }).getAttribute("aria-expanded")).toBe("false");
});

it("releases the sheet and scroll lock when the desktop breakpoint is crossed", () => {
  let changed = () => {};
  const media = {
    matches: true,
    addEventListener: vi.fn((_event: string, listener: () => void) => { changed = listener; }),
    removeEventListener: vi.fn(),
  };
  vi.stubGlobal("matchMedia", () => media);
  const targets = [document.documentElement, document.body];
  const previous = targets.map(({ style }) => ({ style, x: style.overflowX, y: style.overflowY }));
  navigation(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Menu" }));
  for (const { style } of targets) {
    expect(style.overflowX).toBe("hidden");
    expect(style.overflowY).toBe("hidden");
  }
  act(() => { media.matches = false; changed(); });
  expect(screen.getByRole("button", { name: "Menu" }).getAttribute("aria-expanded")).toBe("false");
  previous.forEach(({ style, x, y }) => {
    expect(style.overflowX).toBe(x);
    expect(style.overflowY).toBe(y);
  });
});
