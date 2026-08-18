import { test, expect } from "@playwright/test";
import { loginAsDefault, openWorkspaceMenu, waitForPageText } from "./helpers";

test.describe("Authentication", () => {
  test("personal mode enters the canonical workspace from /login", async ({ page }) => {
    await page.goto("/login", { waitUntil: "domcontentloaded" });
    await waitForPageText(page, "New Issue");

    await expect(page).toHaveURL(/\/issues$/);
    await expect(page.getByRole("button", { name: "New Issue" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Email" })).toHaveCount(0);
  });

  test("owner bootstrap token opens the canonical workspace", async ({ page }) => {
    const workspaceSlug = await loginAsDefault(page);

    await expect(page).toHaveURL(new RegExp(`/${workspaceSlug}/issues$`));
    await expect(page.getByRole("button", { name: "New Issue" })).toBeVisible();
  });

  test("explicit logout clears the session and shows the bootstrap-secret gate", async ({ page }) => {
    await loginAsDefault(page);

    // Open the workspace dropdown menu
    await openWorkspaceMenu(page);

    await page.getByRole("menuitem", { name: "Log out" }).click();

    await waitForPageText(page, "Bootstrap secret");
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("textbox", { name: "Bootstrap secret" })).toBeVisible();
  });
});
