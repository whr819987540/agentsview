// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vite-plus/test";
import { mount, unmount } from "svelte";
// @ts-ignore
import SystemBoundaryCard from "./SystemBoundaryCard.svelte";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("SystemBoundaryCard", () => {
  it("renders a human label for a known subtype", () => {
    const c = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "continuation",
        content: "This session is being continued from...",
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    expect(document.body.textContent).toMatch(/Session continuation/);
    unmount(c);
  });

  it("labels a message from another session", () => {
    const c = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "peer_message",
        content: '<cross-session-message from-name="peer">\nhello\n</cross-session-message>',
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    expect(document.body.textContent).toMatch(/Message from another session/);
    unmount(c);
  });

  it("labels the notice a /fork child opens with, and only that notice", () => {
    const fork = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "task_notification",
        content: "<task-notification>\n<fork-source>\nThis session began as a fork.\n</fork-source>\n</task-notification>",
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    expect(document.body.textContent).toMatch(/Fork notice/);
    expect(document.body.textContent).not.toMatch(/Task notification/);
    unmount(fork);

    const task = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "task_notification",
        content: "<task-notification>\n<status>completed</status>\n</task-notification>",
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    expect(document.body.textContent).toMatch(/Task notification/);
    unmount(task);
  });

  it("falls back to the raw subtype when unknown", () => {
    const c = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "future_unknown_subtype",
        content: "anything",
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    expect(document.body.textContent).toMatch(/future_unknown_subtype/);
    unmount(c);
  });

  it("shows the content preview as collapsed details", () => {
    const c = mount(SystemBoundaryCard, {
      target: document.body,
      props: {
        subtype: "stop_hook",
        content: "line one\nline two\nline three",
        timestamp: "2026-04-18T12:00:00Z",
      },
    });
    // <details> contains the full content; hidden until expanded.
    const details = document.body.querySelector("details");
    expect(details).toBeTruthy();
    expect(details?.querySelector("pre")?.textContent).toBe("line one\nline two\nline three");
    unmount(c);
  });
});
