import { expect, test, type Locator, type Page } from "@playwright/test";
import { SessionsPage } from "./pages/sessions-page";

// Later than any fixture session's activity, so a mark always holds.
const MARK_AT = "2100-01-01T00:00:00Z";

/** Serve end marks from memory, so marking leaves the fixture archive
 *  alone. Returns the kind of each session's mark. */
async function routeEndMarks(page: Page): Promise<Map<string, string>> {
  const kinds = new Map<string, string>();
  await page.route("**/api/v1/session-end-marks", (route) =>
    route.fulfill({
      json: {
        editable: true,
        marks: [...kinds].map(([session_id, kind]) => ({
          session_id,
          activity_at: MARK_AT,
          kind,
        })),
      },
    }),
  );
  await page.route(/\/api\/v1\/sessions\/[^/]+\/ended(\?|$)/, async (route) => {
    const url = new URL(route.request().url());
    const id = decodeURIComponent(url.pathname.split("/").at(-2)!);
    const kind = url.searchParams.get("kind") ?? "row";
    if (route.request().method() === "PUT") {
      kinds.set(id, kind);
      await route.fulfill({ json: { session_id: id, activity_at: MARK_AT, kind } });
      return;
    }
    if (kinds.get(id) === kind) kinds.delete(id);
    await route.fulfill({ status: 204 });
  });
  return kinds;
}

/** Serve stars from memory, so starring leaves the fixture archive alone. */
async function routeStars(page: Page) {
  const ids = new Set<string>();
  await page.route("**/api/v1/starred", (route) =>
    route.fulfill({ json: { session_ids: [...ids] } }),
  );
  await page.route("**/api/v1/sessions/*/star", async (route) => {
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2)!);
    if (route.request().method() === "PUT") ids.add(id);
    else ids.delete(id);
    await route.fulfill({ status: 204 });
  });
}

function measure(row: Locator) {
  return row.evaluate((el) => {
    const box = (selector: string) => {
      const rect = el.querySelector(selector)!.getBoundingClientRect();
      return { left: rect.left, width: rect.width };
    };
    const slot = el.querySelector<HTMLElement>(".end-slot")!;
    return {
      name: box(".session-name"),
      end: box(".end-slot button"),
      star: box(".star-btn"),
      endOpacity: getComputedStyle(slot).opacity,
    };
  });
}

/** Move the pointer and focus off the sidebar, which also releases sort
 *  holds. Like the star, the end button shows while its row has focus. */
async function leaveSidebar(page: Page) {
  await page.mouse.move(1200, 600);
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
}

test("the end button keeps its place and fills in, like the star", async ({ page }) => {
  await routeEndMarks(page);
  const sp = new SessionsPage(page);
  await sp.goto();
  await expect(page.locator(".end-slot button").first()).toBeAttached();

  const id = await sp.sessionItems.first().getAttribute("data-session-id");
  const row = page.locator(`.session-item[data-session-id="${id}"]`);
  await leaveSidebar(page);
  const rest = await measure(row);
  expect(rest.endOpacity).toBe("0");

  await row.hover();
  await expect.poll(async () => (await measure(row)).endOpacity).toBe("1");
  const hovered = await measure(row);
  expect(hovered.name).toEqual(rest.name);
  expect(hovered.end).toEqual(rest.end);
  expect(hovered.star).toEqual(rest.star);

  await row.getByRole("button", { name: "Mark as ended" }).click();
  await expect(row.getByRole("button", { name: "Unmark as ended" })).toBeVisible();
  await leaveSidebar(page);

  // Off hover, the ended row shows its filled button and no check by the title.
  await expect.poll(async () => (await measure(row)).endOpacity).toBe("1");
  const ended = await measure(row);
  expect(ended.name.width).toBe(rest.name.width);
  expect(ended.end.width).toBe(rest.end.width);
  await expect(row.locator(".status-slot svg")).toHaveCount(0);
  const fill = await row.locator(".end-slot circle").evaluate((c) => getComputedStyle(c).fill);
  expect(fill).not.toBe("none");

  await row.hover();
  await row.getByRole("button", { name: "Unmark as ended" }).click();
  await expect(row.getByRole("button", { name: "Mark as ended" })).toBeVisible();
  await leaveSidebar(page);
  await expect.poll(async () => (await measure(row)).endOpacity).toBe("0");
  expect(await row.locator(".end-slot circle").evaluate((c) => getComputedStyle(c).fill)).toBe(
    "none",
  );
});

test("a fork row holds its own star and end mark; subagent rows hold neither", async ({ page }) => {
  const kinds = await routeEndMarks(page);
  await routeStars(page);
  const sp = new SessionsPage(page);
  await sp.goto();

  const root = page.locator('.session-item[data-session-id="test-session-medium-8"]');
  const fork = page.locator('.session-item[data-session-id="test-session-fork-1"]');
  await root.getByRole("button", { name: "Expand", exact: true }).click();
  await expect(fork).toBeVisible();

  // Like a root row, the fork row keeps both slots in place on hover.
  await leaveSidebar(page);
  const rest = await measure(fork);
  expect(rest.endOpacity).toBe("0");
  await fork.hover();
  await expect.poll(async () => (await measure(fork)).endOpacity).toBe("1");
  const hovered = await measure(fork);
  expect(hovered.name).toEqual(rest.name);
  expect(hovered.star).toEqual(rest.star);

  await fork.getByRole("button", { name: "Star session" }).click();
  await expect(fork.locator(".star-btn.starred")).toBeVisible();
  await expect(root.locator(".star-btn.starred")).toHaveCount(0);

  await fork.getByRole("button", { name: "Mark as ended" }).click();
  await expect(fork.getByRole("button", { name: "Unmark as ended" })).toBeVisible();
  expect(kinds.get("test-session-fork-1")).toBe("branch");
  await leaveSidebar(page);
  await expect(fork.locator(".end-slot.ended")).toBeVisible();
  await expect(root.locator(".end-slot.ended")).toHaveCount(0);

  // The root row's mark is its own, and leaves the fork's in place.
  await root.hover();
  await root.getByRole("button", { name: "Mark as ended" }).click();
  await expect(root.getByRole("button", { name: "Unmark as ended" })).toBeVisible();
  expect(kinds.get("test-session-medium-8")).toBe("row");
  await root.getByRole("button", { name: "Unmark as ended" }).click();
  await expect(root.getByRole("button", { name: "Mark as ended" })).toBeVisible();
  await expect(fork.locator(".end-slot.ended")).toBeVisible();

  const parent = page.locator('.session-item[data-session-id="test-session-small-5"]');
  // Expanding a row also opens its Subagents group.
  await parent.getByRole("button", { name: "Expand", exact: true }).click();
  const subagent = page.locator('.session-item[data-session-id="test-session-subagent-1"]');
  await expect(subagent).toBeVisible();
  await subagent.hover();
  await expect(subagent.locator(".star-btn")).toHaveCount(0);
  await expect(subagent.locator(".end-slot")).toHaveCount(0);
});
