import { expect, test } from "@playwright/test";
import { loginAsDefault, waitForPageText } from "./helpers";

test("personal v1 settings omit retired custom issue properties", async ({ page }) => {
  const workspaceSlug = await loginAsDefault(page);
  await page.goto(`/${workspaceSlug}/settings?tab=properties`, {
    waitUntil: "domcontentloaded",
  });
  await waitForPageText(page, "Settings");

  await expect(page.getByRole("tab", { name: "General" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Properties" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "New property" })).toHaveCount(0);
});
