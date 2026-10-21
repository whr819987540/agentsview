import { test, expect, type Page } from "@playwright/test";

const SESSION = "test-session-duration-showcase";

// Where the bulk block buttons would push the header's actions past the
// viewport, the header hides them and the session actions menu offers the
// same commands.
async function runBulkCommand(page: Page, label: string, inMenu: boolean) {
  const button = page.locator(`.session-breadcrumb .bulk-block-btn[aria-label="${label}"]`);
  const menuItem = page.locator(".actions-menu-item", { hasText: label });
  await page.locator(".session-breadcrumb .actions-btn").click();
  await expect(page.locator(".actions-menu")).toBeVisible();
  if (inMenu) {
    await expect(button).toBeHidden();
    await expect(menuItem).toBeVisible();
    await menuItem.click();
    await expect(page.locator(".actions-menu")).toHaveCount(0);
  } else {
    await expect(menuItem).toBeHidden();
    await page.locator(".session-breadcrumb .actions-btn").click();
    await expect(page.locator(".actions-menu")).toHaveCount(0);
    await button.click();
  }
}

test("keeps the bulk block commands reachable at every header width", async ({ page }) => {
  for (const [width, inMenu] of [
    [1280, false],
    [768, true],
    [400, true],
  ] as const) {
    await page.setViewportSize({ width, height: 600 });
    await page.goto(`/sessions/${SESSION}`);
    const toolHeader = page.locator(".message-list-scroll .tool-header").first();
    await expect(toolHeader).toHaveAttribute("aria-expanded", "false");

    await runBulkCommand(page, "Expand visible blocks", inMenu);
    await expect(toolHeader).toHaveAttribute("aria-expanded", "true");
    await runBulkCommand(page, "Collapse visible blocks", inMenu);
    await expect(toolHeader).toHaveAttribute("aria-expanded", "false");

    const actions = await page.locator(".session-breadcrumb .actions-wrapper").boundingBox();
    expect(actions).not.toBeNull();
    expect(actions!.x + actions!.width).toBeLessThanOrEqual(width);
  }
});
