import { expect, test, type Page, type Route } from "@playwright/test";
import type { DbMessage as Message } from "../src/lib/api/generated/index.js";

// Reuse the existing backend session; only its message window and watch
// stream are controlled.
const SESSION_ID = "test-session-xlarge-5500";

function message(ordinal: number, needle: boolean): Message {
  // Tall rows, so a jump lands among rows measured for the first time.
  const lines = Array.from({ length: 6 }, (_, line) => `Line ${line} of message ${ordinal}`);
  if (needle) lines[3] = "The needle is here";
  const content = lines.join("\n\n");
  return {
    id: 980000 + ordinal,
    session_id: SESSION_ID,
    ordinal,
    role: ordinal % 2 === 0 ? "user" : "assistant",
    content,
    content_length: content.length,
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
  };
}

/** Open a 60-message transcript whose watch stream the test drives. */
async function openLiveSession(page: Page, newestFirst: boolean) {
  const all = Array.from({ length: 60 }, (_, ordinal) => message(ordinal, ordinal === 30));
  const watches: Route[] = [];
  await page.addInitScript(() => {
    localStorage.setItem("agentsview-transcript-mode", "normal");
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
    const url = new URL(route.request().url());
    const descending = url.searchParams.get("direction") === "desc";
    const from = Number(url.searchParams.get("from") ?? (descending ? all.length - 1 : 0));
    const limit = Number(url.searchParams.get("limit") ?? 1000);
    const messages = descending
      ? all.slice(Math.max(0, from - limit + 1), from + 1).reverse()
      : all.slice(from, from + limit);
    await route.fulfill({ json: { messages, count: messages.length } });
  });
  await page.goto(`/sessions/${SESSION_ID}`);
  const transcript = page.locator(".message-list-scroll");
  await expect(transcript).toHaveAttribute("data-loaded", "true");
  if (newestFirst) await page.getByLabel("Toggle sort order").click();

  /** The session gains a message containing the query, as a running agent's would. */
  async function appendLiveMatch() {
    all.push(message(all.length, true));
    await expect.poll(() => watches.length).toBeGreaterThan(0);
    await watches.shift()!.fulfill({
      status: 200,
      headers: { "content-type": "text/event-stream" },
      body: "retry: 50\nevent: session_updated\ndata: {}\n\n",
    });
  }
  return { transcript, appendLiveMatch };
}

async function find(page: Page) {
  const modifier = await page.evaluate(() => (/Mac/.test(navigator.platform) ? "Meta" : "Control"));
  await page.keyboard.press(`${modifier}+f`);
  await expect(page.locator(".kit-find-bar__input")).toBeFocused();
  await page.locator(".kit-find-bar__input").fill("needle");
  await expect(page.locator(".search-announcement")).toHaveText("Match 1 of 1");
}

/** Give a late scroll from the update time to land before checking it did not. */
async function frames(page: Page, count: number) {
  await page.evaluate(async (n) => {
    for (let i = 0; i < n; i++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  }, count);
}

async function scrollTop(page: Page) {
  return page.locator(".message-list-scroll").evaluate((el) => Math.round(el.scrollTop));
}

test.describe("session find during live updates", () => {
  for (const newestFirst of [false, true]) {
    test(`keeps the reader's place with newestFirst=${newestFirst}`, async ({ page }) => {
      const { transcript, appendLiveMatch } = await openLiveSession(page, newestFirst);
      const current = transcript.locator('[data-search-current="true"]');
      await find(page);
      await expect(current).toHaveAttribute("data-search-block", "30:text:0");
      await expect(current).toBeInViewport();

      // An update that only adds rows keeps the revealed match where it was.
      await appendLiveMatch();
      await expect(page.locator(".search-announcement")).toHaveText(/ of 2$/);
      await frames(page, 20);
      await expect(current).toHaveAttribute("data-search-block", "30:text:0");
      await expect(current).toBeInViewport();

      // The reader moves on toward the newest messages.
      await transcript.hover();
      await page.mouse.wheel(0, newestFirst ? -3000 : 3000);
      await expect(current).not.toBeInViewport();
      await expect
        .poll(async () => {
          const before = await scrollTop(page);
          await frames(page, 5);
          return (await scrollTop(page)) === before;
        })
        .toBe(true);
      const readerTop = await scrollTop(page);

      await appendLiveMatch();
      await expect(page.locator(".search-announcement")).toHaveText(/ of 3$/);
      await frames(page, 20);
      expect(await scrollTop(page)).toBe(readerTop);
      await expect(current).not.toBeInViewport();
    });
  }

  test("keeps following the latest message after it is turned on during find", async ({ page }) => {
    const { transcript, appendLiveMatch } = await openLiveSession(page, true);
    await find(page);
    await expect(transcript.locator('[data-search-current="true"]')).toBeInViewport();

    const follow = page.getByLabel("Follow latest messages");
    await follow.click();
    await expect(follow).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => scrollTop(page)).toBe(0);

    // Newest-first puts the new row above the match, which moves its row.
    await appendLiveMatch();
    await expect(page.locator(".search-announcement")).toHaveText(/ of 2$/);
    await frames(page, 20);
    await expect(follow).toHaveAttribute("aria-pressed", "true");
    await expect(transcript.getByText("Line 0 of message 60")).toBeInViewport();
  });
});
