import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { deferWhileHidden, isPageHidden, onPageHidden, onPageVisible } from "./page-visibility.js";

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

afterEach(() => setVisibility("visible"));

describe("page visibility", () => {
  it("reports the tab's visibility", () => {
    expect(isPageHidden()).toBe(false);
    setVisibility("hidden");
    expect(isPageHidden()).toBe(true);
  });

  it("notifies on each transition until unsubscribed", () => {
    const shown = vi.fn();
    const hidden = vi.fn();
    const offShown = onPageVisible(shown);
    const offHidden = onPageHidden(hidden);

    setVisibility("hidden");
    setVisibility("visible");
    expect(hidden).toHaveBeenCalledTimes(1);
    expect(shown).toHaveBeenCalledTimes(1);

    offShown();
    offHidden();
    setVisibility("hidden");
    setVisibility("visible");
    expect(hidden).toHaveBeenCalledTimes(1);
    expect(shown).toHaveBeenCalledTimes(1);
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
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("b");

    setVisibility("hidden");
    setVisibility("visible");
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
    expect(fn).not.toHaveBeenCalled();
  });
});
