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
});
