// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vite-plus/test";
import { mount, tick, unmount } from "svelte";
import { ui } from "../../stores/ui.svelte.js";
import ThinkingBlock from "./ThinkingBlock.svelte";

describe("ThinkingBlock bulk collapse", () => {
  let component: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    ui.showAllBlocks();
    ui.bulkCollapseCommand = null;
  });

  afterEach(() => {
    if (component) {
      unmount(component);
      component = undefined;
    }
    document.body.innerHTML = "";
    ui.showAllBlocks();
    ui.bulkCollapseCommand = null;
  });

  it("applies bulk expand and collapse as user overrides", async () => {
    ui.expandVisibleBlocks();
    component = mount(ThinkingBlock, {
      target: document.body,
      props: {
        content: "Internal reasoning",
      },
    });
    await tick();

    expect(document.querySelector(".thinking-content")?.textContent).toBe(
      "Internal reasoning",
    );

    ui.collapseVisibleBlocks();
    await tick();

    expect(document.querySelector(".thinking-content")).toBeNull();
  });
});
