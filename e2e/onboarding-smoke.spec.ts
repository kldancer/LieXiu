import { expect, test } from "@playwright/test";
import { loginAsDefault, waitForPageText } from "./helpers";

test("personal v1 has no legacy onboarding route", async ({ page }) => {
  await loginAsDefault(page);

  await page.goto("/onboarding", { waitUntil: "domcontentloaded" });
  await waitForPageText(page, "Page not found");

  await expect(page).toHaveURL(/\/onboarding$/);
  await expect(page.getByText("Continue on web")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
});
