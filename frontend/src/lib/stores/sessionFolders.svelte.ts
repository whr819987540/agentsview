import { SessionFoldersService, type DbSessionFolder } from "../api/generated/index";
import { ApiError } from "../api/runtime.js";

export type SessionFolder = DbSessionFolder;

/** Thrown when the project already has a folder with the requested name. */
export class SessionFolderNameTakenError extends Error {}

const nameCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/**
 * Sidebar folders for each project. Folders change only how the sidebar
 * lists sessions; the archive keeps sessions where they are.
 */
class SessionFoldersStore {
  folders: SessionFolder[] = $state([]);
  /** False on replicas and read-only archives: folders cannot change. */
  editable: boolean = $state(false);
  private loading: Promise<void> | null = null;
  private loaded = false;
  /** Bumped on every local change so an older list response cannot
   *  overwrite a newer edit. */
  private version = 0;

  /** Folder ID of each filed session. */
  folderIdBySession: Map<string, number> = $derived.by(() => {
    const map = new Map<string, number>();
    for (const folder of this.folders) {
      for (const id of folder.session_ids) map.set(id, folder.id);
    }
    return map;
  });

  /** Folders of each project, sorted by name. */
  foldersByProject: Map<string, SessionFolder[]> = $derived.by(() => {
    const map = new Map<string, SessionFolder[]>();
    for (const folder of this.folders) {
      let list = map.get(folder.project);
      if (!list) {
        list = [];
        map.set(folder.project, list);
      }
      list.push(folder);
    }
    for (const list of map.values()) {
      list.sort((a, b) => nameCollator.compare(a.name, b.name) || a.id - b.id);
    }
    return map;
  });

  load(): Promise<void> {
    if (this.loaded) return Promise.resolve();
    return this.refresh();
  }

  refresh(): Promise<void> {
    if (!this.loading) {
      this.loading = this.fetchList().finally(() => {
        this.loading = null;
      });
    }
    return this.loading;
  }

  private async fetchList(): Promise<void> {
    const version = this.version;
    try {
      const res = await SessionFoldersService.getApiV1SessionFolders();
      this.loaded = true;
      if (this.version !== version) return;
      this.folders = res.folders;
      this.editable = res.editable;
    } catch {
      // Keep the current folders; the sidebar still lists every session.
    }
  }

  foldersFor(project: string): SessionFolder[] {
    return this.foldersByProject.get(project) ?? [];
  }

  /** The project folder holding the first filed session of ids, the way
   *  the sidebar places a session group. */
  folderIdForSessions(project: string, sessionIds: string[]): number | null {
    const projectFolders = new Set(this.foldersFor(project).map((f) => f.id));
    for (const id of sessionIds) {
      const folderId = this.folderIdBySession.get(id);
      if (folderId !== undefined && projectFolders.has(folderId)) return folderId;
    }
    return null;
  }

  /** A name not yet used in the project: base, "base 2", "base 3", ... */
  availableName(project: string, base: string): string {
    const taken = new Set(this.foldersFor(project).map((f) => f.name));
    if (!taken.has(base)) return base;
    let n = 2;
    while (taken.has(`${base} ${n}`)) n++;
    return `${base} ${n}`;
  }

  async create(project: string, name: string): Promise<SessionFolder> {
    const folder = await mapNameTaken(() =>
      SessionFoldersService.postApiV1SessionFolders({ project, name }),
    );
    this.version++;
    this.folders = [...this.folders, folder];
    return folder;
  }

  async rename(id: number, name: string): Promise<SessionFolder> {
    const folder = await mapNameTaken(() =>
      SessionFoldersService.patchApiV1SessionFoldersById({ id }, { name }),
    );
    this.version++;
    this.folders = this.folders.map((f) => (f.id === id ? folder : f));
    return folder;
  }

  async remove(id: number): Promise<void> {
    await SessionFoldersService.deleteApiV1SessionFoldersById({ id });
    this.version++;
    this.folders = this.folders.filter((f) => f.id !== id);
  }

  /** Move sessions into a folder, taking them out of any other folder. */
  async moveSessions(folderId: number, sessionIds: string[]): Promise<void> {
    if (sessionIds.length === 0) return;
    const moving = new Set(sessionIds);
    await this.applyMembership(
      (f) => {
        const kept = f.session_ids.filter((id) => !moving.has(id));
        return f.id === folderId ? [...kept, ...sessionIds] : kept;
      },
      () =>
        SessionFoldersService.putApiV1SessionFoldersByIdSessions(
          { id: folderId },
          { session_ids: sessionIds },
        ),
    );
  }

  /** Return sessions to their project's top level. */
  async removeSessions(sessionIds: string[]): Promise<void> {
    if (sessionIds.length === 0) return;
    const removing = new Set(sessionIds);
    await this.applyMembership(
      (f) => f.session_ids.filter((id) => !removing.has(id)),
      () =>
        SessionFoldersService.postApiV1SessionFoldersRemoveSessions({
          session_ids: sessionIds,
        }),
    );
  }

  /** Show the membership change at once, then adopt the server's list.
   *  A failed request reloads the server's list before rethrowing. */
  private async applyMembership(
    next: (folder: SessionFolder) => string[],
    request: () => Promise<{ folders: SessionFolder[]; editable: boolean }>,
  ): Promise<void> {
    const version = ++this.version;
    this.folders = this.folders.map((f) => ({ ...f, session_ids: next(f) }));
    try {
      const res = await request();
      if (this.version !== version) return;
      this.folders = res.folders;
      this.editable = res.editable;
    } catch (error) {
      if (this.version === version) {
        this.version++;
        await this.fetchList();
      }
      throw error;
    }
  }
}

async function mapNameTaken<T>(request: () => Promise<T>): Promise<T> {
  try {
    return await request();
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      throw new SessionFolderNameTakenError(error.message);
    }
    throw error;
  }
}

export function createSessionFoldersStore(): SessionFoldersStore {
  return new SessionFoldersStore();
}

export const sessionFolders = createSessionFoldersStore();
