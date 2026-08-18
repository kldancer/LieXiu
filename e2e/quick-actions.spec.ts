import { expect, test } from "@playwright/test";
import { loginAsDefault, waitForPageText } from "./helpers";

test("personal v1 exposes issue quick-action configuration without chat", async ({ page }) => {
  const workspaceSlug = await loginAsDefault(page);
  await page.goto(`/${workspaceSlug}/settings?tab=quick-actions`, {
    waitUntil: "domcontentloaded",
  });
  await waitForPageText(page, "Quick Actions");

  await expect(page.getByRole("tab", { name: "Quick Actions" })).toBeVisible();
  await expect(page).not.toHaveURL(/\/chat/);
});
