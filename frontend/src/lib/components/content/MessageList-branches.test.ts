// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, tick, unmount } from "svelte";
import type {
  DbMessage as Message,
  DbSession,
  ServiceBranchPoints,
} from "../../api/generated/index.js";
import { messages } from "../../stores/messages.svelte.js";
import { readProgress } from "../../stores/read-progress.svelte.js";
import { sessions } from "../../stores/sessions.svelte.js";
import { ui } from "../../stores/ui.svelte.js";
import { setLocale } from "../../i18n/index.js";

const virtualizerMock = vi.hoisted(() => ({
  options: { count: 0 },
  scrollOffset: 0,
  scrollRect: { height: 200 },
  getVirtualItems: vi.fn<() => Array<{ index: number; key: string; start: number; end: number }>>(
    () => [],
  ),
  getTotalSize: vi.fn(() => 120),
  measureElement: vi.fn(),
  scrollToIndex: vi.fn(),
  scrollToOffset: vi.fn(),
  getOffsetForIndex: vi.fn(),
  getOffsetForAlignment: vi.fn((offset: number) => offset),
}));

vi.mock("../../virtual/createVirtualizer.svelte.js", () => ({
  createVirtualizer: (optsFn: () => { count: number }) => ({
    get instance() {
      virtualizerMock.options.count = optsFn().count;
      return virtualizerMock;
    },
  }),
}));

const branchPointsMock = vi.hoisted(() => ({
  fetchBranchPoints: vi.fn<(id: string) => Promise<ServiceBranchPoints>>(),
}));

vi.mock("../../api/branchPoints.js", () => branchPointsMock);

// @ts-ignore
import MessageList from "./MessageList.svelte";

function message(ordinal: number, content: string, extra: Partial<Message> = {}): Message {
  return {
    id: ordinal + 100,
    session_id: "s1",
    ordinal,
    role: ordinal % 2 === 0 ? "user" : "assistant",
    content,
    timestamp: new Date(Date.UTC(2026, 0, 1, 10, 0, ordinal + 10)).toISOString(),
    has_thinking: false,
    thinking_text: "",
    has_tool_use: false,
    content_length: content.length,
    model: "",
    token_usage: null,
    context_tokens: 0,
    output_tokens: 0,
    has_context_tokens: false,
    has_output_tokens: false,
    is_system: false,
    ...extra,
  };
}

function session(id: string, title: string, userMessages: number): DbSession {
  return {
    id,
    display_name: title,
    first_message: null,
    user_message_count: userMessages,
  } as DbSession;
}

function forkBoundary(): Message {
  return message(-1, "parent-1", {
    role: "system",
    is_system: true,
    source_subtype: "fork_boundary",
  });
}

function setVirtualRows(count: number) {
  virtualizerMock.getVirtualItems.mockReturnValue(
    Array.from({ length: count }, (_, index) => ({
      index,
      key: `row-${index}`,
      start: index * 100,
      end: index * 100 + 100,
    })),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) {
    await tick();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

type ListExports = { scrollToOrdinal(ordinal: number): void };

describe("MessageList branches", () => {
  let component: ReturnType<typeof mount> | undefined;
  let rafSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.clearAllMocks();
    messages.clear();
    sessions.activeSessionId = "s1";
    messages.sessionId = "s1";
    messages.activeSessionToken = "current";
    ui.followLatest = false;
    ui.sortNewestFirst = false;
    ui.showAllBlocks();
    ui.setTranscriptMode("normal");
    ui.selectedOrdinal = null;
    readProgress.reset();
    setVirtualRows(8);
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
    readProgress.reset();
    document.body.innerHTML = "";
  });

  function rowTexts(): string[] {
    return [...document.querySelectorAll(".virtual-row")].map(
      (row) => row.textContent?.replace(/\s+/g, " ").trim() ?? "",
    );
  }

  describe("in a fork with inherited context", () => {
    beforeEach(() => {
      messages.messages = [
        message(-3, "inherited question"),
        message(-2, "inherited answer"),
        forkBoundary(),
        message(0, "fork question"),
      ];
      messages.messageCount = 1;
      branchPointsMock.fetchBranchPoints.mockResolvedValue({
        session_id: "s1",
        leaves: {
          parent: session("parent-1", "Parent work", 3),
          relationship_type: "fork",
          after_ordinal: 5,
          after_turn: 3,
          turns_after: 0,
        },
        points: [],
      });
    });

    it("hides the inherited messages behind the boundary until shown", async () => {
      component = mount(MessageList, { target: document.body });
      await settle();

      expect(virtualizerMock.options.count).toBe(2);
      const collapsed = rowTexts().join(" | ");
      expect(collapsed).not.toContain("inherited question");
      expect(collapsed).toContain("Forked from Parent work after turn 3");
      expect(collapsed).toContain("History inherited from the parent session is hidden.");

      const show = [...document.querySelectorAll("button")].find((b) =>
        b.textContent?.includes("Show 2 inherited messages"),
      );
      expect(show).toBeDefined();
      show!.click();
      await settle();

      expect(virtualizerMock.options.count).toBe(4);
      expect(rowTexts().join(" | ")).toContain("inherited question");
      expect(document.body.textContent).toContain("Hide inherited messages");
    });

    it("shows the inherited messages when a jump targets one of them", async () => {
      component = mount(MessageList, { target: document.body });
      await settle();
      expect(virtualizerMock.options.count).toBe(2);

      (component as ListExports).scrollToOrdinal(-3);
      await settle();

      expect(virtualizerMock.options.count).toBe(4);
      expect(rowTexts()[0]).toContain("inherited question");
    });
  });

  it("marks where forks, continuations, and rewinds leave a parent", async () => {
    messages.messages = [
      message(0, "first prompt"),
      message(1, "first answer"),
      message(2, "second prompt"),
      message(3, "second answer"),
    ];
    messages.messageCount = 4;
    branchPointsMock.fetchBranchPoints.mockResolvedValue({
      session_id: "s1",
      points: [
        {
          kind: "continuation",
          after_ordinal: 1,
          after_turn: 1,
          turns_after: 1,
          session: session("moved", "Moved on", 2),
        },
        {
          kind: "rewind",
          after_ordinal: 1,
          after_turn: 1,
          turns_after: 1,
          inputs: ["first try", "second try"],
        },
        {
          kind: "fork",
          after_ordinal: 3,
          after_turn: 2,
          turns_after: 0,
          session: session("forked", "Side quest", 0),
        },
      ],
    });

    component = mount(MessageList, { target: document.body });
    await settle();

    expect(branchPointsMock.fetchBranchPoints).toHaveBeenCalledWith("s1", expect.any(AbortSignal));
    const rows = rowTexts();
    expect(rows[0]).not.toContain("Continued in");
    expect(rows[1]).toContain(
      "Continued in Moved on from here. The 1 later turn below is not part of it.",
    );
    expect(rows[1]).toContain("2 turns");
    expect(rows[1]).toContain("Rewound here: 2 inputs abandoned");
    expect(rows[1]).toContain("second try");
    expect(rows[3]).toContain("Forked here: Side quest");
    expect(rows[3]).toContain("no turns yet");
    const link = document.querySelector<HTMLAnchorElement>('[data-branch-kind="fork"] a');
    expect(link?.getAttribute("href")).toContain("/sessions/forked");
  });

  it("tells a continuation which parent turn it resumed from", async () => {
    messages.messages = [message(0, "instead"), message(1, "answer")];
    messages.messageCount = 2;
    branchPointsMock.fetchBranchPoints.mockResolvedValue({
      session_id: "s1",
      leaves: {
        parent: session("parent-1", "Parent work", 5),
        relationship_type: "continuation",
        after_ordinal: 19,
        after_turn: 1,
        turns_after: 4,
      },
      points: [],
    });

    component = mount(MessageList, { target: document.body });
    await settle();

    const first = rowTexts()[0]!;
    expect(first).toContain("Continues Parent work after turn 1");
    expect(first).toContain("That session's 4 later turns are not part of this one.");
    const open = document.querySelector<HTMLAnchorElement>(".leave-open");
    expect(open?.getAttribute("href")).toContain("/sessions/parent-1");
    expect(open?.getAttribute("href")).toContain("msg=19");
    expect(rowTexts()[1]).not.toContain("Continues");
  });
});
