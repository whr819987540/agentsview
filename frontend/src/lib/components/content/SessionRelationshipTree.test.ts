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
import type { Session } from "../../api/types/core.js";
import type { SessionTreeResponse } from "../../api/generated/index.js";

const mocks = vi.hoisted(() => ({
  fetchSessionTree: vi.fn(),
}));

vi.mock("../../api/sessionTree.js", () => ({
  fetchSessionTree: mocks.fetchSessionTree,
}));

import { m } from "../../i18n/index.js";
import { router } from "../../stores/router.svelte.js";
import { ui } from "../../stores/ui.svelte.js";
// @ts-ignore
import SessionRelationshipTree from "./SessionRelationshipTree.svelte";

function makeSession(
  id: string,
  overrides: Partial<Session> = {},
): Session {
  return {
    compaction_count: 0,
    consecutive_failure_max: 0,
    edit_churn_count: 0,
    ended_with_role: "",
    final_failure_streak: 0,
    has_peak_context_tokens: false,
    has_total_output_tokens: false,
    mid_task_compaction_count: 0,
    outcome: "",
    outcome_confidence: "",
    secret_leak_count: 0,
    tool_failure_signal_count: 0,
    tool_retry_count: 0,
    id,
    project: "agentsview",
    machine: "test",
    agent: "codex",
    first_message: `First message ${id}`,
    started_at: "2025-01-15T10:00:00Z",
    ended_at: "2025-01-15T10:05:00Z",
    message_count: 2,
    user_message_count: 1,
    total_output_tokens: 0,
    peak_context_tokens: 0,
    is_automated: false,
    created_at: "2025-01-15T10:00:00Z",
    ...overrides,
  };
}

function makeTree(): SessionTreeResponse {
  return {
    active_session_id: "child",
    truncated: true,
    root: {
      session: makeSession("root", {
        relationship_type: "",
        message_count: 5,
      }),
      depth: 0,
      is_active: false,
      is_leaf: false,
      is_branch_start: true,
      children: [
        {
          session: makeSession("child", {
            relationship_type: "fork",
            display_name: "Active fork",
            first_message: "Active fork preview",
          }),
          depth: 1,
          is_active: true,
          is_leaf: true,
          is_branch_start: false,
          children: [],
        },
        {
          session: makeSession("sibling", {
            relationship_type: "subagent",
            message_count: 1,
          }),
          depth: 1,
          is_active: false,
          is_leaf: true,
          is_branch_start: false,
          children: [],
        },
      ],
    },
  };
}

async function flush() {
  await tick();
  await Promise.resolve();
  await tick();
}

function mockHeight(element: Element, height: number) {
  Object.defineProperty(element, "getBoundingClientRect", {
    configurable: true,
    value: () => ({
      width: 320,
      height,
      top: 0,
      right: 320,
      bottom: height,
      left: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }),
  });
}

function getTree() {
  const tree = document.querySelector<HTMLElement>(".session-tree");
  expect(tree).not.toBeNull();
  return tree!;
}

function getResizeHandle() {
  return document.querySelector<HTMLElement>(
    `[aria-label="${m.session_tree_resize()}"]`,
  );
}

function pointerEvent(type: string, clientY: number) {
  return new PointerEvent(type, {
    bubbles: true,
    clientY,
    pointerId: 1,
  });
}

async function dragHandle(startY: number, endY: number) {
  const handle = getResizeHandle();
  expect(handle).not.toBeNull();

  handle!.dispatchEvent(pointerEvent("pointerdown", startY));
  handle!.dispatchEvent(pointerEvent("pointermove", endY));
  await tick();
  handle!.dispatchEvent(pointerEvent("pointerup", endY));
  await tick();
}

async function pressKey(key: string) {
  getResizeHandle()!.dispatchEvent(
    new KeyboardEvent("keydown", { bubbles: true, key }),
  );
  await tick();
}

describe("SessionRelationshipTree", () => {
  let component: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    mocks.fetchSessionTree.mockReset();
    window.history.replaceState(null, "", "/sessions/child");
    router.route = "sessions";
    router.sessionId = "child";
    router.params = {};
  });

  afterEach(() => {
    if (component) {
      unmount(component);
      component = undefined;
    }
    ui.setSessionTreeHeight(280);
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("hides single-node trees", async () => {
    mocks.fetchSessionTree.mockResolvedValue({
      active_session_id: "solo",
      truncated: false,
      root: {
        session: makeSession("solo"),
        depth: 0,
        is_active: true,
        is_leaf: true,
        is_branch_start: false,
        children: [],
      },
    });

    component = mount(SessionRelationshipTree, {
      target: document.body,
      props: { sessionId: "solo" },
    });
    await flush();

    expect(
      document.querySelector(".session-tree"),
    ).toBeNull();
    expect(getResizeHandle()).toBeNull();
  });

  it("renders active, leaf, branch, and localized labels", async () => {
    mocks.fetchSessionTree.mockResolvedValue(makeTree());

    component = mount(SessionRelationshipTree, {
      target: document.body,
      props: { sessionId: "child" },
    });
    await flush();

    expect(document.body.textContent).toContain(
      m.session_tree_title(),
    );
    expect(document.body.textContent).toContain(
      m.session_tree_truncated(),
    );
    expect(document.body.textContent).toContain(
      m.session_tree_relationship_fork(),
    );
    expect(document.body.textContent).toContain(
      m.session_tree_message_count({
        count: 1,
        countLabel: "1",
      }),
    );

    const active = document.querySelector<HTMLAnchorElement>(
      'a[href="/sessions/child"]',
    );
    expect(active).not.toBeNull();
    expect(active?.classList.contains("active")).toBe(true);
    expect(active?.classList.contains("leaf")).toBe(true);
    expect(active?.getAttribute("aria-current")).toBe("page");

    const root = document.querySelector<HTMLAnchorElement>(
      'a[href="/sessions/root"]',
    );
    expect(root?.classList.contains("branch-start")).toBe(true);
  });

  it("navigates through router when a node is clicked", async () => {
    mocks.fetchSessionTree.mockResolvedValue(makeTree());
    const navigate = vi
      .spyOn(router, "navigateToSession")
      .mockImplementation(() => {});

    component = mount(SessionRelationshipTree, {
      target: document.body,
      props: { sessionId: "child" },
    });
    await flush();

    document
      .querySelector<HTMLAnchorElement>('a[href="/sessions/sibling"]')!
      .click();

    expect(navigate).toHaveBeenCalledWith("sibling");
  });

  describe("height", () => {
    // Stands in for the vitals column the tree renders into.
    let column: HTMLElement;

    beforeEach(() => {
      mocks.fetchSessionTree.mockResolvedValue(makeTree());
      column = document.createElement("div");
      document.body.appendChild(column);
    });

    async function mountInColumn() {
      component = mount(SessionRelationshipTree, {
        target: column,
        props: { sessionId: "child" },
      });
      await flush();
    }

    it("limits the tree to the stored height", async () => {
      ui.setSessionTreeHeight(360);

      await mountInColumn();

      expect(getTree().style.maxHeight).toBe("360px");
      expect(getResizeHandle()?.getAttribute("aria-valuenow")).toBe(
        "360",
      );
    });

    it("grows a short tree from its rendered height when dragged down", async () => {
      await mountInColumn();
      mockHeight(getTree(), 150);

      await dragHandle(400, 460);

      expect(ui.sessionTreeHeight).toBe(210);
      expect(getTree().style.maxHeight).toBe("210px");
    });

    it("shrinks from arrow keys and stops at the minimum", async () => {
      await mountInColumn();
      mockHeight(getTree(), 280);

      await pressKey("ArrowUp");
      expect(ui.sessionTreeHeight).toBe(256);

      await dragHandle(400, 0);
      expect(ui.sessionTreeHeight).toBe(96);
      expect(getTree().style.maxHeight).toBe("96px");
    });

    it("keeps room for the session vitals below the tree", async () => {
      mockHeight(column, 500);
      await mountInColumn();
      mockHeight(getTree(), 280);

      await dragHandle(400, 700);

      // A 500px column keeps 160px for the handle and vitals.
      expect(ui.sessionTreeHeight).toBe(340);
      expect(getTree().style.maxHeight).toBe("340px");
      expect(getResizeHandle()?.getAttribute("aria-valuemax")).toBe(
        "340",
      );
    });

    it("fits a taller stored height to a short column without forgetting it", async () => {
      ui.setSessionTreeHeight(600);
      mockHeight(column, 500);

      await mountInColumn();
      expect(getTree().style.maxHeight).toBe("340px");

      mockHeight(getTree(), 340);
      await dragHandle(400, 480);

      expect(getTree().style.maxHeight).toBe("340px");
      expect(ui.sessionTreeHeight).toBe(600);
    });
  });
});
