import { expect, test, type Page } from "@playwright/test";
import { SessionsPage } from "./pages/sessions-page";
import {
  createMockSessions,
  handleSessionsRoute,
  sessionsRoutePattern,
} from "./helpers/mock-sessions";

const PROJECT = "folders-project";

interface FolderRow {
  id: number;
  project: string;
  name: string;
  session_ids: string[];
  created_at: string;
  updated_at: string;
}

/**
 * In-memory stand-in for the session folder API. Specs share one fixture
 * archive, so folders written there would hide sessions from other specs.
 */
async function mockFolderApi(page: Page): Promise<FolderRow[]> {
  const folders: FolderRow[] = [];
  let nextId = 1;
  const list = () => ({ folders, editable: true });
  const nameTaken = (project: string, name: string) =>
    folders.some((f) => f.project === project && f.name === name);
  const unfile = (ids: string[]) => {
    for (const f of folders) f.session_ids = f.session_ids.filter((id) => !ids.includes(id));
  };

  await page.route(/\/api\/v1\/session-folders(?:\/[^?]*)?(?:\?.*)?$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1\/session-folders/, "");
    const method = request.method();
    const body = request.postData() ? request.postDataJSON() : {};
    const byId = path.match(/^\/(\d+)(\/sessions)?$/);
    const folder = byId ? folders.find((f) => f.id === Number(byId[1])) : undefined;

    if (path === "" && method === "GET") {
      await route.fulfill({ json: list() });
    } else if (path === "" && method === "POST") {
      if (nameTaken(body.project, body.name)) {
        await route.fulfill({ status: 409, json: { error: "name taken" } });
        return;
      }
      const now = new Date().toISOString();
      const created = {
        id: nextId++,
        project: body.project,
        name: body.name,
        session_ids: [],
        created_at: now,
        updated_at: now,
      };
      folders.push(created);
      await route.fulfill({ status: 201, json: created });
    } else if (folder && !byId?.[2] && method === "PATCH") {
      if (nameTaken(folder.project, body.name)) {
        await route.fulfill({ status: 409, json: { error: "name taken" } });
        return;
      }
      folder.name = body.name;
      await route.fulfill({ json: folder });
    } else if (path === "/remove-sessions" && method === "POST") {
      unfile(body.session_ids);
      await route.fulfill({ json: list() });
    } else if (folder && byId?.[2] && method === "PUT") {
      unfile(body.session_ids);
      folder.session_ids.push(...body.session_ids);
      await route.fulfill({ json: list() });
    } else if (folder && method === "DELETE") {
      folders.splice(folders.indexOf(folder), 1);
      await route.fulfill({ status: 204, body: "" });
    } else {
      await route.fulfill({ status: 404, json: { error: "session folder not found" } });
    }
  });
  return folders;
}

async function openProject(page: Page): Promise<SessionsPage> {
  const sessions = createMockSessions(3, "fold", () => PROJECT);
  await page.route(sessionsRoutePattern, handleSessionsRoute([{ sessions, project: null }]));
  await page.route("**/api/v1/projects*", (route) =>
    route.fulfill({ json: { projects: [{ name: PROJECT, session_count: 3 }] } }),
  );
  const sp = new SessionsPage(page);
  await sp.goto();
  await sp.filterByProject(PROJECT);
  await expect(sp.sessionItems).toHaveCount(3);
  return sp;
}

function sessionRow(page: Page, id: string) {
  return page.locator(`.session-item[data-session-id="${id}"]`);
}

test("files sessions into a new folder by dragging them", async ({ browserName, page }) => {
  test.skip(browserName !== "chromium", "HTML5 drag and drop is verified on Chromium");
  const folders = await mockFolderApi(page);
  const sp = await openProject(page);

  await page.getByRole("button", { name: "New folder" }).click();
  const nameInput = page.getByRole("textbox", { name: "Folder name" });
  await nameInput.fill("Specs");
  await nameInput.press("Enter");
  const folderRow = page.locator(".folder-row", { hasText: "Specs" });
  await expect(folderRow.locator(".folder-count")).toHaveText("0");

  await sessionRow(page, "fold-0").dragTo(folderRow);

  await expect(folderRow.locator(".folder-count")).toHaveText("1");
  await expect(sp.sessionItems).toHaveCount(2);
  expect(folders.map((f) => [f.name, f.session_ids])).toEqual([["Specs", ["fold-0"]]]);

  await folderRow.click();
  await expect(sp.sessionItems).toHaveCount(3);
  await expect(sp.sessionItems.first()).toHaveAttribute("data-session-id", "fold-0");

  // Dropping on a row outside any folder takes the session back out.
  await sessionRow(page, "fold-0").dragTo(sessionRow(page, "fold-2"));
  await expect(folderRow.locator(".folder-count")).toHaveText("0");
  expect(folders[0]!.session_ids).toEqual([]);

  await folderRow.click({ button: "right" });
  await page.getByRole("button", { name: "Delete folder" }).click();
  await expect(page.locator(".folder-row")).toHaveCount(0);
  await expect(sp.sessionItems).toHaveCount(3);
});

test("moves a session into a folder created from its menu", async ({ page }) => {
  const folders = await mockFolderApi(page);
  const sp = await openProject(page);

  await sessionRow(page, "fold-1").click({ button: "right" });
  await page.getByRole("button", { name: "Move to folder…" }).click();
  const picker = page.getByRole("dialog", { name: "Move to folder" });
  await picker.getByRole("textbox", { name: "New folder name" }).fill("Later");
  await picker.getByRole("button", { name: "Create" }).click();

  await expect(picker).toHaveCount(0);
  const folderRow = page.locator(".folder-row", { hasText: "Later" });
  await expect(folderRow.locator(".folder-count")).toHaveText("1");
  await expect(sp.sessionItems).toHaveCount(2);
  expect(folders.map((f) => [f.project, f.name, f.session_ids])).toEqual([
    [PROJECT, "Later", ["fold-1"]],
  ]);
});
