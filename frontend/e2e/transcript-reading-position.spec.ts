import { expect, test, type Locator, type Page, type Route } from "@playwright/test";
import {
  FakeTranscript,
  frames,
  laidOutHeight,
  openTranscript,
  routeSessionWatch,
  settledReadingPosition,
  transcriptRow,
  visibleOrdinals,
  wheel,
  type ReadingPosition,
} from "./helpers/transcript-fixture";

// Each test pairs an event that changes the transcript with a way of reading
// it, per docs/agents/testing.md. Tests marked test.fail() reproduce a known
// defect on main; the fix for that defect removes the marker.

// More than 3,000 messages, so the list loads the newest 1,000 first.
const PROGRESSIVE_SESSION = "test-session-xlarge-5500";
const LIVE_SESSION = "test-session-large-200";
const OTHER_SESSION = "test-session-medium-100";
const FORK_SESSION = "test-session-fork-1";
const FORK_PARENT = "test-session-medium-8";

function isOlderPage(url: URL) {
  return url.searchParams.get("direction") === "desc" && url.searchParams.has("from");
}

/** Pixels between the transcript's top edge and the row's top, or null when
 *  the row is not rendered. A row scrolled to the top sits 8 px down, below
 *  the list's padding. */
function rowTop(scroller: Locator, ordinal: number): Promise<number | null> {
  return scroller.evaluate((el, target) => {
    const row = [...el.querySelectorAll(".virtual-row")].find((candidate) =>
      candidate.textContent?.includes(`Scroll anchor row ${target}.`),
    );
    if (!row) return null;
    return Math.round(row.getBoundingClientRect().top - el.getBoundingClientRect().top);
  }, ordinal);
}

function distanceFromEnd(scroller: Locator): Promise<number> {
  return scroller.evaluate((el) => Math.round(el.scrollHeight - el.clientHeight - el.scrollTop));
}

async function setNewestFirst(page: Page, scroller: Locator) {
  await page.getByLabel("Toggle sort order").click();
  await expect
    .poll(async () => {
      const shown = await visibleOrdinals(scroller);
      return shown.length > 1 && shown[0]! > shown[1]!;
    })
    .toBe(true);
}

async function expectSamePosition(scroller: Locator, before: ReadingPosition) {
  const after = await settledReadingPosition(scroller);
  expect(after.ordinal, "row at the top of the transcript").toBe(before.ordinal);
  expect(Math.abs(after.offset - before.offset), "offset into that row").toBeLessThanOrEqual(2);
}

test.describe("older history", () => {
  test("keeps the reader on the same message when older history loads above it", async ({
    page,
  }) => {
    const fake = FakeTranscript.withRows(PROGRESSIVE_SESSION, 5500);
    await fake.install(page);
    const olderPage = fake.holdNext(isOlderPage);
    const scroller = await openTranscript(page, `/sessions/${PROGRESSIVE_SESSION}`);

    // Oldest first opens on the first of the newest 1,000 rows; reading on
    // from there asks for the page before it.
    await wheel(page, scroller, 2, 300);
    await olderPage.arrived;
    const before = await settledReadingPosition(scroller);
    expect(before.ordinal).toBeGreaterThanOrEqual(4500);
    const heightBefore = await laidOutHeight(scroller);

    olderPage.release();
    await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 50_000);
    await frames(page, 10);
    await expectSamePosition(scroller, before);
  });

  test("newest first adds older history below the reader without moving them", async ({ page }) => {
    const fake = FakeTranscript.withRows(PROGRESSIVE_SESSION, 5500);
    await fake.install(page);
    const scroller = await openTranscript(page, `/sessions/${PROGRESSIVE_SESSION}`);
    await setNewestFirst(page, scroller);
    const olderPage = fake.holdNext(isOlderPage);

    // The bottom of the newest page is where newest first asks for older rows.
    await scroller.evaluate((el) => {
      el.scrollTop = el.scrollHeight;
      el.dispatchEvent(new Event("scroll"));
    });
    await olderPage.arrived;
    const before = await settledReadingPosition(scroller);
    expect(before.ordinal).toBeLessThan(4600);
    const heightBefore = await laidOutHeight(scroller);

    olderPage.release();
    await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 50_000);
    await frames(page, 10);
    await expectSamePosition(scroller, before);
  });
});

/** Open a link to one message and check that it lands at the top. */
async function expectLinkLandsAtTop(page: Page, sessionId: string, ordinal: number) {
  const scroller = await openTranscript(page, `/sessions/${sessionId}?msg=${ordinal}`);
  await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  await settledReadingPosition(scroller);

  const top = await rowTop(scroller, ordinal);
  expect(top, `row ${ordinal} is rendered`).not.toBeNull();
  expect(top!, `row ${ordinal} distance from the top`).toBeGreaterThanOrEqual(0);
  expect(top!, `row ${ordinal} distance from the top`).toBeLessThanOrEqual(16);
}

test.describe("jumps to a message", () => {
  test("puts a linked message at the top of a fully loaded transcript", async ({ page }) => {
    const fake = FakeTranscript.withRows(OTHER_SESSION, 100);
    await fake.install(page);
    // Whether a jump drifts depends on the row heights around its target, so
    // check two targets.
    for (const ordinal of [60, 30]) await expectLinkLandsAtTop(page, OTHER_SESSION, ordinal);
  });

  test("puts a linked message near the top of the newest page at the top", async ({ page }) => {
    // The jump lands near the top of the loaded rows, so older history loads
    // above it while the target must stay put.
    const fake = FakeTranscript.withRows(PROGRESSIVE_SESSION, 5500);
    await fake.install(page);
    await expectLinkLandsAtTop(page, PROGRESSIVE_SESSION, 4510);
  });
});

test.describe("live updates", () => {
  test("oldest first keeps a reader in the middle in place when messages arrive", async ({
    page,
  }) => {
    const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
    await fake.install(page);
    const watch = await routeSessionWatch(page, LIVE_SESSION);
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    await wheel(page, scroller, 10);
    const before = await settledReadingPosition(scroller);
    const heightBefore = await laidOutHeight(scroller);

    fake.append(3);
    await watch.notifyUpdated();
    await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 100);
    await frames(page, 10);
    await expectSamePosition(scroller, before);
  });

  test("newest first keeps a reader in the middle in place when messages arrive", async ({
    page,
  }) => {
    // New rows enter above the reader at estimated heights and nothing
    // compensates, so the text being read moves down by their height.
    test.fail();
    const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
    await fake.install(page);
    const watch = await routeSessionWatch(page, LIVE_SESSION);
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    await setNewestFirst(page, scroller);
    await wheel(page, scroller, 10);
    const before = await settledReadingPosition(scroller);
    expect(before.ordinal).toBeLessThan(75);
    const heightBefore = await laidOutHeight(scroller);

    fake.append(3);
    await watch.notifyUpdated();
    await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore + 100);
    await frames(page, 10);
    await expectSamePosition(scroller, before);
  });

  test("shows a message that synced before the watch connected", async ({ page }) => {
    // The server's watch reports changes after the moment it subscribes, and
    // the first connection does not refresh the transcript, so a sync that
    // lands between the transcript read and the subscription never shows.
    // A reconnect does refresh, which is why the stream here stays open.
    test.fail();
    const fake = FakeTranscript.withRows(LIVE_SESSION, 80);
    await fake.install(page);
    const watches: Route[] = [];
    await page.route(`**/api/v1/sessions/${LIVE_SESSION}/watch*`, (route) => {
      watches.push(route);
    });
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    await expect.poll(() => watches.length).toBe(1);

    fake.append(1);
    // The real server subscribes now and sees no later change.
    await watches[0]!.continue();
    await scroller.evaluate((el) => {
      el.scrollTop = el.scrollHeight;
      el.dispatchEvent(new Event("scroll"));
    });
    await expect(scroller.getByText("Scroll anchor row 80.")).toBeVisible({ timeout: 10_000 });
  });

  test("keeps a reader past the first thousand rows of a forked session in place", async ({
    page,
  }) => {
    // A fork's reload starts over and publishes its first 1,000 rows before
    // the rest arrive, which clamps the scroll position of a reader below them.
    test.fail();
    const inherited = Array.from({ length: 600 }, (_, i) => transcriptRow(FORK_PARENT, i - 601));
    const boundary = {
      ...transcriptRow(FORK_SESSION, -1),
      role: "system",
      is_system: true,
      source_subtype: "fork_boundary",
      content: FORK_PARENT,
      content_length: FORK_PARENT.length,
    };
    const own = Array.from({ length: 900 }, (_, ordinal) => transcriptRow(FORK_SESSION, ordinal));
    const fake = new FakeTranscript(
      FORK_SESSION,
      [...inherited, boundary, ...own],
      (rows) => rows.filter((row) => row.ordinal >= 0).length,
    );
    await fake.install(page);
    const watch = await routeSessionWatch(page, FORK_SESSION);
    const scroller = await openTranscript(page, `/sessions/${FORK_SESSION}`);

    const follow = page.getByLabel("Follow latest messages");
    await follow.click();
    await expect.poll(() => distanceFromEnd(scroller)).toBeLessThanOrEqual(8);
    await follow.click();
    await expect(follow).toHaveAttribute("aria-pressed", "false");
    await wheel(page, scroller, 3, -400);
    const before = await settledReadingPosition(scroller);
    // Own row 399 is the first one past the first 1,000 rows of the fork view.
    expect(before.ordinal).toBeGreaterThan(399);
    const heightBefore = await laidOutHeight(scroller);
    const servedBefore = fake.served.length;

    fake.append(1);
    await watch.notifyUpdated();
    await expect.poll(() => fake.served.length).toBeGreaterThanOrEqual(servedBefore + 2);
    await expect.poll(() => laidOutHeight(scroller)).toBeGreaterThan(heightBefore);
    await frames(page, 10);
    await expectSamePosition(scroller, before);
  });
});

test.describe("filter and sort changes", () => {
  test("keeps the message being read at the top when a block filter hides others", async ({
    page,
  }) => {
    // The list keeps its scrollTop while the rows above shrink away, so the
    // reader lands far past the message they were reading.
    test.fail();
    const fake = FakeTranscript.withRows(LIVE_SESSION, 200);
    await fake.install(page);
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    await wheel(page, scroller, 40);
    const before = await settledReadingPosition(scroller);
    expect(before.ordinal).not.toBeNull();
    // Even ordinals are user messages; a hidden top row hands over to the next one.
    const expectedTop = before.ordinal! % 2 === 1 ? before.ordinal! : before.ordinal! + 1;

    const filter = page.getByRole("button", { name: "Filter block types" });
    await filter.click();
    await page
      .locator(".block-filter-item")
      .filter({ has: page.locator(".block-filter-label", { hasText: "User messages" }) })
      .click();
    if (await page.locator(".block-filter-dropdown").count()) await filter.click();
    await expect(page.locator(".filter-badge")).toHaveText("1");
    await expect
      .poll(async () => (await visibleOrdinals(scroller)).every((ordinal) => ordinal % 2 === 1))
      .toBe(true);
    await frames(page, 10);

    const after = await settledReadingPosition(scroller);
    expect(after.ordinal, "row at the top of the transcript").toBe(expectedTop);
  });

  test("keeps the message being read in view when the sort order flips and back", async ({
    page,
  }) => {
    // Flipping the order keeps the scroll offset, which now points into the
    // mirrored half of the transcript.
    test.fail();
    const fake = FakeTranscript.withRows(LIVE_SESSION, 200);
    await fake.install(page);
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    await wheel(page, scroller, 90);
    const before = await settledReadingPosition(scroller);
    expect(before.ordinal).not.toBeNull();

    await setNewestFirst(page, scroller);
    await settledReadingPosition(scroller);
    expect(await visibleOrdinals(scroller)).toContain(before.ordinal);

    await page.getByLabel("Toggle sort order").click();
    await expect
      .poll(async () => {
        const shown = await visibleOrdinals(scroller);
        return shown.length > 1 && shown[0]! < shown[1]!;
      })
      .toBe(true);
    await expectSamePosition(scroller, before);
  });
});

test.describe("session switches", () => {
  test("follow latest carries into the next session", async ({ page }) => {
    const first = FakeTranscript.withRows(LIVE_SESSION, 200);
    const second = FakeTranscript.withRows(OTHER_SESSION, 100);
    await first.install(page);
    await second.install(page);
    const scroller = await openTranscript(page, `/sessions/${LIVE_SESSION}`);
    const follow = page.getByLabel("Follow latest messages");
    await follow.click();
    await expect.poll(() => distanceFromEnd(scroller)).toBeLessThanOrEqual(8);

    await page.locator(`.session-item[data-session-id="${OTHER_SESSION}"]`).click();
    await expect(
      page.locator(
        `.message-list-scroll[data-messages-session-id="${OTHER_SESSION}"][data-loaded="true"]`,
      ),
    ).toBeVisible();
    await expect(follow).toHaveAttribute("aria-pressed", "true");
    await expect.poll(() => distanceFromEnd(scroller)).toBeLessThanOrEqual(8);
    await expect.poll(async () => (await visibleOrdinals(scroller)).at(-1)).toBe(99);
  });
});
