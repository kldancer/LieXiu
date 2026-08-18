import { test, expect } from "@playwright/test";
import { loginAsDefault, waitForPageText } from "./helpers";

test.describe("Settings", () => {
  test("updating workspace name reflects in sidebar immediately", async ({
    page,
  }) => {
    const workspaceSlug = await loginAsDefault(page);

    // Read the current workspace name from the sidebar
    const sidebarName = page.locator('[data-slot="sidebar-header"] button').first();
    const originalName = (await sidebarName.innerText()).split("\n").pop()?.trim() ?? "LieXiu";

    await page.goto(`/${workspaceSlug}/settings?tab=workspace`, { waitUntil: "domcontentloaded" });
    await waitForPageText(page, "General");

    // Change workspace name
    const nameInput = page
      .locator('input[type="text"]')
      .first();
    await nameInput.clear();
    const newName = "Renamed WS " + Date.now();
    await nameInput.fill(newName);

    // Workspace details auto-save on blur.
    await nameInput.blur();
    await expect(page.getByRole("status")).toContainText("Saved", { timeout: 5000 });

    // Sidebar should reflect the new name WITHOUT page refresh
    await expect(page.getByRole("button", { name: new RegExp(newName) }).first()).toBeVisible();

    // Restore original name so other tests aren't affected
    await nameInput.clear();
    await nameInput.fill(originalName.trim());
    await nameInput.blur();
    await expect(page.getByRole("status")).toContainText("Saved", { timeout: 5000 });
    await expect(page.getByRole("button", { name: new RegExp(originalName) }).first()).toBeVisible();
  });

});
