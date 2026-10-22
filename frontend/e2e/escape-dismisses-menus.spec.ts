import { expect, test, type Locator, type Page } from "@playwright/test";

const SESSION = "test-session-duration-showcase";

async function openSession(page: Page) {
  await page.setViewportSize({ width: 1440, height: 800 });
  await page.goto(`/sessions/${SESSION}`);
  await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
}

function sessionShown(page: Page): Promise<boolean> {
  return page.evaluate(
    (id) =>
      location.pathname === `/sessions/${id}` &&
      document.querySelector(".session-breadcrumb") !== null,
    SESSION,
  );
}

/** Let the frame after a keypress render, so a session that Escape closed has
 *  left the page before the check. */
async function frames(page: Page, count: number) {
  await page.evaluate(async (n) => {
    for (let i = 0; i < n; i++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  }, count);
}

/** One Escape closes the popover on top and leaves the session open. */
async function expectEscapeClosesOnly(page: Page, popover: Locator) {
  await expect(popover).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(popover).toHaveCount(0);
  await frames(page, 3);
  expect(await sessionShown(page), "session still open").toBe(true);
}

test.describe("Escape in a popover on the session page", () => {
  test.beforeEach(async ({ page }) => {
    await openSession(page);
  });

  test("closes the session actions menu", async ({ page }) => {
    await page.locator(".session-breadcrumb .actions-btn").click();
    await expectEscapeClosesOnly(page, page.locator(".actions-menu"));
  });

  test("closes the resume menu", async ({ page }) => {
    await page.locator(".session-breadcrumb .resume-btn").click();
    await expectEscapeClosesOnly(page, page.locator(".open-menu"));
  });

  test("closes the usage breakdown", async ({ page }) => {
    await page.locator(".session-breadcrumb .usage-breakdown-trigger").click();
    await expectEscapeClosesOnly(page, page.locator(".usage-breakdown-menu"));
  });

  test("closes a session's menu in the sidebar", async ({ page }) => {
    await page.locator(`.session-item[data-session-id="${SESSION}"]`).click({ button: "right" });
    await expectEscapeClosesOnly(page, page.locator(".context-menu"));
  });

  test("closes the move to folder picker", async ({ page }) => {
    await page.locator(`.session-item[data-session-id="${SESSION}"]`).click({ button: "right" });
    await page.locator(".context-menu-item", { hasText: "Move to folder" }).click();
    await expectEscapeClosesOnly(page, page.locator(".folder-picker"));
  });
});

// Opening the session from the list mounts the header after the global
// shortcuts, so their Escape handler runs first unless it waits for the menus.
test("Escape closes the resume menu of a session opened from the list", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 800 });
  await page.goto("/sessions");
  await page.locator(`.session-item[data-session-id="${SESSION}"]`).click();
  await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");

  await page.locator(".session-breadcrumb .resume-btn").click();
  await expectEscapeClosesOnly(page, page.locator(".open-menu"));
});

test("Escape with nothing open still returns to the session list", async ({ page }) => {
  await openSession(page);
  await page.locator(".message-list-scroll").click();

  await page.keyboard.press("Escape");

  await expect(page.locator(".session-breadcrumb")).toHaveCount(0);
  await expect(page).not.toHaveURL(new RegExp(SESSION));
});
