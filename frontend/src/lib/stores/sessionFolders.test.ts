import { beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { SessionFoldersService } from "../api/generated/index";
import { ApiError } from "../api/runtime.js";
import {
  createSessionFoldersStore,
  SessionFolderNameTakenError,
  type SessionFolder,
} from "./sessionFolders.svelte.js";

vi.mock("../api/generated/index", () => ({
  SessionFoldersService: {
    getApiV1SessionFolders: vi.fn(),
    postApiV1SessionFolders: vi.fn(),
    patchApiV1SessionFoldersById: vi.fn(),
    deleteApiV1SessionFoldersById: vi.fn(),
    putApiV1SessionFoldersByIdSessions: vi.fn(),
    putApiV1SessionFoldersByIdParent: vi.fn(),
    postApiV1SessionFoldersRemoveSessions: vi.fn(),
  },
}));

const service = SessionFoldersService as unknown as Record<
  | "getApiV1SessionFolders"
  | "postApiV1SessionFolders"
  | "patchApiV1SessionFoldersById"
  | "deleteApiV1SessionFoldersById"
  | "putApiV1SessionFoldersByIdSessions"
  | "putApiV1SessionFoldersByIdParent"
  | "postApiV1SessionFoldersRemoveSessions",
  ReturnType<typeof vi.fn>
>;

function folder(
  id: number,
  name: string,
  sessionIds: string[],
  project = "alpha",
  parentId?: number,
): SessionFolder {
  return {
    id,
    project,
    name,
    ...(parentId === undefined ? {} : { parent_id: parentId }),
    session_ids: sessionIds,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("SessionFoldersStore", () => {
  let store: ReturnType<typeof createSessionFoldersStore>;

  beforeEach(async () => {
    vi.clearAllMocks();
    service.getApiV1SessionFolders.mockResolvedValue({
      folders: [folder(1, "Docs", ["a1"]), folder(2, "Bugs", [])],
      editable: true,
    });
    store = createSessionFoldersStore();
    await store.load();
  });

  it("sorts a project's folders by name with numbers in order", async () => {
    service.getApiV1SessionFolders.mockResolvedValue({
      folders: [
        folder(1, "Sprint 10", []),
        folder(2, "sprint 2", []),
        folder(3, "Archive", []),
        folder(4, "Other project", [], "beta"),
      ],
      editable: true,
    });
    await store.refresh();

    expect(store.foldersFor("alpha").map((f) => f.name)).toEqual([
      "Archive",
      "sprint 2",
      "Sprint 10",
    ]);
  });

  it("shows a move at once and adopts the server's list", async () => {
    const response = deferred<{ folders: SessionFolder[]; editable: boolean }>();
    service.putApiV1SessionFoldersByIdSessions.mockReturnValue(response.promise);

    const moving = store.moveSessions(2, ["a1", "a1-cont"]);

    expect(service.putApiV1SessionFoldersByIdSessions).toHaveBeenCalledWith(
      { id: 2 },
      { session_ids: ["a1", "a1-cont"] },
    );
    expect(store.folderIdBySession.get("a1")).toBe(2);
    expect(store.folders.find((f) => f.id === 1)!.session_ids).toEqual([]);

    // The server skipped a1-cont (another project).
    response.resolve({
      folders: [folder(1, "Docs", []), folder(2, "Bugs", ["a1"])],
      editable: true,
    });
    await moving;

    expect(store.folderIdBySession).toEqual(new Map([["a1", 2]]));
  });

  it("restores the server's folders when a move fails", async () => {
    service.putApiV1SessionFoldersByIdSessions.mockRejectedValue(new ApiError(500, "boom"));
    service.getApiV1SessionFolders.mockResolvedValue({
      folders: [folder(1, "Docs", ["a1"]), folder(2, "Bugs", [])],
      editable: true,
    });

    await expect(store.moveSessions(2, ["a1"])).rejects.toThrow("boom");

    expect(service.getApiV1SessionFolders).toHaveBeenCalledTimes(2);
    expect(store.folderIdBySession.get("a1")).toBe(1);
  });

  it("finds the folder of a row from its first filed session in the project", () => {
    expect(store.folderIdForSessions("alpha", ["a1-cont", "a1"])).toBe(1);
    expect(store.folderIdForSessions("beta", ["a1"])).toBeNull();
    expect(store.folderIdForSessions("alpha", ["a9"])).toBeNull();
  });

  it("reports a taken name as SessionFolderNameTakenError", async () => {
    service.postApiV1SessionFolders.mockRejectedValue(new ApiError(409, "taken"));

    await expect(store.create("alpha", "Docs")).rejects.toBeInstanceOf(SessionFolderNameTakenError);
    expect(store.folders).toHaveLength(2);
  });

  it("keeps the old name when a rename fails for another reason", async () => {
    service.patchApiV1SessionFoldersById.mockRejectedValue(new ApiError(500, "boom"));

    await expect(store.rename(1, "Notes")).rejects.not.toBeInstanceOf(SessionFolderNameTakenError);
    expect(store.folders.find((f) => f.id === 1)!.name).toBe("Docs");
  });

  it("drops a deleted folder and its memberships", async () => {
    service.deleteApiV1SessionFoldersById.mockResolvedValue(undefined);

    await store.remove(1);

    expect(store.foldersFor("alpha").map((f) => f.name)).toEqual(["Bugs"]);
    expect(store.folderIdBySession.size).toBe(0);
  });

  describe("nested folders", () => {
    // Work > Bugs > Triage, with Notes also in Work and Ops in beta.
    beforeEach(async () => {
      service.getApiV1SessionFolders.mockResolvedValue({
        folders: [
          folder(1, "Work", ["w1"]),
          folder(2, "Bugs", ["b1"], "alpha", 1),
          folder(3, "Triage", ["t1"], "alpha", 2),
          folder(4, "Notes", [], "alpha", 1),
          folder(5, "Ops", [], "beta"),
        ],
        editable: true,
      });
      store = createSessionFoldersStore();
      await store.load();
    });

    it("walks a folder's ancestors and subtree", () => {
      expect(store.ancestorIds(3)).toEqual([2, 1]);
      expect(store.parentId(3)).toBe(2);
      expect(store.parentId(1)).toBeNull();
      expect([...store.subtreeIds(1)].sort((a, b) => a - b)).toEqual([1, 2, 3, 4]);
      expect([...store.subtreeIds(3)]).toEqual([3]);
    });

    it("creates a subfolder with its parent and a top-level folder without one", async () => {
      service.postApiV1SessionFolders.mockResolvedValue(folder(6, "Later", [], "alpha", 1));

      await store.create("alpha", "Later", 1);
      await store.create("alpha", "Top");

      expect(service.postApiV1SessionFolders.mock.calls).toEqual([
        [{ project: "alpha", name: "Later", parent_id: 1 }],
        [{ project: "alpha", name: "Top" }],
      ]);
    });

    it("moves a folder to the top level by sending no parent", async () => {
      service.putApiV1SessionFoldersByIdParent.mockResolvedValue(folder(3, "Triage", ["t1"]));

      await store.moveFolder(3, null);

      expect(service.putApiV1SessionFoldersByIdParent).toHaveBeenCalledWith({ id: 3 }, {});
      expect(store.parentId(3)).toBeNull();
      expect(store.ancestorIds(2)).toEqual([1]);
    });

    it("moves a deleted folder's sessions and subfolders into its parent", async () => {
      service.deleteApiV1SessionFoldersById.mockResolvedValue(undefined);

      await store.remove(2);

      expect(store.parentId(3)).toBe(1);
      expect(store.folders.find((f) => f.id === 1)!.session_ids).toEqual(["w1", "b1"]);
      expect(store.folderIdBySession.get("b1")).toBe(1);
      expect(store.folderIdBySession.get("t1")).toBe(3);
    });

    it("moves a deleted top-level folder's contents to the top level", async () => {
      service.deleteApiV1SessionFoldersById.mockResolvedValue(undefined);

      await store.remove(1);

      expect(store.parentId(2)).toBeNull();
      expect(store.parentId(4)).toBeNull();
      expect(store.folderIdBySession.has("w1")).toBe(false);
      expect(store.ancestorIds(3)).toEqual([2]);
    });
  });

  it("numbers a default name already used in the project", () => {
    expect(store.availableName("alpha", "Docs")).toBe("Docs 2");
    expect(store.availableName("alpha", "New folder")).toBe("New folder");
    expect(store.availableName("beta", "Docs")).toBe("Docs");
  });
});
