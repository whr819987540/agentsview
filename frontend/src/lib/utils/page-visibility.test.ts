import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import {
  deferWhileHidden,
  isPageHidden,
  onPageHidden,
  onPageVisible,
  VISIBLE_CATCH_UP_DELAY_MS,
} from "./page-visibility.js";

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  setVisibility("visible");
  vi.runOnlyPendingTimers();
  vi.useRealTimers();
});

describe("page visibility", () => {
  it("reports the tab's visibility", () => {
    expect(isPageHidden()).toBe(false);
    setVisibility("hidden");
    expect(isPageHidden()).toBe(true);
  });

  it("tells hidden listeners at once and visible listeners after a short delay", () => {
    const shown = vi.fn();
    const hidden = vi.fn();
    const offShown = onPageVisible(shown);
    const offHidden = onPageHidden(hidden);

    setVisibility("hidden");
    expect(hidden).toHaveBeenCalledTimes(1);
    setVisibility("visible");
    expect(shown).not.toHaveBeenCalled();
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(shown).toHaveBeenCalledTimes(1);

    offShown();
    offHidden();
    setVisibility("hidden");
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(hidden).toHaveBeenCalledTimes(1);
    expect(shown).toHaveBeenCalledTimes(1);
  });

  it("skips the catch-up when the tab is hidden again before it starts", () => {
    const shown = vi.fn();
    const off = onPageVisible(shown);
    setVisibility("hidden");
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS - 1);
    setVisibility("hidden");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(shown).not.toHaveBeenCalled();
    off();
  });

  it("runs a deferred call at once while visible", () => {
    const fn = vi.fn();
    const deferred = deferWhileHidden(fn);
    deferred.run("a");
    expect(fn).toHaveBeenCalledWith("a");
    deferred.dispose();
  });

  it("collapses calls made while hidden into one with the latest arguments", () => {
    const fn = vi.fn();
    const deferred = deferWhileHidden(fn);
    setVisibility("hidden");
    deferred.run("a");
    deferred.run("b");
    expect(fn).not.toHaveBeenCalled();

    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("b");

    setVisibility("hidden");
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(fn).toHaveBeenCalledTimes(1);
    deferred.dispose();
  });

  it("drops a held call when disposed", () => {
    const fn = vi.fn();
    const deferred = deferWhileHidden(fn);
    setVisibility("hidden");
    deferred.run();
    deferred.dispose();
    setVisibility("visible");
    vi.advanceTimersByTime(VISIBLE_CATCH_UP_DELAY_MS);
    expect(fn).not.toHaveBeenCalled();
  });
});
