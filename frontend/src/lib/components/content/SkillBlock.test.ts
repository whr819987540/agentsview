// @vitest-environment jsdom
import { mount, unmount, tick } from "svelte";
import { afterEach, describe, expect, it } from "vite-plus/test";
import { setLocale } from "../../i18n/index.js";

// @ts-ignore
import SkillBlock from "./SkillBlock.svelte";

describe("SkillBlock", () => {
  let component: ReturnType<typeof mount> | undefined;

  afterEach(() => {
    if (component) unmount(component);
    component = undefined;
    document.body.innerHTML = "";
    setLocale("en");
  });

  it("localizes the label fallback", async () => {
    setLocale("zh-CN");

    component = mount(SkillBlock, {
      target: document.body,
      props: { content: "Use the project guidance." },
    });
    await tick();

    expect(document.querySelector(".skill-label")?.textContent).toBe("技能：未知");
  });

  it("marks a current search match in the skill name", async () => {
    component = mount(SkillBlock, {
      target: document.body,
      props: {
        content: "Use the project guidance.",
        name: "deep-research",
        highlightQuery: "deep-research",
        isCurrentHighlight: true,
      },
    });
    await tick();

    const mark = document.querySelector(
      ".skill-label mark.search-highlight--current",
    ) as HTMLElement | null;
    expect(mark?.textContent).toBe("deep-research");
  });

  it("expands and marks a current search match", async () => {
    component = mount(SkillBlock, {
      target: document.body,
      props: {
        content: "Use the project guidance.",
        highlightQuery: "project",
        isCurrentHighlight: true,
      },
    });
    await tick();

    const mark = document.querySelector(
      "mark.search-highlight--current",
    ) as HTMLElement | null;
    expect(document.querySelector(".skill-content")).not.toBeNull();
    expect(mark?.textContent).toBe("project");
  });
});
