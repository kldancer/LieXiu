import { describe, expect, it } from "vitest";
import {
  DEFAULT_PRODUCT_NAME,
  DEFAULT_PRODUCT_URL,
  resolveProductName,
  resolveProductUrl,
} from "./product-name";

describe("resolveProductName", () => {
  it("uses the configured public product name", () => {
    expect(resolveProductName({ NEXT_PUBLIC_PRODUCT_NAME: "  Acme Studio  " })).toBe("Acme Studio");
  });

  it("falls back to the default product name", () => {
    expect(resolveProductName({ NEXT_PUBLIC_PRODUCT_NAME: " " })).toBe(DEFAULT_PRODUCT_NAME);
    expect(resolveProductName({})).toBe(DEFAULT_PRODUCT_NAME);
  });
});

describe("resolveProductUrl", () => {
  it("uses the public URL before the server-side frontend origin", () => {
    expect(
      resolveProductUrl({
        NEXT_PUBLIC_PRODUCT_URL: "https://studio.example.test/app",
        FRONTEND_ORIGIN: "https://internal.example.test",
      }).href,
    ).toBe("https://studio.example.test/app");
  });

  it("falls back safely for missing or non-http URLs", () => {
    expect(resolveProductUrl({ FRONTEND_ORIGIN: "https://studio.example.test" }).href).toBe(
      "https://studio.example.test/",
    );
    expect(resolveProductUrl({ NEXT_PUBLIC_PRODUCT_URL: "javascript:alert(1)" }).href).toBe(
      `${DEFAULT_PRODUCT_URL}/`,
    );
  });
});
