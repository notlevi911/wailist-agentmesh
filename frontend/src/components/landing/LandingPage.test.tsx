import { afterEach, assert, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { LandingPage } from "./LandingPage";

const push = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));
vi.mock("@/lib/nativeAuth", () => ({ IS_NATIVE: false }));

beforeEach(() => {
  push.mockReset();
  vi.stubGlobal("IntersectionObserver", class {
    observe() {}
    disconnect() {}
  });
  vi.stubGlobal("matchMedia", () => ({
    matches: true,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("retired waitlist landing", () => {
  it("keeps the product sections and footer without collecting email", () => {
    const { container } = render(<LandingPage signedIn={false} />);
    expect(container.querySelector("#pillars")).not.toBeNull();
    expect(container.querySelector("#flow")).not.toBeNull();
    expect(screen.getByRole("link", { name: "Privacy Policy" }).getAttribute("href")).toBe("/privacy");
    expect(container.querySelector("form, input[type=email], #waitlist")).toBeNull();
    expect(screen.queryByText(/join the waitlist/i)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Menu" }));
    expect(screen.queryByRole("button", { name: "Waitlist" })).toBeNull();
    expect(screen.getAllByRole("button", { name: "Overview" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "How it works" }).length).toBeGreaterThan(0);
  });

  it("keeps direct signup and studio sign-in available", () => {
    render(<LandingPage signedIn={false} />);
    const [signUp] = screen.getAllByRole("button", { name: "Sign Up" });
    assert.isDefined(signUp);
    fireEvent.click(signUp);
    expect(push).toHaveBeenLastCalledWith("/signup");
    const [openStudio] = screen.getAllByRole("button", { name: /Open Studio/ });
    assert.isDefined(openStudio);
    fireEvent.click(openStudio);
    expect(push).toHaveBeenLastCalledWith("/signin");
  });

  it("opens existing workflows for a signed-in user", () => {
    render(<LandingPage signedIn />);
    const [openStudio] = screen.getAllByRole("button", { name: /Open Studio/ });
    assert.isDefined(openStudio);
    fireEvent.click(openStudio);
    expect(push).toHaveBeenLastCalledWith("/workflows");
    expect(screen.queryByRole("button", { name: "Sign Up" })).toBeNull();
  });
});
