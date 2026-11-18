import { expect, test, type Route } from "@playwright/test";
import type { DbMessage as Message } from "../src/lib/api/generated/index.js";

// Chromium shares about six HTTP/1.1 connections per host among all tabs of a
// profile, and the daemon serves plain HTTP. Long-lived streams count against
// that pool for as long as they stay open.
test.describe("several open tabs", () => {
  test("a third session tab loads while two others stay open", async ({ context, browserName }) => {
    test.skip(browserName !== "chromium", "WebKit sizes its per-host connection pool differently");
    const response = await context.request.get("/api/v1/sessions?limit=20");
    expect(response.ok()).toBe(true);
    const body = (await response.json()) as { sessions: { id: string; message_count: number }[] };
    const ids = body.sessions.filter((s) => s.message_count > 0).map((s) => s.id);
    expect(ids.length).toBeGreaterThanOrEqual(3);

    // Each tab keeps its session's watch stream open. Before tabs shared one
    // events stream, two session tabs held four of the six connections and
    // the third tab's own streams took the rest, so its transcript request
    // never got a connection.
    for (const id of ids.slice(0, 3)) {
      const page = await context.newPage();
      await page.goto(`/sessions/${encodeURIComponent(id)}`);
      await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
      await expect(page.locator(".virtual-row").first()).toBeVisible();
    }
  });

  test("a hidden tab holds live updates and catches up once when shown", async ({ page }) => {
    const SESSION_ID = "test-session-xlarge-5500";
    const message = (ordinal: number, text: string): Message => ({
      id: 990000 + ordinal,
      session_id: SESSION_ID,
      ordinal,
      role: ordinal % 2 === 0 ? "user" : "assistant",
      content: text,
      content_length: text.length,
      timestamp: "2026-01-01T00:00:00Z",
      has_thinking: false,
      thinking_text: "",
      has_tool_use: false,
      model: "",
      context_tokens: 0,
      has_context_tokens: false,
      output_tokens: 0,
      has_output_tokens: false,
      is_system: false,
    });
    const all = Array.from({ length: 12 }, (_, i) => message(i, `Message ${i}`));
    const watches: Route[] = [];
    let messageReads = 0;

    await page.addInitScript(() => {
      let hidden = false;
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => (hidden ? "hidden" : "visible"),
      });
      Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
      (window as unknown as { setTabHidden: (h: boolean) => void }).setTabHidden = (h) => {
        hidden = h;
        document.dispatchEvent(new Event("visibilitychange"));
      };
    });
    await page.route(`**/api/v1/sessions/${SESSION_ID}/watch*`, (route) => {
      watches.push(route);
    });
    await page.route(`**/api/v1/sessions/${SESSION_ID}`, async (route) => {
      const response = await route.fetch();
      const session = await response.json();
      await route.fulfill({ response, json: { ...session, message_count: all.length } });
    });
    await page.route(`**/api/v1/sessions/${SESSION_ID}/messages*`, async (route) => {
      messageReads++;
      const url = new URL(route.request().url());
      const from = Number(url.searchParams.get("from") ?? 0);
      const limit = Number(url.searchParams.get("limit") ?? 1000);
      const messages = all.slice(from, from + limit);
      await route.fulfill({ json: { messages, count: messages.length } });
    });

    await page.goto(`/sessions/${SESSION_ID}`);
    await expect(page.locator(".message-list-scroll")).toHaveAttribute("data-loaded", "true");
    await expect.poll(() => watches.length).toBeGreaterThan(0);
    const setHidden = (hidden: boolean) =>
      page.evaluate(
        (h) => (window as unknown as { setTabHidden: (h: boolean) => void }).setTabHidden(h),
        hidden,
      );

    await setHidden(true);
    const readsBefore = messageReads;
    all.push(message(all.length, "Written while the tab was hidden"));
    await watches.shift()!.fulfill({
      status: 200,
      headers: { "content-type": "text/event-stream" },
      body: "retry: 50\nevent: session_updated\ndata: {}\n\n",
    });
    // The stream reconnects after the update; the update itself waits.
    await expect.poll(() => watches.length).toBeGreaterThan(0);
    await page.waitForTimeout(500);
    expect(messageReads).toBe(readsBefore);
    await expect(page.getByText("Written while the tab was hidden")).toHaveCount(0);

    await setHidden(false);
    await expect(page.getByText("Written while the tab was hidden")).toBeVisible();
    await page.waitForTimeout(500);
    expect(messageReads).toBe(readsBefore + 1);
  });
});
