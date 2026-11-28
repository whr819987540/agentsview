// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vite-plus/test";
import { mount, unmount } from "svelte";
// @ts-ignore
import TreeGuides from "./TreeGuides.svelte";

let component: ReturnType<typeof mount> | undefined;

function render(props: { guides: boolean[]; offset?: number; reach: number }) {
  component = mount(TreeGuides, { target: document.body, props });
  return [...document.querySelectorAll<HTMLElement>(".tree-guide")].map((el) => [
    el.className.replace(/\s*svelte-\S+/g, "").trim(),
    el.style.left,
    el.style.width,
  ]);
}

describe("TreeGuides", () => {
  afterEach(() => {
    if (component) void unmount(component);
    component = undefined;
    document.body.innerHTML = "";
  });

  it("runs an ancestor's line past the row and joins a row with a sibling below", () => {
    // Depth 3: level 1 continues, level 2 has ended, and a sibling follows.
    expect(render({ guides: [true, false, true], reach: 18 })).toEqual([
      ["tree-guide rail", "16px", ""],
      ["tree-guide rail", "48px", ""],
      ["tree-guide elbow", "48px", "26px"],
    ]);
  });

  it("ends the line at the last row of its level", () => {
    expect(render({ guides: [false, false], reach: 3 })).toEqual([
      ["tree-guide rail end", "32px", ""],
      ["tree-guide elbow", "32px", "11px"],
    ]);
  });

  it("moves the lines with a folder's indent", () => {
    expect(render({ guides: [true], offset: 28, reach: 18 })).toEqual([
      ["tree-guide rail", "44px", ""],
      ["tree-guide elbow", "44px", "26px"],
    ]);
  });
});
