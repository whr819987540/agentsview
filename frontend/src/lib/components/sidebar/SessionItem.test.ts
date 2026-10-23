// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { flushSync, mount, unmount } from "svelte";
import { createClassComponent } from "svelte/legacy";
import SessionItem from "./SessionItem.svelte";
import { sessions } from "../../stores/sessions.svelte.js";
import { sessionEndMarks } from "../../stores/sessionEndMarks.svelte.js";

let component: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (component) unmount(component);
  component = undefined;
  document.body.innerHTML = "";
  sessions.machineLabels = {};
});

describe("SessionItem identity", () => {
  const baseSession = {
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
    id: "session",
    project: "project",
    machine: "local",
    agent: "claude",
    first_message: "Inspect the issue",
    started_at: "2024-01-01T00:00:00Z",
    ended_at: "2024-01-01T00:01:00Z",
    created_at: "2024-01-01T00:00:00Z",
    message_count: 1,
    user_message_count: 1,
  };

  function mountSession(overrides: Record<string, unknown> = {}, props = {}) {
    component = mount(SessionItem, {
      target: document.body,
      props: {
        session: { ...baseSession, ...overrides },
        ...props,
      },
    });
    flushSync();
  }

  it("renders the session label and entrypoint badge", () => {
    sessions.machineLabels = { "installation-a": "Workstation A" };
    mountSession({
      id: "custom-label",
      display_name: "A session with a long title",
      agent_label: "Claude Triage",
      entrypoint: "sdk-cli",
      machine: "installation-a",
    });

    const agentTag = document.querySelector<HTMLElement>(".agent-tag");
    expect(agentTag?.textContent).toBe("Claude Triage");
    expect(agentTag?.title).toBe("Claude Triage");
    expect(document.querySelector(".entrypoint-tag")?.textContent).toBe("sdk-cli");
    expect(document.querySelector<HTMLElement>(".session-name")?.title).toBe(
      "A session with a long title",
    );
    expect(document.querySelector<HTMLElement>(".session-project")?.title).toBe("project");
    expect(document.querySelector<HTMLElement>(".entrypoint-tag")?.title).toBe("sdk-cli");
    expect(document.querySelector(".machine-tag")?.textContent).toBe("Workstation A");
  });

  it("uses the registry label when no override exists", () => {
    mountSession({ agent: "qwen" });

    const agentTag = document.querySelector<HTMLElement>(".agent-tag");
    expect(agentTag?.textContent).toBe("Qwen Code");
    expect(agentTag?.title).toBe("Qwen Code");
  });

  it("uses the registry label for a whitespace-only override", () => {
    mountSession({ agent: "qwen", agent_label: "   " });

    const agentTag = document.querySelector<HTMLElement>(".agent-tag");
    expect(agentTag?.textContent).toBe("Qwen Code");
    expect(agentTag?.title).toBe("Qwen Code");
  });

  it("updates the visible label and title when the session prop changes", () => {
    const session = { ...baseSession, agent: "qwen", agent_label: "Initial Label" };
    const legacyComponent = createClassComponent({
      component: SessionItem,
      target: document.body,
      props: { session },
    });
    component = legacyComponent as unknown as ReturnType<typeof mount>;
    flushSync();

    legacyComponent.$set({
      session: { ...session, agent_label: "Updated Label" },
    });
    flushSync();

    const agentTag = document.querySelector<HTMLElement>(".agent-tag");
    expect(agentTag?.textContent).toBe("Updated Label");
    expect(agentTag?.title).toBe("Updated Label");
  });

  it("suppresses the default cli entrypoint badge", () => {
    mountSession({ id: "default-entrypoint", entrypoint: "cli" });

    expect(document.querySelector(".agent-tag")?.textContent).toBe("Claude");
    expect(document.querySelector(".entrypoint-tag")).toBeNull();
  });

  it("preserves compact and grouped metadata visibility", () => {
    mountSession({ machine: "remote" }, { compact: true });
    expect(document.querySelector(".agent-tag")).toBeNull();
    expect(document.querySelector(".machine-tag")).toBeNull();
    expect(document.querySelector(".session-item")?.classList.contains("compact")).toBe(true);

    unmount(component!);
    component = undefined;
    document.body.innerHTML = "";

    mountSession({ machine: "remote", agent_label: "Grouped Label" }, { hideAgent: true });
    expect(document.querySelector(".agent-tag")).toBeNull();
    expect(document.querySelector(".machine-tag")?.textContent).toBe("remote");
  });
});

describe("SessionItem end mark", () => {
  const root = {
    id: "root",
    project: "project",
    machine: "local",
    agent: "claude",
    first_message: "Refactor the exporter",
    started_at: "2024-01-01T00:00:00Z",
    ended_at: "2024-01-01T01:00:00Z",
    created_at: "2024-01-01T00:00:00Z",
    termination_status: "truncated",
    message_count: 4,
    user_message_count: 2,
  };
  const continuation = {
    ...root,
    id: "continuation",
    started_at: "2024-01-01T02:00:00Z",
    ended_at: "2024-01-01T03:00:00Z",
  };

  afterEach(() => {
    sessionEndMarks.marks = new Map();
    sessionEndMarks.editable = false;
    vi.restoreAllMocks();
  });

  function mountRow(props: Record<string, unknown> = {}) {
    component = mount(SessionItem, {
      target: document.body,
      props: { session: root, ...props },
    });
    flushSync();
  }

  function openContextMenu() {
    document
      .querySelector(".session-item")!
      .dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, clientX: 10, clientY: 10 }));
    flushSync();
  }

  function menuItem(label: string): HTMLButtonElement | undefined {
    return [...document.querySelectorAll<HTMLButtonElement>(".context-menu-item")].find(
      (button) => button.textContent?.trim() === label,
    );
  }

  it("shows a check in place of the status dot while the mark holds", () => {
    sessionEndMarks.marks = new Map([["root", "2024-01-01T01:00:00Z"]]);
    mountRow();

    const check = document.querySelector<HTMLElement>(".status-slot .ended-mark");
    expect(check?.getAttribute("aria-label")).toBe("Marked as ended");
    expect(document.querySelector(".status-slot .kit-status-dot")).toBeNull();
  });

  it("shows the live status again once the row has newer activity", () => {
    sessionEndMarks.marks = new Map([["root", "2024-01-01T01:00:00Z"]]);
    mountRow({ groupSessions: [root, continuation], statusSession: continuation });

    expect(document.querySelector(".ended-mark")).toBeNull();
    expect(document.querySelector(".status-slot .kit-status-dot--unclean")).not.toBeNull();
  });

  it("marks and unmarks the row from its context menu", () => {
    sessionEndMarks.editable = true;
    const mark = vi.spyOn(sessionEndMarks, "mark").mockResolvedValue();
    const unmark = vi.spyOn(sessionEndMarks, "unmark").mockResolvedValue();
    mountRow({ groupSessions: [continuation, root], statusSession: continuation });

    openContextMenu();
    menuItem("Mark as ended")!.click();
    flushSync();
    expect(mark).toHaveBeenCalledWith([root, continuation]);
    expect(document.querySelector(".context-menu")).toBeNull();

    sessionEndMarks.marks = new Map([["root", "2024-01-01T03:00:00Z"]]);
    flushSync();
    openContextMenu();
    expect(menuItem("Mark as ended")).toBeUndefined();
    menuItem("Unmark as ended")!.click();
    expect(unmark).toHaveBeenCalledWith([root, continuation]);
  });

  it("leaves marking to the parent row on child rows", () => {
    sessionEndMarks.editable = true;
    sessionEndMarks.marks = new Map([["root", "2024-01-01T01:00:00Z"]]);
    mountRow({ compact: true, depth: 1 });

    expect(document.querySelector(".ended-mark")).toBeNull();
    openContextMenu();
    expect(menuItem("Mark as ended")).toBeUndefined();
    expect(menuItem("Unmark as ended")).toBeUndefined();
  });

  it("hides the menu item when marks cannot change", () => {
    mountRow();
    openContextMenu();

    expect(menuItem("Mark as ended")).toBeUndefined();
  });

  function rowButton(label: string): HTMLButtonElement | null {
    return document.querySelector<HTMLButtonElement>(`.session-item button[aria-label="${label}"]`);
  }

  it("marks and unmarks the row from its hover button without opening it", () => {
    sessionEndMarks.editable = true;
    const mark = vi.spyOn(sessionEndMarks, "mark").mockResolvedValue();
    const unmark = vi.spyOn(sessionEndMarks, "unmark").mockResolvedValue();
    const select = vi.spyOn(sessions, "selectSession").mockImplementation(() => {});
    mountRow({ groupSessions: [continuation, root], statusSession: continuation });

    expect(rowButton("Unmark as ended")).toBeNull();
    rowButton("Mark as ended")!.click();
    expect(mark).toHaveBeenCalledWith([root, continuation]);

    sessionEndMarks.marks = new Map([["root", "2024-01-01T03:00:00Z"]]);
    flushSync();
    expect(rowButton("Mark as ended")).toBeNull();
    rowButton("Unmark as ended")!.click();
    expect(unmark).toHaveBeenCalledWith([root, continuation]);
    expect(select).not.toHaveBeenCalled();
  });

  it("leaves out the hover button when marks cannot change and on child rows", () => {
    mountRow();
    expect(rowButton("Mark as ended")).toBeNull();

    void unmount(component!);
    component = undefined;
    sessionEndMarks.editable = true;
    mountRow({ compact: true, depth: 1 });
    expect(rowButton("Mark as ended")).toBeNull();
  });
});
