// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vite-plus/test";
import { mount, unmount } from "svelte";
import { setLocale } from "../../i18n/index.js";
// @ts-ignore
import ForkBoundaryDivider from "./ForkBoundaryDivider.svelte";

afterEach(() => {
  setLocale("en");
  document.body.innerHTML = "";
});

describe("ForkBoundaryDivider", () => {
  it("renders localized fork boundary copy without translating identifiers", () => {
    setLocale("zh-CN");
    const c = mount(ForkBoundaryDivider, {
      target: document.body,
      props: {
        message: {
          id: -1,
          session_id: "fork-session",
          ordinal: -1,
          role: "system",
          content: "parent-session-id",
          timestamp: "2026-07-01T10:05:00Z",
          has_thinking: false,
          thinking_text: "",
          has_tool_use: false,
          content_length: 17,
          model: "",
          token_usage: null,
          context_tokens: 0,
          output_tokens: 0,
          has_context_tokens: false,
          has_output_tokens: false,
          is_system: true,
          source_subtype: "fork_boundary",
        },
      },
    });

    expect(
      document.body.querySelector(".fork-boundary")?.getAttribute("title"),
    ).toBe("分叉从此处开始");
    expect(document.body.textContent).toContain("分叉开始");
    expect(document.body.textContent).toContain(
      "上方消息是从父会话继承的上下文。",
    );
    expect(document.body.textContent).not.toContain("parent-session-id");

    unmount(c);
  });
});
