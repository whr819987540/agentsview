// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { flushSync } from "svelte";
import type { DbSessionTiming as SessionTiming } from "../api/generated/index.js";
import { LIVE_TICK_TIMING_WAIT_MS, liveTick } from "./liveTick.svelte.js";
import { sessionTiming } from "./sessionTiming.svelte.js";
import { HIDDEN_WATCH_GRACE_MS } from "./sync.svelte.js";
import { VISIBLE_CATCH_UP_DELAY_MS } from "../utils/page-visibility.js";

const T0 = new Date("2026-03-01T10:00:00Z").getTime();

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

function running(): SessionTiming {
  return { running: true } as unknown as SessionTiming;
}

beforeEach(() => {
  vi.useFakeTimers({ now: T0 });
  sessionTiming.timing = running();
  flushSync();
});

afterEach(() => {
  setVisibility("visible");
  vi.advanceTimersByTime(LIVE_TICK_TIMING_WAIT_MS + VISIBLE_CATCH_UP_DELAY_MS);
  sessionTiming.timing = null;
  flushSync();
  vi.useRealTimers();
});

describe("liveTick after the tab was hidden", () => {
  it("holds running labels after a long hide until fresh timing arrives", () => {
    expect(liveTick.now).toBe(T0);
    setVisibility("hidden");
    vi.advanceTimersByTime(HIDDEN_WATCH_GRACE_MS + 10_000);
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS + 2000);
    expect(liveTick.now).toBe(T0);

    sessionTiming.timing = running();
    flushSync();
    expect(liveTick.now).toBe(Date.now());
  });

  it("stops waiting for fresh timing after a while", () => {
    setVisibility("hidden");
    vi.advanceTimersByTime(HIDDEN_WATCH_GRACE_MS + 10_000);
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS + LIVE_TICK_TIMING_WAIT_MS - 1);
    expect(liveTick.now).toBe(T0);

    vi.advanceTimersByTime(1);
    expect(liveTick.now).toBe(Date.now());
  });

  it("moves at once after a short hide", () => {
    setVisibility("hidden");
    vi.advanceTimersByTime(10_000);
    expect(liveTick.now).toBe(T0);
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(liveTick.now).toBe(Date.now());
  });
});
