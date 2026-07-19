// @vitest-environment jsdom
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { mount, tick, unmount } from "svelte";
import type { Message } from "../../api/types.js";
import { messages } from "../../stores/messages.svelte.js";
import { sessions } from "../../stores/sessions.svelte.js";
import { ui } from "../../stores/ui.svelte.js";
import { setLocale } from "../../i18n/index.js";
import { scrollMemory } from "./scroll-memory.js";

interface VirtualRow {
  key: string;
  index: number;
  start: number;
  end: number;
}

const searchMock = vi.hoisted(() => ({
  isOpen: false,
  query: "",
  matches: [] as Array<{ ordinal: number; sessionId: string }>,
  currentMatchIndex: -1,
  loading: false,
  currentOrdinal: null as number | null,
  close: vi.fn(),
  next: vi.fn(),
  prev: vi.fn(),
}));

vi.mock("../../stores/inSessionSearch.svelte.js", () => ({
  inSessionSearch: searchMock,
}));

const inSessionSearch = searchMock;

const virtualizerMock = vi.hoisted(() => ({
  options: { count: 0 },
  scrollOffset: 0,
  getVirtualItems: vi.fn((): VirtualRow[] => []),
  getTotalSize: vi.fn(() => 120),
  measureElement: vi.fn(),
  scrollToIndex: vi.fn(),
  scrollToOffset: vi.fn(),
  getOffsetForIndex: vi.fn(),
}));

vi.mock("../../virtual/createVirtualizer.svelte.js", () => ({
  createVirtualizer: (
    optsFn: () => { count: number },
  ) => ({
    get instance() {
      virtualizerMock.options.count = optsFn().count;
      return virtualizerMock;
    },
  }),
}));

// @ts-ignore
import MessageList from "./MessageList.svelte";

function makeMessage(ordinal: number): Message {
  return {
    id: ordinal + 1,
    session_id: "s1",
    ordinal,
    role: ordinal % 2 === 0 ? "user" : "assistant",
    content: `msg ${ordinal}`,
    timestamp: new Date(ordinal * 1000).toISOString(),
    has_thinking: false,
    thinking_text: "",
    has_tool_use: false,
    content_length: 6,
    model: "",
    token_usage: null,
    context_tokens: 0,
    output_tokens: 0,
    has_context_tokens: false,
    has_output_tokens: false,
    is_system: false,
  };
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe("MessageList follow cancellation", () => {
  let component: ReturnType<typeof mount> | undefined;
  let rafSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.clearAllMocks();
    virtualizerMock.getVirtualItems.mockImplementation(() => []);
    virtualizerMock.getOffsetForIndex.mockImplementation(
      () => undefined,
    );
    messages.clear();
    sessions.activeSessionId = "s1";
    messages.sessionId = "s1";
    messages.messages = [makeMessage(10)];
    messages.messageCount = 11;
    messages.hasOlder = true;
    ui.followLatest = true;
    ui.followLatestRequest = 1;
    ui.sortNewestFirst = false;
    ui.selectedOrdinal = null;
    ui.pendingScrollOrdinal = null;
    ui.pendingScrollSession = null;
    rafSpy = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((cb: FrameRequestCallback) => {
        window.setTimeout(() => cb(performance.now()), 0);
        return 1;
      });
  });

  afterEach(() => {
    setLocale("en");
    if (component) {
      unmount(component);
      component = undefined;
    }
    rafSpy.mockRestore();
    messages.clear();
    sessions.activeSessionId = null;
    ui.followLatest = false;
    inSessionSearch.isOpen = false;
    inSessionSearch.query = "";
    inSessionSearch.matches = [];
    inSessionSearch.currentMatchIndex = -1;
    inSessionSearch.currentOrdinal = null;
    ui.showAllBlocks();
    ui.bulkCollapseCommand = null;
    document.body.innerHTML = "";
  });

  it("renders empty and loading states in Simplified Chinese", async () => {
    setLocale("zh-CN");
    sessions.activeSessionId = null;
    messages.clear();

    component = mount(MessageList, { target: document.body });
    await tick();

    expect(document.body.textContent).toContain("选择一个会话查看消息");

    unmount(component);
    component = undefined;
    document.body.innerHTML = "";

    sessions.activeSessionId = "s1";
    messages.sessionId = "s1";
    messages.messages = [];
    messages.loading = true;

    component = mount(MessageList, { target: document.body });
    await tick();

    expect(document.body.textContent).toContain("正在加载消息...");
  });

  it("centers the exact current search mark in a long message", async () => {
    const message = makeMessage(10);
    message.content = "before needle after";
    message.content_length = message.content.length;
    messages.messages = [message];
    messages.hasOlder = false;
    ui.followLatest = false;
    ui.followLatestRequest = 0;
    virtualizerMock.getVirtualItems.mockImplementation(() => [
      { key: "k10", index: 0, start: 0, end: 1200 },
    ]);
    virtualizerMock.getOffsetForIndex.mockImplementation(() => [
      0,
      "start",
    ]);
    inSessionSearch.isOpen = true;
    inSessionSearch.query = "needle";
    inSessionSearch.matches = [{ ordinal: 10, sessionId: "s1" }];
    inSessionSearch.currentMatchIndex = 0;
    inSessionSearch.currentOrdinal = 10;

    component = mount(MessageList, { target: document.body });
    await tick();

    const container = document.querySelector(
      ".message-list-scroll",
    ) as HTMLElement;
    const rectSpy = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLElement) {
        const top = this.classList.contains(
          "search-highlight--current",
        )
          ? 700
          : this === container ? 100 : 0;
        const height = this.classList.contains(
          "search-highlight--current",
        )
          ? 20
          : this === container ? 400 : 0;
        return {
          top,
          bottom: top + height,
          left: 0,
          right: 800,
          width: 800,
          height,
          x: 0,
          y: top,
          toJSON: () => ({}),
        };
      });
    const scope = document.querySelector(
      '[data-message-ordinals="10"]',
    ) as HTMLElement;
    const mark = document.createElement("mark");
    mark.className =
      "search-highlight search-highlight--current";
    scope.appendChild(mark);
    Object.defineProperties(container, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });
    await new Promise((resolve) => setTimeout(resolve, 20));
    virtualizerMock.scrollToOffset.mockClear();

    (
      component as ReturnType<typeof mount> & {
        scrollToOrdinal: (
          ordinal: number,
          searchQuery?: string,
        ) => void;
      }
    ).scrollToOrdinal(10, "needle");

    await vi.waitFor(() => {
      expect(virtualizerMock.scrollToOffset).toHaveBeenCalledWith(
        410,
        { align: "start" },
      );
    });
    rectSpy.mockRestore();
  });

  it("centers a match within a nested scrollable block", async () => {
    messages.hasOlder = false;
    virtualizerMock.getVirtualItems.mockImplementation(() => [
      { key: "k10", index: 0, start: 0, end: 1200 },
    ]);
    virtualizerMock.getOffsetForIndex.mockImplementation(() => [
      0,
      "start",
    ]);
    inSessionSearch.isOpen = true;
    inSessionSearch.query = "needle";
    inSessionSearch.matches = [{ ordinal: 10, sessionId: "s1" }];
    inSessionSearch.currentMatchIndex = 0;
    inSessionSearch.currentOrdinal = 10;

    component = mount(MessageList, { target: document.body });
    await tick();

    const container = document.querySelector(
      ".message-list-scroll",
    ) as HTMLElement;
    const scope = document.querySelector(
      '[data-message-ordinals="10"]',
    ) as HTMLElement;
    const nestedScroller = document.createElement("div");
    nestedScroller.style.overflowY = "auto";
    const mark = document.createElement("mark");
    mark.className =
      "search-highlight search-highlight--current";
    nestedScroller.appendChild(mark);
    scope.appendChild(nestedScroller);
    Object.defineProperties(container, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });
    Object.defineProperties(nestedScroller, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 900 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });
    const rectSpy = vi.spyOn(
      HTMLElement.prototype,
      "getBoundingClientRect",
    ).mockImplementation(function (this: HTMLElement) {
      const top = this === mark
        ? 600
        : this === nestedScroller ? 200
        : this === container ? 100 : 0;
      const height = this === mark
        ? 20
        : this === nestedScroller ? 300
        : this === container ? 400 : 0;
      return {
        top,
        bottom: top + height,
        left: 0,
        right: 800,
        width: 800,
        height,
        x: 0,
        y: top,
        toJSON: () => ({}),
      };
    });
    await new Promise((resolve) => setTimeout(resolve, 20));

    (
      component as ReturnType<typeof mount> & {
        scrollToOrdinal: (
          ordinal: number,
          searchQuery?: string,
        ) => void;
      }
    ).scrollToOrdinal(10, "needle");

    await vi.waitFor(() => {
      expect(nestedScroller.scrollTop).toBe(260);
    });
    rectSpy.mockRestore();
  });

  it("keeps delayed ordinal navigation alive after follow latest is disabled", async () => {
    const loaded = deferred<void>();
    const ensureSpy = vi
      .spyOn(messages, "ensureOrdinalLoaded")
      .mockImplementation(async () => {
        await loaded.promise;
        messages.messages = [makeMessage(0), makeMessage(10)];
      });

    component = mount(MessageList, { target: document.body });
    await tick();

    ui.setFollowLatest(false);
    (
      component as ReturnType<typeof mount> & {
        scrollToOrdinal: (ordinal: number) => void;
      }
    ).scrollToOrdinal(0);
    await tick();

    loaded.resolve();
    await tick();
    await vi.waitFor(() => {
      expect(virtualizerMock.scrollToIndex).toHaveBeenCalled();
    });

    expect(ensureSpy).toHaveBeenCalledWith(0);
    expect(virtualizerMock.scrollToIndex).toHaveBeenCalledWith(0, {
      align: "start",
    });
  });
});

describe("MessageList scroll position memory", () => {
  let component: ReturnType<typeof mount> | undefined;
  let rafSpy: ReturnType<typeof vi.spyOn>;

  const virtualRows = [
    { key: "k0", index: 0, start: 0, end: 120 },
    { key: "k1", index: 1, start: 120, end: 240 },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    virtualizerMock.getVirtualItems.mockImplementation(() => []);
    virtualizerMock.getOffsetForIndex.mockImplementation(
      () => undefined,
    );
    virtualizerMock.scrollOffset = 0;
    scrollMemory.clear();
    messages.clear();
    sessions.activeSessionId = "s1";
    messages.sessionId = "s1";
    messages.messages = [makeMessage(0), makeMessage(1)];
    messages.messageCount = 2;
    messages.hasOlder = false;
    messages.loading = false;
    ui.followLatest = false;
    ui.followLatestRequest = 0;
    ui.sortNewestFirst = false;
    ui.selectedOrdinal = null;
    ui.pendingScrollOrdinal = null;
    ui.pendingScrollSession = null;
    rafSpy = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((cb: FrameRequestCallback) => {
        window.setTimeout(() => cb(performance.now()), 0);
        return 1;
      });
  });

  afterEach(() => {
    if (component) {
      unmount(component);
      component = undefined;
    }
    rafSpy.mockRestore();
    scrollMemory.clear();
    messages.clear();
    sessions.activeSessionId = null;
    ui.followLatest = false;
    ui.pendingScrollOrdinal = null;
    ui.pendingScrollSession = null;
    document.body.innerHTML = "";
  });

  it("records the viewport anchor on scroll", async () => {
    virtualizerMock.getVirtualItems.mockImplementation(
      () => virtualRows,
    );
    virtualizerMock.scrollOffset = 130;

    component = mount(MessageList, { target: document.body });
    await tick();

    const container = document.querySelector(
      ".message-list-scroll",
    );
    expect(container).not.toBeNull();
    container!.dispatchEvent(new Event("scroll"));

    await vi.waitFor(() => {
      expect(scrollMemory.get("s1")).toEqual({
        ordinal: 1,
        offsetPx: 10,
      });
    });
  });

  it("restores the remembered position once messages are loaded", async () => {
    scrollMemory.remember("s1", { ordinal: 1, offsetPx: 10 });
    virtualizerMock.getVirtualItems.mockImplementation(
      () => virtualRows,
    );
    virtualizerMock.getOffsetForIndex.mockImplementation(() => [
      120,
      "start",
    ]);

    component = mount(MessageList, { target: document.body });

    await vi.waitFor(() => {
      expect(virtualizerMock.scrollToOffset).toHaveBeenCalledWith(
        130,
        { align: "start" },
      );
    });
  });

  it("skips restore when a pending scroll targets the session", async () => {
    scrollMemory.remember("s1", { ordinal: 1, offsetPx: 10 });
    ui.pendingScrollOrdinal = 0;
    ui.pendingScrollSession = "s1";
    virtualizerMock.getVirtualItems.mockImplementation(
      () => virtualRows,
    );
    virtualizerMock.getOffsetForIndex.mockImplementation(() => [
      120,
      "start",
    ]);

    component = mount(MessageList, { target: document.body });
    await tick();
    await new Promise((r) => setTimeout(r, 20));

    expect(virtualizerMock.scrollToOffset).not.toHaveBeenCalled();
    expect(virtualizerMock.scrollToIndex).not.toHaveBeenCalled();
  });

  it("skips restore when follow latest is enabled", async () => {
    scrollMemory.remember("s1", { ordinal: 0, offsetPx: 55 });
    ui.followLatest = true;
    ui.followLatestRequest = 1;
    virtualizerMock.getVirtualItems.mockImplementation(
      () => virtualRows,
    );
    virtualizerMock.getOffsetForIndex.mockImplementation(() => [
      120,
      "start",
    ]);

    component = mount(MessageList, { target: document.body });
    await tick();
    await new Promise((r) => setTimeout(r, 20));

    // Follow latest scrolls without the anchor's pixel offset.
    expect(
      virtualizerMock.scrollToOffset,
    ).not.toHaveBeenCalledWith(175, expect.anything());
  });
});
