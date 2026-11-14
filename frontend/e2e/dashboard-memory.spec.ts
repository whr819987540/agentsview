import { test, expect, type CDPSession, type Page } from "@playwright/test";

// Leaving the dashboard must free its charts. A retained chart keeps its whole
// detached DOM tree alive, so the transcript view would carry a dead copy of
// the dashboard for the rest of the page's life.
test.describe("dashboard memory", () => {
  test.skip(
    ({ browserName }) => browserName !== "chromium",
    "DOM counters come from the Chromium DevTools protocol",
  );

  async function domCounters(cdp: CDPSession) {
    // Two collections: the first can only queue finalizers for wrappers.
    await cdp.send("HeapProfiler.collectGarbage");
    await cdp.send("HeapProfiler.collectGarbage");
    const { nodes, jsEventListeners } = await cdp.send("Memory.getDOMCounters");
    return { nodes, jsEventListeners };
  }

  async function firstSessionId(page: Page): Promise<string> {
    const response = await page.request.get("/api/v1/sessions?project=project-alpha");
    expect(response.ok()).toBe(true);
    const body = await response.json();
    const id = body.sessions?.[0]?.id;
    expect(typeof id).toBe("string");
    return id as string;
  }

  test("leaving the dashboard releases its DOM", async ({ page }) => {
    const sessionId = await firstSessionId(page);
    const cdp = await page.context().newCDPSession(page);
    const row = page.locator(`.session-item[data-session-id="${sessionId}"]`);
    const transcript = page.locator(".virtual-row").first();

    // Baseline: the transcript in a page that has not shown the dashboard yet.
    await page.goto(`/sessions/${encodeURIComponent(sessionId)}`);
    await expect(transcript).toBeVisible();
    await expect(row).toBeVisible();
    const baseline = await domCounters(cdp);

    await page.locator(".breadcrumb-link").click();
    await page.getByRole("button", { name: "Open dashboard" }).click();
    await expect(page.locator(".analytics-content")).toHaveAttribute("aria-busy", "false");
    await expect(page.locator(".lc-root-container").first()).toBeVisible();
    const dashboard = await domCounters(cdp);
    const dashboardNodes = dashboard.nodes - baseline.nodes;
    const dashboardListeners = dashboard.jsEventListeners - baseline.jsEventListeners;
    expect(dashboardNodes).toBeGreaterThan(1_000);

    await row.locator(".session-info-link").click();
    await expect(transcript).toBeVisible();
    await expect(page.locator(".analytics-page")).toHaveCount(0);
    const after = await domCounters(cdp);

    // Compiled templates and other one-time caches may stay, but not the
    // dashboard itself.
    expect(after.nodes - baseline.nodes).toBeLessThan(dashboardNodes / 4);
    expect(after.jsEventListeners - baseline.jsEventListeners).toBeLessThan(dashboardListeners / 4);
  });
});
