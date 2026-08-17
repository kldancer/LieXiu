import { describe, expect, it } from "vitest";
import { workspaceUrlHost } from "./workspace-url";

describe("workspaceUrlHost", () => {
  it("returns the host of a full app URL", () => {
    expect(workspaceUrlHost("https://liexiu.example.com")).toBe(
      "liexiu.example.com",
    );
  });

  it("ignores scheme, path, and trailing slash", () => {
    expect(workspaceUrlHost("https://liexiu.example.com/")).toBe(
      "liexiu.example.com",
    );
    expect(workspaceUrlHost("http://liexiu.example.com/app/onboarding")).toBe(
      "liexiu.example.com",
    );
  });

  it("preserves a non-default port", () => {
    expect(workspaceUrlHost("https://my.host:3000")).toBe("my.host:3000");
  });

  it("accepts a bare host without a scheme", () => {
    expect(workspaceUrlHost("liexiu.example.com")).toBe("liexiu.example.com");
    expect(workspaceUrlHost("liexiu.example.com/path")).toBe(
      "liexiu.example.com",
    );
  });

  it("falls back to the brand host when no app URL is configured", () => {
    expect(workspaceUrlHost("")).toBe("liexiu.ai");
    expect(workspaceUrlHost("   ")).toBe("liexiu.ai");
    expect(workspaceUrlHost(null)).toBe("liexiu.ai");
    expect(workspaceUrlHost(undefined)).toBe("liexiu.ai");
  });
});
