import { describe, expect, it } from "vitest";
import {
  ScrollMemory,
  findAnchorIndexAsc,
  findFirstVisibleVirtualItem,
} from "./scroll-memory.js";

describe("ScrollMemory", () => {
  it("returns the remembered anchor for a session", () => {
    const memory = new ScrollMemory();
    memory.remember("a", { ordinal: 12, offsetPx: 40 });

    expect(memory.get("a")).toEqual({
      ordinal: 12,
      offsetPx: 40,
    });
    expect(memory.get("b")).toBeNull();
  });

  it("overwrites the anchor on repeated remembers", () => {
    const memory = new ScrollMemory();
    memory.remember("a", { ordinal: 1, offsetPx: 0 });
    memory.remember("a", { ordinal: 7, offsetPx: 15 });

    expect(memory.get("a")).toEqual({
      ordinal: 7,
      offsetPx: 15,
    });
  });

  it("forgets and clears entries", () => {
    const memory = new ScrollMemory();
    memory.remember("a", { ordinal: 1, offsetPx: 0 });
    memory.remember("b", { ordinal: 2, offsetPx: 0 });

    memory.forget("a");
    expect(memory.get("a")).toBeNull();
    expect(memory.get("b")).not.toBeNull();

    memory.clear();
    expect(memory.get("b")).toBeNull();
  });

  it("evicts the least recently updated entry beyond the cap", () => {
    const memory = new ScrollMemory();
    for (let i = 0; i < 100; i++) {
      memory.remember(`s${i}`, { ordinal: i, offsetPx: 0 });
    }
    // Refresh s0 so s1 becomes the eviction candidate.
    memory.remember("s0", { ordinal: 0, offsetPx: 5 });
    memory.remember("s100", { ordinal: 100, offsetPx: 0 });

    expect(memory.get("s1")).toBeNull();
    expect(memory.get("s0")).toEqual({
      ordinal: 0,
      offsetPx: 5,
    });
    expect(memory.get("s100")).not.toBeNull();
  });
});

describe("findFirstVisibleVirtualItem", () => {
  const items = [
    { index: 0, start: 0, end: 120 },
    { index: 1, start: 120, end: 240 },
    { index: 2, start: 240, end: 360 },
  ];

  it("skips overscanned items fully above the viewport", () => {
    expect(findFirstVisibleVirtualItem(items, 130)).toEqual({
      index: 1,
      start: 120,
      end: 240,
    });
  });

  it("returns the first item at the top of the list", () => {
    expect(findFirstVisibleVirtualItem(items, 0)).toEqual({
      index: 0,
      start: 0,
      end: 120,
    });
  });

  it("returns null when nothing is below the viewport top", () => {
    expect(findFirstVisibleVirtualItem(items, 400)).toBeNull();
    expect(findFirstVisibleVirtualItem([], 0)).toBeNull();
  });
});

describe("findAnchorIndexAsc", () => {
  const items = [
    { ordinals: [0] },
    { ordinals: [1, 2, 3] },
    { ordinals: [5] },
  ];

  it("finds the item containing the ordinal", () => {
    expect(findAnchorIndexAsc(items, 0)).toBe(0);
    expect(findAnchorIndexAsc(items, 2)).toBe(1);
    expect(findAnchorIndexAsc(items, 5)).toBe(2);
  });

  it("falls back to the nearest following item", () => {
    expect(findAnchorIndexAsc(items, 4)).toBe(2);
  });

  it("clamps past-the-end anchors to the last item", () => {
    expect(findAnchorIndexAsc(items, 9)).toBe(2);
  });

  it("returns -1 for an empty list", () => {
    expect(findAnchorIndexAsc([], 3)).toBe(-1);
  });
});
