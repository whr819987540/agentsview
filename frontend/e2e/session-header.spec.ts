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

/** Pixels the session actions menu button runs past the header's right edge. */
function actionsOverflow(page: Page): Promise<number> {
  return page.evaluate(() => {
    const header = document.querySelector(".session-breadcrumb")!.getBoundingClientRect();
    const button = document
      .querySelector(".session-breadcrumb .actions-btn")!
      .getBoundingClientRect();
    return Math.round(button.right - header.right);
  });
}

/** Wait until the badges that load after the session itself are in the
 *  header, so the check sees its widest content. They stay in the page when a
 *  narrow header hides them. */
async function waitForAllBadges(page: Page) {
  const header = page.locator(".session-breadcrumb");
  await expect(header.locator(".usage-breakdown")).toBeAttached();
  await expect(header.locator(".cost-badge")).toBeAttached();
  await expect(header.locator(".model-badge")).toBeAttached();
}

async function expectActionsReachable(page: Page, label: string) {
  await waitForAllBadges(page);
  await expect.poll(() => actionsOverflow(page), label).toBeLessThanOrEqual(0);
  await page.locator(".session-breadcrumb .actions-btn").click();
  await expect(page.locator(".actions-menu"), label).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".actions-menu")).toHaveCount(0);
}

// The room left for the header depends on the sidebar and the analysis panel
// beside it as well as on the window.
for (const layout of [
  { name: "the default sidebar", sidebar: "260", analysis: "false" },
  { name: "a wide sidebar", sidebar: "520", analysis: "false" },
  { name: "the analysis panel open", sidebar: "260", analysis: "true" },
]) {
  test(`keeps the session actions inside the header with ${layout.name}`, async ({ page }) => {
    await page.addInitScript(
      ([sidebar, analysis]) => {
        localStorage.setItem("agentsview-sidebar-width", sidebar!);
        localStorage.setItem("agentsview-session-vitals", analysis!);
      },
      [layout.sidebar, layout.analysis],
    );
    for (const width of [761, 820, 901, 960, 1100, 1280]) {
      await page.setViewportSize({ width, height: 600 });
      await page.goto(`/sessions/${SESSION}`);
      await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
      await expectActionsReachable(page, `${width} px wide`);
    }
  });
}

test("keeps every badge and button in a header with room for them", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 600 });
  await page.goto(`/sessions/${SESSION}`);
  await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
  await waitForAllBadges(page);

  const header = page.locator(".session-breadcrumb");
  await expect(header.locator(".session-time")).toBeVisible();
  await expect(header.locator(".session-id")).toBeVisible();
  await expect(header.locator(".usage-breakdown")).toBeVisible();
  await expect(header.locator(".agent-badge").first()).toBeVisible();
  await expect(
    header.locator('.bulk-block-btn[aria-label="Collapse visible blocks"]'),
  ).toBeVisible();
});

test("fits the header again when another session with more badges opens", async ({ page }) => {
  // At this width the few badges of the first session fit beside the actions
  // and the second session's do not, while the header keeps its width.
  await page.setViewportSize({ width: 900, height: 600 });
  await page.goto("/sessions/test-session-tool-sequences");
  await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
  const header = page.locator(".session-breadcrumb");
  await expect(header.locator(".session-time")).toBeVisible();

  await page.evaluate((id) => {
    history.pushState(null, "", `/sessions/${id}`);
    dispatchEvent(new PopStateEvent("popstate"));
  }, SESSION);

  await expectActionsReachable(page, "after the switch");
});

test("fits the header again when the window narrows and widens", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 600 });
  await page.goto(`/sessions/${SESSION}`);
  await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
  await waitForAllBadges(page);
  const time = page.locator(".session-breadcrumb .session-time");
  await expect(time).toBeVisible();

  await page.setViewportSize({ width: 901, height: 600 });
  await expectActionsReachable(page, "narrowed");

  await page.setViewportSize({ width: 1440, height: 600 });
  await expect(time).toBeVisible();
});
