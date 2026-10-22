import { expect, test, type Locator, type Page } from "@playwright/test";
import {
  distanceFromEnd,
  FakeTranscript,
  frames,
  laidOutHeight,
  openTranscript,
  routeSessionWatch,
  setNewestFirst,
  settledReadingPosition,
  visibleOrdinals,
} from "./helpers/transcript-fixture";

// More than 3,000 messages, so the list loads the newest 1,000 first.
const PROGRESSIVE_SESSION = "test-session-xlarge-5500";
const LIVE_SESSION = "test-session-large-200";

function scrollToBottomButton(page: Page) {
  return page.locator('.session-breadcrumb button[aria-label="Scroll to bottom"]');
}

/** Wait for the transcript to stop moving, then check that it shows the end
 *  of the list with `ordinal` as the last row on screen. */
async function expectAtBottom(scroller: Locator, ordinal: number) {
  await settledReadingPosition(scroller);
  expect(await distanceFromEnd(scroller), "pixels below the viewport").toBeLessThanOrEqual(1);
  expect((await visibleOrdinals(scroller)).at(-1), "last row on screen").toBe(ordinal);
}

test("oldest first lands on the newest message after its rows are measured", async ({ page }) => {
  const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
  await fake.install(page);
  const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
  expect((await visibleOrdinals(scroller))[0]).toBe(0);

  await scrollToBottomButton(page).click();

  await expectAtBottom(scroller, 79);
  // A single jump, not follow latest.
  await expect(page.getByLabel("Follow latest messages")).toHaveAttribute("aria-pressed", "false");
});

test("newest first loads the older history and lands on the first message", async ({ page }) => {
  const fake = FakeTranscript.withRows(PROGRESSIVE_SESSION, 5500);
  await fake.install(page);
  const scroller = await openTranscript(page, `/sessions/${PROGRESSIVE_SESSION}`);
  await setNewestFirst(page, scroller);
  // A slow page of older history: landing on the oldest loaded row before
  // it arrives would leave the reader above the rows it brings.
  const olderPage = fake.holdNext(
    (url) => url.searchParams.get("direction") === "desc" && url.searchParams.has("from"),
  );

  await scrollToBottomButton(page).click();
  await olderPage.arrived;
  await frames(page, 30);
  olderPage.release();

  await expectAtBottom(scroller, 0);
});

test("newest first turns follow latest off so new messages leave the reader at the bottom", async ({
  page,
}) => {
  const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
  await fake.install(page);
  const watch = await routeSessionWatch(page, LIVE_SESSION);
  const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
  await setNewestFirst(page, scroller);
  const follow = page.getByLabel("Follow latest messages");
  await follow.click();
  await expect(follow).toHaveAttribute("aria-pressed", "true");

  await scrollToBottomButton(page).click();
  await expect(follow).toHaveAttribute("aria-pressed", "false");
  await expectAtBottom(scroller, 0);

  const heightBefore = await laidOutHeight(scroller);
  fake.append(3);
  await watch.notifyUpdated();
  await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 100);
  await frames(page, 10);
  await expectAtBottom(scroller, 0);
});

test("leaves a find match behind when new messages move its row", async ({ page }) => {
  const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
  await fake.install(page);
  const watch = await routeSessionWatch(page, LIVE_SESSION);
  const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
  await setNewestFirst(page, scroller);
  const modifier = await page.evaluate(() => (/Mac/.test(navigator.platform) ? "Meta" : "Control"));
  await page.keyboard.press(`${modifier}+f`);
  await page.locator(".kit-find-bar__input").fill("Scroll anchor row 40.");
  await expect(page.locator(".search-announcement")).toHaveText("Match 1 of 1");
  await expect(scroller.locator('[data-search-current="true"]')).toBeInViewport();

  await scrollToBottomButton(page).click();
  await expectAtBottom(scroller, 0);

  // Newest first, new messages go above the match and move its row.
  const heightBefore = await laidOutHeight(scroller);
  fake.append(3);
  await watch.notifyUpdated();
  await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 100);
  await frames(page, 10);
  await expectAtBottom(scroller, 0);
});

test("narrow headers offer the jump in the session actions menu", async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 600 });
  const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
  await fake.install(page);
  const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
  await expect(scrollToBottomButton(page)).toBeHidden();

  await page.locator(".session-breadcrumb .actions-btn").click();
  await page.locator(".actions-menu-item", { hasText: "Scroll to bottom" }).click();

  await expect(page.locator(".actions-menu")).toHaveCount(0);
  await expectAtBottom(scroller, 79);
});
