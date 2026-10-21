// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { mount, tick, unmount } from "svelte";
// @ts-ignore
import SessionList from "./SessionList.svelte";
import { SessionFoldersService } from "../../api/generated/index";
import type { Session } from "../../api/types.js";
import { sessions } from "../../stores/sessions.svelte.js";
import { sessionFolders, type SessionFolder } from "../../stores/sessionFolders.svelte.js";
import { starred } from "../../stores/starred.svelte.js";
import { ui } from "../../stores/ui.svelte.js";
import { m, setLocale } from "../../i18n/index.js";
import { ITEM_HEIGHT, STORAGE_KEY_GROUP } from "./session-list-utils.js";

vi.mock("../../api/generated/index", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/generated/index")>()),
  SessionFoldersService: {
    getApiV1SessionFolders: vi.fn(),
  },
}));

const folderService = SessionFoldersService as unknown as Record<
  "getApiV1SessionFolders",
  ReturnType<typeof vi.fn>
>;

const EXPANDED_FOLDERS_KEY = "agentsview-sidebar-expanded-folders";

class ResizeObserverMock {
  observe = vi.fn();
  disconnect = vi.fn();
}

function makeSession(overrides: Partial<Session> & { id: string; ended_at: string }): Session {
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
    project: "alpha",
    machine: "local",
    agent: "claude",
    display_name: `Session ${overrides.id}`,
    first_message: null,
    started_at: overrides.ended_at,
    message_count: 4,
    user_message_count: 2,
    total_output_tokens: 0,
    peak_context_tokens: 0,
    is_automated: false,
    created_at: overrides.ended_at,
    ...overrides,
  };
}

/**
 * root-a has a continuation and a subagent, root-b only a subagent, and
 * single has no children.
 */
function chainSessions(): Session[] {
  return [
    makeSession({ id: "root-a", ended_at: "2026-10-05T01:00:00Z" }),
    makeSession({
      id: "cont-a",
      parent_session_id: "root-a",
      relationship_type: "continuation",
      ended_at: "2026-10-05T03:00:00Z",
    }),
    makeSession({
      id: "sub-a",
      parent_session_id: "root-a",
      relationship_type: "subagent",
      ended_at: "2026-10-05T02:00:00Z",
    }),
    makeSession({ id: "root-b", ended_at: "2026-10-04T01:00:00Z" }),
    makeSession({
      id: "sub-b",
      parent_session_id: "root-b",
      relationship_type: "subagent",
      ended_at: "2026-10-04T02:00:00Z",
    }),
    makeSession({ id: "single", ended_at: "2026-10-03T01:00:00Z" }),
  ];
}

function folder(id: number, name: string, sessionIds: string[], project: string): SessionFolder {
  return {
    id,
    project,
    name,
    session_ids: sessionIds,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  };
}

async function settle() {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve();
    await tick();
  }
}

function visibleSessionIds(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".session-item")).map(
    (row) => row.dataset.sessionId ?? "",
  );
}

function subGroupHeaders(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".sub-group-header")).map(
    (header) => header.getAttribute("aria-label") ?? "",
  );
}

function bulkButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(
    `[aria-label="${m.sidebar_expand_all()}"], [aria-label="${m.sidebar_collapse_all()}"]`,
  );
}

async function clickBulk(label: string) {
  const button = bulkButton();
  expect(button?.getAttribute("aria-label")).toBe(label);
  button!.click();
  await settle();
}

async function toggleRow(id: string) {
  document
    .querySelector<HTMLButtonElement>(`.session-item[data-session-id="${id}"] .tree-toggle`)!
    .click();
  await settle();
}

describe("SessionList expand all and collapse all", () => {
  let component: ReturnType<typeof mount> | undefined;
  let originalResizeObserver: typeof ResizeObserver | undefined;

  beforeEach(() => {
    originalResizeObserver = globalThis.ResizeObserver;
    Object.defineProperty(globalThis, "ResizeObserver", {
      configurable: true,
      writable: true,
      value: ResizeObserverMock,
    });
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(ITEM_HEIGHT * 10);
    vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation((cb: FrameRequestCallback) => {
      queueMicrotask(() => cb(0));
      return 1;
    });
    vi.spyOn(sessions, "hydrateVisibleSessions").mockResolvedValue(undefined);
    vi.spyOn(sessions, "load").mockResolvedValue(undefined);
    localStorage.clear();
    setLocale("en");
    ui.sidebarOpen = true;
    ui.isMobileViewport = false;
    starred.filterOnly = false;
    sessions.sessions = chainSessions();
    sessions.activeSessionId = null;
    sessions.nextCursor = null;
    sessions.loading = false;
    sessions.selectMode = false;
    sessions.selectedIds = new Set();
    sessions.filters = { ...sessions.filters, project: "" };
    sessions.sidebarIndexVersion++;
    sessions.hydratedSessionsByVersion = new Map([[sessions.sidebarIndexVersion, new Map()]]);

    const initial = { folders: [] as SessionFolder[], editable: true };
    folderService.getApiV1SessionFolders.mockResolvedValue(initial);
    sessionFolders.folders = initial.folders;
    sessionFolders.editable = true;
  });

  afterEach(() => {
    if (component) {
      void unmount(component);
      component = undefined;
    }
    document.body.innerHTML = "";
    Object.defineProperty(globalThis, "ResizeObserver", {
      configurable: true,
      writable: true,
      value: originalResizeObserver,
    });
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it("opens continuation chains but leaves subagents closed", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(visibleSessionIds()).toEqual(["root-a", "root-b", "single"]);

    await clickBulk(m.sidebar_expand_all());

    // root-a shows its continuation and a closed Subagents group; root-b,
    // whose only child is a subagent, stays closed.
    expect(visibleSessionIds()).toEqual(["root-a", "cont-a", "root-b", "single"]);
    expect(subGroupHeaders()).toEqual([m.sidebar_expand_group({ name: m.sidebar_subagents() })]);
    expect(bulkButton()?.getAttribute("aria-label")).toBe(m.sidebar_collapse_all());

    await clickBulk(m.sidebar_collapse_all());

    expect(visibleSessionIds()).toEqual(["root-a", "root-b", "single"]);
    expect(subGroupHeaders()).toEqual([]);
    expect(bulkButton()?.getAttribute("aria-label")).toBe(m.sidebar_expand_all());
  });

  it("collapses every open chain and keeps each Subagents group's state", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();
    await toggleRow("root-a");
    await toggleRow("root-b");
    expect(visibleSessionIds()).toEqual(["root-a", "cont-a", "sub-a", "root-b", "sub-b", "single"]);

    await clickBulk(m.sidebar_collapse_all());
    expect(visibleSessionIds()).toEqual(["root-a", "root-b", "single"]);

    // The Subagents group root-a had open opens again with its chain.
    await clickBulk(m.sidebar_expand_all());
    expect(visibleSessionIds()).toEqual(["root-a", "cont-a", "sub-a", "root-b", "single"]);
  });

  it("offers Collapse all for an open chain whose only children are subagents", async () => {
    sessions.sessions = chainSessions().filter((s) => !s.id.endsWith("-a"));
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(bulkButton()).toBeNull();

    await toggleRow("root-b");
    expect(visibleSessionIds()).toEqual(["root-b", "sub-b", "single"]);

    await clickBulk(m.sidebar_collapse_all());
    expect(visibleSessionIds()).toEqual(["root-b", "single"]);
    expect(bulkButton()).toBeNull();
  });

  it("opens and closes project sections and their folders", async () => {
    localStorage.setItem(STORAGE_KEY_GROUP, "project");
    sessions.sessions = [
      makeSession({ id: "a1", ended_at: "2026-10-03T00:00:00Z" }),
      makeSession({ id: "a2", ended_at: "2026-10-02T00:00:00Z" }),
      makeSession({ id: "b1", project: "beta", ended_at: "2026-10-01T00:00:00Z" }),
    ];
    sessionFolders.folders = [folder(1, "Docs", ["a2"], "alpha"), folder(2, "Notes", [], "beta")];
    folderService.getApiV1SessionFolders.mockResolvedValue({
      folders: sessionFolders.folders,
      editable: true,
    });
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(document.querySelectorAll(".group-header")).toHaveLength(2);
    expect(visibleSessionIds()).toEqual([]);

    await clickBulk(m.sidebar_expand_all());

    expect(visibleSessionIds()).toEqual(["a2", "a1", "b1"]);
    expect(document.querySelectorAll(".folder-row")).toHaveLength(2);
    expect(JSON.parse(localStorage.getItem(EXPANDED_FOLDERS_KEY) ?? "[]")).toEqual([1, 2]);

    // A section that loads after Expand all opens too.
    sessions.sessions = [
      ...sessions.sessions,
      makeSession({ id: "g1", project: "gamma", ended_at: "2026-09-30T00:00:00Z" }),
    ];
    await settle();
    expect(visibleSessionIds()).toEqual(["a2", "a1", "b1", "g1"]);

    await clickBulk(m.sidebar_collapse_all());

    expect(visibleSessionIds()).toEqual([]);
    expect(document.querySelectorAll(".folder-row")).toHaveLength(0);
    expect(JSON.parse(localStorage.getItem(EXPANDED_FOLDERS_KEY) ?? "[]")).toEqual([]);

    // Reopening a section shows its folders still closed.
    for (const header of Array.from(
      document.querySelectorAll<HTMLButtonElement>(".group-header"),
    )) {
      header.click();
      await settle();
    }
    expect(visibleSessionIds()).toEqual(["a1", "b1", "g1"]);
  });

  it("collapses only the folders of the filtered project", async () => {
    sessions.filters = { ...sessions.filters, project: "alpha" };
    sessions.sessions = [
      makeSession({ id: "a1", ended_at: "2026-10-03T00:00:00Z" }),
      makeSession({ id: "a2", ended_at: "2026-10-02T00:00:00Z" }),
    ];
    sessionFolders.folders = [folder(1, "Docs", ["a2"], "alpha"), folder(2, "Notes", [], "beta")];
    folderService.getApiV1SessionFolders.mockResolvedValue({
      folders: sessionFolders.folders,
      editable: true,
    });
    localStorage.setItem(EXPANDED_FOLDERS_KEY, JSON.stringify([1, 2]));
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(visibleSessionIds()).toEqual(["a2", "a1"]);

    await clickBulk(m.sidebar_collapse_all());

    expect(visibleSessionIds()).toEqual(["a1"]);
    expect(JSON.parse(localStorage.getItem(EXPANDED_FOLDERS_KEY) ?? "[]")).toEqual([2]);
  });
});
