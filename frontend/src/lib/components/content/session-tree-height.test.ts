import { describe, expect, it } from "vite-plus/test";
import {
  clampSessionTreeHeightForLayout,
  clampStoredSessionTreeHeight,
} from "./session-tree-height.js";

describe("session tree height helpers", () => {
  it("falls back to the default for missing or non-numeric stored values", () => {
    expect(clampStoredSessionTreeHeight(undefined)).toBe(280);
    expect(clampStoredSessionTreeHeight(null)).toBe(280);
    expect(clampStoredSessionTreeHeight("")).toBe(280);
    expect(clampStoredSessionTreeHeight("tall")).toBe(280);
    expect(clampStoredSessionTreeHeight(Number.NaN)).toBe(280);
    expect(clampStoredSessionTreeHeight(Number.POSITIVE_INFINITY)).toBe(280);
  });

  it("clamps stored values to the supported range and reads stored strings", () => {
    expect(clampStoredSessionTreeHeight("420")).toBe(420);
    expect(clampStoredSessionTreeHeight(20)).toBe(96);
    expect(clampStoredSessionTreeHeight(5000)).toBe(1600);
  });

  it("leaves the vitals reserve below the tree", () => {
    // A 700px column leaves 540px for the tree.
    expect(clampSessionTreeHeightForLayout(900, 700)).toBe(540);
    expect(clampSessionTreeHeightForLayout(300, 700)).toBe(300);
    expect(clampSessionTreeHeightForLayout(40, 700)).toBe(96);
  });

  it("keeps the tree minimum when the column cannot fit the reserve", () => {
    expect(clampSessionTreeHeightForLayout(300, 200)).toBe(96);
  });

  it("caps tall columns at the storage maximum", () => {
    expect(clampSessionTreeHeightForLayout(5000, 3000)).toBe(1600);
  });
});
