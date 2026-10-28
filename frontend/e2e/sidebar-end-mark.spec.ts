import { expect, test, type Locator, type Page } from "@playwright/test";
import { SessionsPage } from "./pages/sessions-page";

// Later than any fixture session's activity, so a mark always holds.
const MARK_AT = "2100-01-01T00:00:00Z";

/** Serve end marks from memory, so marking leaves the fixture archive alone. */
async function routeEndMarks(page: Page) {
  const marks = new Map<string, string>();
  await page.route("**/api/v1/session-end-marks", (route) =>
    route.fulfill({
      json: {
        editable: true,
        marks: [...marks].map(([session_id, activity_at]) => ({ session_id, activity_at })),
      },
    }),
  );
  await page.route("**/api/v1/sessions/*/ended", async (route) => {
    const id = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2)!);
    if (route.request().method() === "PUT") {
      marks.set(id, MARK_AT);
      await route.fulfill({ json: { session_id: id, activity_at: MARK_AT } });
      return;
    }
    marks.delete(id);
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
