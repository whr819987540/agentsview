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
    postApiV1SessionFolders: vi.fn(),
    patchApiV1SessionFoldersById: vi.fn(),
    deleteApiV1SessionFoldersById: vi.fn(),
    putApiV1SessionFoldersByIdSessions: vi.fn(),
    postApiV1SessionFoldersRemoveSessions: vi.fn(),
  },
}));

const folderService = SessionFoldersService as unknown as Record<
  | "getApiV1SessionFolders"
  | "postApiV1SessionFolders"
  | "patchApiV1SessionFoldersById"
  | "putApiV1SessionFoldersByIdSessions"
  | "postApiV1SessionFoldersRemoveSessions",
  ReturnType<typeof vi.fn>
>;

class ResizeObserverMock {
  observe = vi.fn();
  disconnect = vi.fn();
}

function makeSession(id: string, project: string, endedAt: string): Session {
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
    project,
    machine: "local",
    agent: "claude",
    display_name: `Session ${id}`,
    first_message: null,
    started_at: endedAt,
    ended_at: endedAt,
    message_count: 1,
    user_message_count: 1,
    total_output_tokens: 0,
    peak_context_tokens: 0,
    is_automated: false,
    created_at: endedAt,
  };
}

function folder(id: number, name: string, sessionIds: string[], project = "alpha"): SessionFolder {
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

function folderRows(): string[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".folder-row")).map(
    (row) =>
      `${row.querySelector(".folder-name")?.textContent}(${row.querySelector(".folder-count")?.textContent})`,
  );
}

function sessionRow(id: string): HTMLElement {
  return document.querySelector<HTMLElement>(`.session-item[data-session-id="${id}"]`)!;
}

function dragEvent(type: string): Event {
  return new Event(type, { bubbles: true, cancelable: true });
}

/** Press Escape the way a browser delivers it to the document. */
function pressEscape(): KeyboardEvent {
  const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
  document.dispatchEvent(event);
  return event;
}

describe("SessionList folders", () => {
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
    sessions.sessions = [
      makeSession("a1", "alpha", "2026-10-03T00:00:00Z"),
      makeSession("a2", "alpha", "2026-10-02T00:00:00Z"),
      makeSession("a3", "alpha", "2026-10-01T00:00:00Z"),
    ];
    sessions.activeSessionId = null;
    sessions.nextCursor = null;
    sessions.loading = false;
    sessions.selectMode = false;
    sessions.selectedIds = new Set();
    sessions.filters = { ...sessions.filters, project: "alpha" };
    sessions.sidebarIndexVersion++;
    sessions.hydratedSessionsByVersion = new Map([[sessions.sidebarIndexVersion, new Map()]]);

    const initial = { folders: [folder(1, "Docs", ["a2"])], editable: true };
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
    sessions.filters = { ...sessions.filters, project: "" };
    Object.defineProperty(globalThis, "ResizeObserver", {
      configurable: true,
      writable: true,
      value: originalResizeObserver,
    });
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it("lists a filtered project's folders above its unfiled sessions", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();

    expect(folderRows()).toEqual(["Docs(1)"]);
    expect(visibleSessionIds()).toEqual(["a1", "a3"]);
  });

  it("keeps folders out of a list that spans projects", async () => {
    sessions.filters = { ...sessions.filters, project: "" };
    component = mount(SessionList, { target: document.body });
    await settle();

    expect(folderRows()).toEqual([]);
    expect(visibleSessionIds()).toEqual(["a1", "a2", "a3"]);
    expect(document.querySelector(`[aria-label="${m.sidebar_folder_create()}"]`)).toBeNull();
  });

  it("expands a folder to show its sessions indented, and remembers it", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();

    document.querySelector<HTMLButtonElement>(".folder-row")!.click();
    await settle();

    expect(visibleSessionIds()).toEqual(["a2", "a1", "a3"]);
    expect(sessionRow("a2").style.paddingLeft).toBe("22px");
    expect(sessionRow("a1").style.paddingLeft).toBe("8px");

    void unmount(component);
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(visibleSessionIds()).toEqual(["a2", "a1", "a3"]);
  });

  it("spaces an open folder's sessions from the unfiled ones only while it is open", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();
    expect(document.querySelector(".folder-gap")).toBeNull();

    const folderRow = document.querySelector<HTMLButtonElement>(".folder-row")!;
    folderRow.click();
    await settle();
    expect(document.querySelectorAll(".folder-gap")).toHaveLength(1);

    folderRow.click();
    await settle();
    expect(document.querySelector(".folder-gap")).toBeNull();
  });

  it("takes a session out of its folder when it is dropped on the gap", async () => {
    folderService.postApiV1SessionFoldersRemoveSessions.mockResolvedValue({
      folders: [folder(1, "Docs", [])],
      editable: true,
    });
    component = mount(SessionList, { target: document.body });
    await settle();
    document.querySelector<HTMLButtonElement>(".folder-row")!.click();
    await settle();

    sessionRow("a2").dispatchEvent(dragEvent("dragstart"));
    const gap = document.querySelector<HTMLElement>(".folder-gap")!;
    const over = dragEvent("dragover");
    gap.dispatchEvent(over);
    expect(over.defaultPrevented).toBe(true);

    gap.dispatchEvent(dragEvent("drop"));
    await settle();

    expect(folderService.postApiV1SessionFoldersRemoveSessions).toHaveBeenCalledWith({
      session_ids: ["a2"],
    });
    expect(folderRows()).toEqual(["Docs(0)"]);
    expect(document.querySelector(".folder-gap")).toBeNull();
  });

  it("files a dragged session into the folder it is dropped on", async () => {
    folderService.putApiV1SessionFoldersByIdSessions.mockResolvedValue({
      folders: [folder(1, "Docs", ["a1", "a2"])],
      editable: true,
    });
    component = mount(SessionList, { target: document.body });
    await settle();

    sessionRow("a1").dispatchEvent(dragEvent("dragstart"));
    const folderRow = document.querySelector<HTMLElement>(".folder-row")!;
    const over = dragEvent("dragover");
    folderRow.dispatchEvent(over);
    await tick();
    expect(over.defaultPrevented).toBe(true);
    expect(folderRow.classList.contains("drop-active")).toBe(true);

    folderRow.dispatchEvent(dragEvent("drop"));
    await settle();

    expect(folderService.putApiV1SessionFoldersByIdSessions).toHaveBeenCalledWith(
      { id: 1 },
      { session_ids: ["a1"] },
    );
    expect(folderRows()).toEqual(["Docs(2)"]);
    expect(visibleSessionIds()).toEqual(["a3"]);
  });

  it("refuses a drop on another project's folder", async () => {
    localStorage.setItem(STORAGE_KEY_GROUP, "project");
    sessions.filters = { ...sessions.filters, project: "" };
    sessions.sessions = [
      makeSession("a1", "alpha", "2026-10-03T00:00:00Z"),
      makeSession("b1", "beta", "2026-10-02T00:00:00Z"),
    ];
    sessionFolders.folders = [folder(1, "Docs", [])];
    folderService.getApiV1SessionFolders.mockResolvedValue({
      folders: sessionFolders.folders,
      editable: true,
    });
    component = mount(SessionList, { target: document.body });
    await settle();
    for (const header of Array.from(
      document.querySelectorAll<HTMLButtonElement>(".group-header"),
    )) {
      header.click();
      await settle();
    }
    expect(folderRows()).toEqual(["Docs(0)"]);

    const folderRow = document.querySelector<HTMLElement>(".folder-row")!;
    sessionRow("b1").dispatchEvent(dragEvent("dragstart"));
    const foreign = dragEvent("dragover");
    folderRow.dispatchEvent(foreign);
    sessionRow("b1").dispatchEvent(dragEvent("dragend"));

    sessionRow("a1").dispatchEvent(dragEvent("dragstart"));
    const own = dragEvent("dragover");
    folderRow.dispatchEvent(own);

    expect(foreign.defaultPrevented).toBe(false);
    expect(own.defaultPrevented).toBe(true);
  });

  it("moves a session from its context menu folder picker", async () => {
    folderService.putApiV1SessionFoldersByIdSessions.mockResolvedValue({
      folders: [folder(1, "Docs", ["a2", "a3"])],
      editable: true,
    });
    component = mount(SessionList, { target: document.body });
    await settle();

    sessionRow("a3").dispatchEvent(
      new MouseEvent("contextmenu", { bubbles: true, clientX: 12, clientY: 12 }),
    );
    await tick();
    Array.from(document.querySelectorAll<HTMLButtonElement>(".context-menu-item"))
      .find((button) => button.textContent?.includes(m.sidebar_row_move_to_folder()))!
      .click();
    await settle();
    Array.from(document.querySelectorAll<HTMLButtonElement>(".folder-picker-option"))
      .find((button) => button.textContent?.includes("Docs"))!
      .click();
    await settle();

    expect(folderService.putApiV1SessionFoldersByIdSessions).toHaveBeenCalledWith(
      { id: 1 },
      { session_ids: ["a3"] },
    );
    expect(document.querySelector(".folder-picker")).toBeNull();
    expect(visibleSessionIds()).toEqual(["a1"]);
  });

  // A claimed Escape keeps the global shortcut from also leaving the session.
  it("closes a folder's menu on Escape and claims the key", async () => {
    component = mount(SessionList, { target: document.body });
    await settle();

    document
      .querySelector<HTMLElement>(".folder-row")!
      .dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, clientX: 12, clientY: 12 }));
    await tick();
    expect(document.querySelector(".context-menu")).not.toBeNull();

    const escape = pressEscape();
    await tick();

    expect(document.querySelector(".context-menu")).toBeNull();
    expect(escape.defaultPrevented).toBe(true);
  });

  it("closes a project's menu on Escape and claims the key", async () => {
    localStorage.setItem(STORAGE_KEY_GROUP, "project");
    sessions.filters = { ...sessions.filters, project: "" };
    component = mount(SessionList, { target: document.body });
    await settle();

    document
      .querySelector<HTMLElement>(".group-header")!
      .dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, clientX: 12, clientY: 12 }));
    await tick();
    expect(document.querySelector(".context-menu")).not.toBeNull();

    const escape = pressEscape();
    await tick();

    expect(document.querySelector(".context-menu")).toBeNull();
    expect(escape.defaultPrevented).toBe(true);
  });

  it("creates a folder from the header and saves the name typed into it", async () => {
    folderService.postApiV1SessionFolders.mockResolvedValue(folder(5, "New folder", []));
    folderService.patchApiV1SessionFoldersById.mockResolvedValue(folder(5, "Specs", []));
    component = mount(SessionList, { target: document.body });
    await settle();

    document
      .querySelector<HTMLButtonElement>(`[aria-label="${m.sidebar_folder_create()}"]`)!
      .click();
    await settle();

    expect(folderService.postApiV1SessionFolders).toHaveBeenCalledWith({
      project: "alpha",
      name: "New folder",
    });
    const input = document.querySelector<HTMLInputElement>(".folder-row.editing input")!;
    expect(input.value).toBe("New folder");
    input.value = "Specs";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await settle();

    expect(folderService.patchApiV1SessionFoldersById).toHaveBeenCalledWith(
      { id: 5 },
      { name: "Specs" },
    );
    expect(folderRows()).toEqual(["Docs(1)", "Specs(0)"]);
  });
});
