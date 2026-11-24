import { readFile } from "node:fs/promises";
import { expect, test, type Page } from "@playwright/test";

// A fixture session from cmd/testfixture. The import below runs its preview
// against the real archive (a dry run rolls back) and mocks only the final
// write, because specs share one fixture archive.
const SESSION = "test-session-duration-showcase";
const MISSING = "missing-curation-transfer-session";

async function openPanel(page: Page) {
  await page.goto("/settings");
  await expect(page.locator(".settings-loading")).toHaveCount(0, { timeout: 10_000 });
  const nav = page.getByRole("navigation", { name: "Settings" });
  await nav.locator("button", { hasText: "Move stars and folders" }).click();
  await expect(page.getByRole("heading", { name: "Move stars and folders" })).toBeVisible();
}

test("exports stars and folders as a dated JSON download", async ({ page }) => {
  await openPanel(page);

  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export to file" }).click();
  const download = await downloaded;

  expect(download.suggestedFilename()).toMatch(/^agentsview-curation-\d{4}-\d{2}-\d{2}\.json$/);
  const path = await download.path();
  expect(path).toBeTruthy();
  const doc = JSON.parse(await readFile(path!, "utf8"));
  expect(doc.format).toBe("agentsview-curation");
  expect(doc.version).toBe(1);
  expect(Array.isArray(doc.sessions)).toBe(true);
  expect(Array.isArray(doc.folders)).toBe(true);
  await expect(
    page.getByRole("status").filter({ hasText: download.suggestedFilename() }),
  ).toBeVisible();
});

test("previews an import against this archive, then applies it", async ({ page }) => {
  const doc = {
    format: "agentsview-curation",
    version: 1,
    sessions: [
      {
        id: SESSION,
        agent: "copilot",
        project: "project-duration",
        starred: true,
        folder: "Curation transfer e2e",
      },
      { id: MISSING, agent: "claude", project: "elsewhere", starred: true },
    ],
    folders: [{ project: "project-duration", name: "Curation transfer e2e" }],
  };
  const applied: unknown[] = [];
  await page.route("**/api/v1/curation/import**", async (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.get("dry_run") === "true") {
      await route.continue();
      return;
    }
    applied.push(route.request().postDataJSON());
    await route.fulfill({
      json: {
        dry_run: false,
        sessions: 2,
        matched: 1,
        matched_by_prefix: 0,
        unmatched: [MISSING],
        ambiguous: [],
        stars: { added: 1, unchanged: 0, kept: 0 },
        end_marks: { added: 0, unchanged: 0, kept: 0 },
        folder_members: { added: 1, unchanged: 0, kept: 0 },
        folders_created: 1,
      },
    });
  });
  await openPanel(page);

  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Choose file…" }).click();
  await (await chooser).setFiles({
    name: "curation.json",
    mimeType: "application/json",
    buffer: Buffer.from(JSON.stringify(doc)),
  });

  const panel = page.locator(".curation-transfer");
  await expect(panel).toContainText("Matched sessions");
  await expect(panel).toContainText("1 of 2");
  await expect(panel.locator('[data-group="unmatched"] li')).toHaveText([MISSING]);
  await expect(panel.locator('tr[data-row="folder-members"] td')).toHaveText([
    "Sessions in folders",
    "1",
    "0",
    "0",
  ]);
  const stars = await panel.locator('tr[data-row="stars"] td').allTextContents();
  expect(stars[0]).toBe("Stars");
  expect(Number(stars[1]) + Number(stars[2])).toBe(1);
  expect(applied).toHaveLength(0);

  const refreshed = Promise.all(
    ["/api/v1/starred", "/api/v1/session-folders", "/api/v1/session-end-marks"].map((path) =>
      page.waitForRequest(
        (request) => request.method() === "GET" && new URL(request.url()).pathname === path,
      ),
    ),
  );
  await panel.getByRole("button", { name: "Import", exact: true }).click();

  await expect(panel).toContainText(
    "Import complete. The sidebar now shows the imported stars, ended marks, and folders.",
  );
  await refreshed;
  expect(applied).toEqual([doc]);
});
