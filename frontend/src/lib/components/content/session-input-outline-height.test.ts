import { describe, expect, it } from "vite-plus/test";
import {
  clampSessionInputOutlineHeightForLayout,
  clampStoredSessionInputOutlineHeight,
} from "./session-input-outline-height.js";

describe("session input outline height helpers", () => {
  it("falls back to the default for missing or non-numeric stored values", () => {
    expect(clampStoredSessionInputOutlineHeight(undefined)).toBe(240);
    expect(clampStoredSessionInputOutlineHeight(null)).toBe(240);
    expect(clampStoredSessionInputOutlineHeight("")).toBe(240);
    expect(clampStoredSessionInputOutlineHeight("tall")).toBe(240);
    expect(clampStoredSessionInputOutlineHeight(Number.NaN)).toBe(240);
    expect(clampStoredSessionInputOutlineHeight(Number.POSITIVE_INFINITY)).toBe(240);
  });

  it("clamps stored values to the supported range and reads stored strings", () => {
    expect(clampStoredSessionInputOutlineHeight("420")).toBe(420);
    expect(clampStoredSessionInputOutlineHeight(20)).toBe(96);
    expect(clampStoredSessionInputOutlineHeight(5000)).toBe(1600);
  });

  it("leaves the panel reserve below the outline", () => {
    // A 700px panel leaves 540px for the outline.
    expect(clampSessionInputOutlineHeightForLayout(900, 700)).toBe(540);
    expect(clampSessionInputOutlineHeightForLayout(300, 700)).toBe(300);
    expect(clampSessionInputOutlineHeightForLayout(40, 700)).toBe(96);
  });

  it("keeps the outline minimum when the panel cannot fit the reserve", () => {
    expect(clampSessionInputOutlineHeightForLayout(300, 200)).toBe(96);
  });

  it("caps tall panels at the storage maximum", () => {
    expect(clampSessionInputOutlineHeightForLayout(5000, 3000)).toBe(1600);
  });
});
