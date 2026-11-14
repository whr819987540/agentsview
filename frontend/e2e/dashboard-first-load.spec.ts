import { test, expect } from "@playwright/test";

test.describe("Chromium dashboard evidence", () => {
  test.skip(({ browserName }) => browserName !== "chromium", "the fixture evidence is recorded on Chromium");

  test("root hydrates sessions and reads analytics only after the dashboard opens", async ({ page }) => {
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "GET") requests.push(new URL(request.url()).pathname);
  });
  const fixture = await page.request.get("/api/v1/sessions?project=project-alpha");
  expect(fixture.ok()).toBe(true);
  const body = await fixture.json();
  expect(body.sessions).toHaveLength(3);
  expect(body.sessions.every((session: { project: string }) => session.project === "project-alpha")).toBe(true);
  await page.goto("/", { waitUntil: "commit" });
  await expect.poll(() => requests.some((path) => /^\/api\/v1\/sessions\/test-session-/.test(path))).toBe(true);
  await expect(page.locator(".session-item").first()).toBeVisible();
  const openButton = page.getByRole("button", { name: "Open dashboard" });
  await expect(openButton).toBeVisible();
  // The launcher has rendered and the visible rows have hydrated, so a
  // dashboard that loaded on its own would have requested its data by now.
  expect(requests.filter((path) => path.includes("/analytics/"))).toEqual([]);
  await expect(page.locator(".analytics-page")).toHaveCount(0);

  await openButton.click();
  await expect(page.locator(".analytics-page")).toBeVisible();
  await expect.poll(() => requests.some((path) => path.includes("/analytics/"))).toBe(true);
  const hydration = requests.findIndex((path) => /^\/api\/v1\/sessions\/test-session-/.test(path));
  const analytics = requests.findIndex((path) => path.includes("/analytics/"));
  console.log(JSON.stringify({ fixture: "project-alpha", fixtureRows: body.sessions.length, hydration, analytics, requests }));
  expect(hydration).toBeGreaterThanOrEqual(0);
  expect(hydration).toBeLessThan(analytics);
  });

  for (const width of [1280, 768, 400]) {
    test(`root shell at ${width}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.route("**/api/v1/analytics/**", () => {});
      await page.goto("/");
      await page.getByRole("button", { name: "Open dashboard" }).click();
      await expect(page.locator(".analytics-page")).toBeVisible();
      await expect(page.locator(".analytics-toolbar")).toBeVisible();
      await page.screenshot({ path: testInfo.outputPath(`1592-2-after-${width}.png`) });
    });
  }
});
