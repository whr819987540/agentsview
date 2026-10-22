import { expect, test, type Page, type Route } from "@playwright/test";

// The sidebar reloads its index when a sync reports new data. These tests
// serve the index themselves, change a session the way a new message would,
// and send the data_changed event that triggers the reload.

interface IndexRow {
  id: string;
  parent_session_id: null;
  relationship_type: null;
  project: string;
  machine: string;
  agent: string;
  display_name: string;
  started_at: string;
  ended_at: string;
  created_at: string;
  message_count: number;
  user_message_count: number;
  termination_status: string | null;
  is_automated: boolean;
  is_teammate: boolean;
}

const MINUTE = 60_000;

function isoAgo(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

function indexRow(id: string, startedAgo: number, endedAgo: number): IndexRow {
  return {
    id,
    parent_session_id: null,
    relationship_type: null,
    project: "live-project",
    machine: "local",
    agent: "claude",
    display_name: `Session ${id}`,
    started_at: isoAgo(startedAgo),
    ended_at: isoAgo(endedAgo),
    created_at: isoAgo(startedAgo),
    message_count: 12,
    user_message_count: 4,
    termination_status: null,
    is_automated: false,
    is_teammate: false,
  };
}

/** Quiet sessions below the ones a test updates. */
function quietRows(count: number): IndexRow[] {
  return Array.from({ length: count }, (_, i) =>
    indexRow(`quiet-${i}`, (i + 3) * 60 * MINUTE, (i + 2) * 60 * MINUTE),
  );
}

async function openSidebar(page: Page, rows: IndexRow[]) {
  const events: Route[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.endsWith("/events")) {
      events.push(route);
      return;
    }
    if (pathname.endsWith("/sessions/sidebar-index")) {
      // Newest activity first, as the server orders the index.
      const sessions = [...rows].sort((a, b) => b.ended_at.localeCompare(a.ended_at));
      await route.fulfill({ json: { sessions, next_cursor: null, total: sessions.length } });
      return;
    }
    const detail = pathname.match(/\/sessions\/([^/]+)$/);
    const session = detail && rows.find((row) => row.id === decodeURIComponent(detail[1]!));
    if (session) {
      await route.fulfill({ json: { ...session, first_message: session.display_name } });
      return;
    }
    if (pathname.endsWith("/version")) {
      await route.fulfill({ json: { version: "test", commit: "test", read_only: false } });
      return;
    }
    if (pathname.endsWith("/sync/status")) {
      await route.fulfill({ json: { last_sync: null, stats: null } });
      return;
    }
    await route.fulfill({ json: {} });
  });
  await page.goto("/");
  await expect(page.locator(".session-item")).toHaveCount(rows.length);

  /** Report new data the way the server does after a sync. */
  async function sendDataChanged() {
    await expect.poll(() => events.length).toBeGreaterThan(0);
    // Earlier connections may have been replaced; the newest is live.
    await events.at(-1)!.fulfill({
      status: 200,
      headers: { "content-type": "text/event-stream" },
      body: 'retry: 600000\nevent: data_changed\ndata: {"scope":"sessions"}\n\n',
    });
  }
  return { sendDataChanged };
}

/** A session gains a message: it is newer and the agent is working again. */
function addMessage(row: IndexRow) {
  row.ended_at = new Date().toISOString();
  row.message_count += 1;
  row.user_message_count += 1;
  row.termination_status = null;
}

function sessionRow(page: Page, id: string) {
  return page.locator(`.session-item[data-session-id="${id}"]`);
}

async function rowLayout(page: Page) {
  return page.locator(".session-list-scroll").evaluate((scroll) =>
    [...scroll.querySelectorAll<HTMLElement>(".session-item")]
      .map((row) => ({
        id: row.dataset.sessionId,
        top: Math.round(row.getBoundingClientRect().top),
        titleLeft: Math.round(row.querySelector(".session-name")!.getBoundingClientRect().left),
      }))
      .sort((a, b) => a.top - b.top),
  );
}

async function frames(page: Page, count: number) {
  await page.evaluate(async (n) => {
    for (let i = 0; i < n; i++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  }, count);
}

test.describe("sidebar live updates", () => {
  test("keeps working sessions in place when one of them gets a message", async ({ page }) => {
    // Both sessions are working. The one that started later leads.
    const startedLater = indexRow("started-later", 60 * MINUTE, 5_000);
    const startedEarlier = indexRow("started-earlier", 2 * 60 * MINUTE, 20_000);
    const { sendDataChanged } = await openSidebar(page, [
      startedLater,
      startedEarlier,
      ...quietRows(6),
    ]);
    const before = await rowLayout(page);
    expect(before.slice(0, 2).map((row) => row.id)).toEqual(["started-later", "started-earlier"]);

    addMessage(startedEarlier);
    await sendDataChanged();
    await expect(sessionRow(page, "started-earlier").locator(".session-count")).toHaveText(
      String(startedEarlier.user_message_count),
    );
    await frames(page, 5);

    expect(await rowLayout(page)).toEqual(before);
  });
});
