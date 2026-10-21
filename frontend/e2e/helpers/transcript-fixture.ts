import { expect, type Locator, type Page, type Route } from "@playwright/test";
import type { DbMessage as Message } from "../../src/lib/api/generated/index.js";
import { waitForStableValue } from "./virtual-list-helpers";

/**
 * A transcript row shaped like real output: one to nine paragraphs, and a
 * code block in every fifth row, so rows land both shorter and much taller
 * than the message list's 120 px estimate. Each row starts with
 * "Scroll anchor row <ordinal>." so a test can tell which row is on screen.
 */
export function transcriptRow(sessionId: string, ordinal: number): Message {
  const size = Math.abs(ordinal);
  const parts = [
    `Scroll anchor row ${ordinal}.`,
    ...Array.from(
      { length: 1 + ((size * 7) % 9) },
      (_, n) =>
        `Paragraph ${n} of row ${ordinal} wraps across the message width so this row is taller or shorter than its neighbours.`,
    ),
  ];
  if (size % 5 === 3) {
    const lines = Array.from({ length: 12 }, (_, n) => `const value${n} = ${size} + ${n};`);
    parts.push(["```ts", ...lines, "```"].join("\n"));
  }
  const content = parts.join("\n\n");
  return {
    id: 700_000 + ordinal,
    session_id: sessionId,
    ordinal,
    role: ordinal % 2 === 0 ? "user" : "assistant",
    content,
    content_length: content.length,
    timestamp: new Date(Date.UTC(2026, 0, 1, 0, 0, size)).toISOString(),
    has_thinking: false,
    thinking_text: "",
    has_tool_use: false,
    model: "",
    context_tokens: 0,
    output_tokens: 0,
    has_context_tokens: false,
    has_output_tokens: false,
    tool_calls: [],
    is_system: false,
  };
}

type RequestMatcher = (url: URL) => boolean;

interface Hold {
  matches: RequestMatcher;
  arrived: () => void;
  released: Promise<void>;
}

/**
 * The messages and metadata endpoints for one session, served from rows the
 * test owns. It pages like the backend: ascending reads start at `from` or the
 * first row, descending reads at `from` or the last row, and every response
 * names the transcript revision. A read bound to an older revision answers 409,
 * as the backend does once a sync moved the transcript.
 */
export class FakeTranscript {
  rows: Message[];
  revision = 1;
  /** Message requests in the order they were answered. */
  readonly served: URL[] = [];
  private holds: Hold[] = [];

  constructor(
    readonly sessionId: string,
    rows: Message[],
    /** The session's own message count, which excludes inherited fork context. */
    private ownCount: (rows: Message[]) => number = (all) => all.length,
  ) {
    this.rows = rows;
  }

  static withRows(sessionId: string, count: number): FakeTranscript {
    return new FakeTranscript(
      sessionId,
      Array.from({ length: count }, (_, ordinal) => transcriptRow(sessionId, ordinal)),
    );
  }

  /** Add rows after the newest one, as a running agent's sync would. */
  append(count: number) {
    const next = (this.rows.at(-1)?.ordinal ?? -1) + 1;
    for (let i = 0; i < count; i++) this.rows.push(transcriptRow(this.sessionId, next + i));
    this.revision++;
  }

  /**
   * Hold the next message request that matches until the returned release is
   * called. `arrived` settles once the request reaches the fake.
   */
  holdNext(matches: RequestMatcher): { arrived: Promise<void>; release: () => void } {
    let arrived!: () => void;
    const arrival = new Promise<void>((resolve) => (arrived = resolve));
    let release!: () => void;
    const released = new Promise<void>((resolve) => (release = resolve));
    this.holds.push({ matches, arrived, released });
    return { arrived: arrival, release };
  }

  async install(page: Page) {
    await page.route(`**/api/v1/sessions/${this.sessionId}`, async (route) => {
      const response = await route.fetch();
      const session = await response.json();
      await route.fulfill({
        response,
        json: {
          ...session,
          message_count: this.ownCount(this.rows),
          transcript_revision: String(this.revision),
        },
      });
    });
    await page.route(`**/api/v1/sessions/${this.sessionId}/messages*`, (route) =>
      this.answer(route),
    );
  }

  private async answer(route: Route) {
    const url = new URL(route.request().url());
    const hold = this.holds.find((h) => h.matches(url));
    if (hold) {
      this.holds = this.holds.filter((h) => h !== hold);
      hold.arrived();
      await hold.released;
    }
    const expected = url.searchParams.get("expected_revision");
    if (expected !== null && expected !== String(this.revision)) {
      this.served.push(url);
      await route.fulfill({ status: 409, json: { title: "Conflict", status: 409 } });
      return;
    }
    const desc = url.searchParams.get("direction") === "desc";
    const limit = Number(url.searchParams.get("limit") ?? 1000);
    const fromParam = url.searchParams.get("from");
    let page: Message[];
    if (desc) {
      const from = fromParam === null ? Infinity : Number(fromParam);
      page = this.rows
        .filter((row) => row.ordinal <= from)
        .reverse()
        .slice(0, limit);
    } else {
      const from = fromParam === null ? -Infinity : Number(fromParam);
      page = this.rows.filter((row) => row.ordinal >= from).slice(0, limit);
    }
    this.served.push(url);
    await route.fulfill({
      json: { messages: page, count: page.length, transcript_revision: String(this.revision) },
    });
  }
}

/**
 * Hold the session's watch streams so the test decides when the server
 * reports a change, the way `session-find-live-updates.spec.ts` does.
 */
export async function routeSessionWatch(page: Page, sessionId: string) {
  const streams: Route[] = [];
  await page.route(`**/api/v1/sessions/${sessionId}/watch*`, (route) => {
    streams.push(route);
  });
  return {
    /** Tell the open watch that the session changed. */
    async notifyUpdated() {
      await expect.poll(() => streams.length).toBeGreaterThan(0);
      await streams.shift()!.fulfill({
        status: 200,
        headers: { "content-type": "text/event-stream" },
        body: "retry: 50\nevent: session_updated\ndata: {}\n\n",
      });
    },
  };
}

export function transcript(page: Page): Locator {
  return page.locator(".message-list-scroll");
}

export interface ReadingPosition {
  /** Ordinal of the first row visible at the viewport top. */
  ordinal: number | null;
  /** Pixels the viewport top sits below that row's top. */
  offset: number;
}

export function readingPosition(scroller: Locator): Promise<ReadingPosition> {
  return scroller.evaluate((el) => {
    const top = el.getBoundingClientRect().top;
    const first = [...el.querySelectorAll(".virtual-row")]
      .map((row) => ({ row, rect: row.getBoundingClientRect() }))
      .filter(({ rect }) => rect.bottom > top + 0.5)
      .sort((a, b) => a.rect.top - b.rect.top)[0];
    const match = first?.row.textContent?.match(/Scroll anchor row (-?\d+)\./);
    return {
      ordinal: match ? Number(match[1]) : null,
      offset: first ? Math.round(top - first.rect.top) : 0,
    };
  });
}

/** Wait until row measurements and scroll corrections stop moving the
 *  viewport, then report where it ended up. */
export function settledReadingPosition(scroller: Locator): Promise<ReadingPosition> {
  return waitForStableValue(
    () => readingPosition(scroller),
    800,
    100,
    10_000,
    (a, b) => a.ordinal === b.ordinal && a.offset === b.offset,
  );
}

/** Ordinals of the rows at least partly inside the viewport, top to bottom. */
export function visibleOrdinals(scroller: Locator): Promise<number[]> {
  return scroller.evaluate((el) => {
    const view = el.getBoundingClientRect();
    return [...el.querySelectorAll(".virtual-row")]
      .map((row) => ({ row, rect: row.getBoundingClientRect() }))
      .filter(({ rect }) => rect.bottom > view.top + 0.5 && rect.top < view.bottom - 0.5)
      .sort((a, b) => a.rect.top - b.rect.top)
      .map(({ row }) => row.textContent?.match(/Scroll anchor row (-?\d+)\./)?.[1])
      .filter((ordinal): ordinal is string => ordinal !== undefined)
      .map(Number);
  });
}

/** Height of every row the list has laid out, measured or estimated. */
export function laidOutHeight(scroller: Locator): Promise<number> {
  return scroller.evaluate((el) => el.scrollHeight);
}

/** Scroll the transcript the way a reader does, with the mouse wheel. */
export async function wheel(page: Page, scroller: Locator, steps: number, deltaY = 400) {
  const box = await scroller.boundingBox();
  if (!box) throw new Error("transcript is not visible");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  for (let i = 0; i < steps; i++) await page.mouse.wheel(0, deltaY);
}

type HeldFrames = {
  callbacks: Map<number, FrameRequestCallback>;
  request: typeof requestAnimationFrame;
  cancel: typeof cancelAnimationFrame;
};

/** Queue animation frame callbacks instead of running them, so work that
 *  waits for a frame cannot go on until releaseAnimationFrames. */
export async function holdAnimationFrames(page: Page) {
  await page.evaluate(() => {
    const win = window as Window & { __heldFrames?: HeldFrames };
    const held: HeldFrames = {
      callbacks: new Map(),
      request: window.requestAnimationFrame.bind(window),
      cancel: window.cancelAnimationFrame.bind(window),
    };
    let next = 1;
    win.__heldFrames = held;
    window.requestAnimationFrame = (callback) => {
      const id = next++;
      held.callbacks.set(id, callback);
      return id;
    };
    window.cancelAnimationFrame = (id) => {
      held.callbacks.delete(id);
    };
  });
}

/** Restore animation frames and run the callbacks held since holdAnimationFrames. */
export async function releaseAnimationFrames(page: Page) {
  await page.evaluate(() => {
    const win = window as Window & { __heldFrames?: HeldFrames };
    const held = win.__heldFrames;
    if (!held) return;
    delete win.__heldFrames;
    window.requestAnimationFrame = held.request;
    window.cancelAnimationFrame = held.cancel;
    for (const callback of held.callbacks.values()) callback(performance.now());
  });
}

/** Let late scroll work from an update land before checking it did not. */
export async function frames(page: Page, count: number) {
  await page.evaluate(async (n) => {
    for (let i = 0; i < n; i++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  }, count);
}

export async function openTranscript(page: Page, path: string) {
  await page.goto(path);
  const scroller = transcript(page);
  await expect(scroller).toHaveAttribute("data-loaded", "true");
  await expect(scroller.locator(".virtual-row").first()).toBeVisible();
  return scroller;
}
