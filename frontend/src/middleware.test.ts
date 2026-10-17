import { describe, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { middleware, config } from "./middleware";

describe("Settings route protection", () => {
  it("matches Settings and nested pages", () => {
    expect(config.matcher).toContain("/settings");
    expect(config.matcher).toContain("/settings/:path*");
    for (const path of ["/settings", "/settings/account"]) {
      const response = middleware(new NextRequest(`https://app.example.test${path}`));
      const location = new URL(response.headers.get("location")!);
      expect(location.pathname).toBe("/signin");
      expect(location.searchParams.get("next")).toBe(path);
    }
  });

  it("lets the page verify an existing session with the backend", () => {
    const response = middleware(new NextRequest("https://app.example.test/settings", {
      headers: { cookie: "agentmesh_ui=1" },
    }));
    expect(response.headers.get("location")).toBeNull();
  });
});
