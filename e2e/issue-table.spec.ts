import { expect, test, type Request } from "@playwright/test";
import { createTestApi, loginAsDefault, reloadAppPage } from "./helpers";

type TableRequestBody = {
  group?: { kind?: string };
  group_key?: string | null;
  page?: { limit?: number; cursor?: string | null };
};

function tableBody(request: Request): TableRequestBody {
  return (request.postDataJSON() ?? {}) as TableRequestBody;
}

test("List uses bounded server status branches in the canonical workspace", async ({
  page,
}) => {
  const api = await createTestApi();
  const run = Date.now();
  const todoTitle = `E2E List Todo ${run}`;
  const doneTitle = `E2E List Done ${run}`;
  await api.seedTableIssues([
    { title: todoTitle, status: "todo", position: -2 },
    { title: doneTitle, status: "done", position: -1 },
  ]);

  try {
    await loginAsDefault(page);
    const requests: TableRequestBody[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/api/issues/table/rows") {
        requests.push(tableBody(request));
      }
    });

    await page.getByRole("button", { name: "List", exact: true }).click();
    await reloadAppPage(page);

    await expect(page.getByText(todoTitle)).toBeVisible();
    await expect(page.getByText(doneTitle)).toBeVisible();
    await expect.poll(() => requests.length).toBeGreaterThan(0);
    expect(
      requests.every(
        (body) =>
          body.group?.kind === "status" &&
          typeof body.group_key === "string" &&
          (body.page?.limit ?? 0) > 0 &&
          (body.page?.limit ?? 0) <= 50,
      ),
    ).toBe(true);
  } finally {
    await api.cleanup();
  }
});
