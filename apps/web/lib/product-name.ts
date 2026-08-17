export const DEFAULT_PRODUCT_NAME = "LieXiu";
export const DEFAULT_PRODUCT_URL = "http://localhost:3000";

export function resolveProductName(
  env: Record<string, string | undefined> = process.env,
): string {
  const configured = env.NEXT_PUBLIC_PRODUCT_NAME?.trim();
  return configured || DEFAULT_PRODUCT_NAME;
}

export function resolveProductUrl(
  env: Record<string, string | undefined> = process.env,
): URL {
  const configured =
    env.NEXT_PUBLIC_PRODUCT_URL?.trim() || env.FRONTEND_ORIGIN?.trim();
  if (configured) {
    try {
      const url = new URL(configured);
      if (url.protocol === "http:" || url.protocol === "https:") return url;
    } catch {
      // Fall through to the local self-host default.
    }
  }
  return new URL(DEFAULT_PRODUCT_URL);
}
