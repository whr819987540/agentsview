import { SessionEndMarksService } from "../api/generated/index";

/** The fields of a session that an end mark is compared with. */
export interface EndMarkSession {
  id: string;
  ended_at?: string | null;
  started_at?: string | null;
  created_at?: string;
}

function timeMs(at: string): number {
  const ms = Date.parse(at);
  return Number.isNaN(ms) ? -Infinity : ms;
}

/** The sessions a sidebar row stands for, the one it shows first. */
export function rowSessions<T extends EndMarkSession>(shown: T, group?: T[]): T[] {
  if (!group) return [shown];
  return [shown, ...group.filter((s) => s.id !== shown.id)];
}

/** The latest activity across sessions, as the sidebar orders them:
 *  each session's end, else its start, else its creation. */
export function lastActivity(sessions: EndMarkSession[]): string {
  let latest = "";
  let latestMs = -Infinity;
  for (const s of sessions) {
    const at = s.ended_at ?? s.started_at ?? s.created_at ?? "";
    const ms = timeMs(at);
    if (latest === "" || ms > latestMs) {
      latest = at;
      latestMs = ms;
    }
  }
  return latest;
}

/**
 * Sessions the user marked as ended. A mark keeps the activity the user saw
 * when marking, and stops counting once the session has newer activity, so
 * a resumed session no longer shows as ended.
 */
class SessionEndMarksStore {
  /** The activity time each marked session was marked at. */
  marks: Map<string, string> = $state(new Map());
  /** False on replicas and read-only archives: marks cannot change. */
  editable: boolean = $state(false);
  private loading: Promise<void> | null = null;
  private loaded = false;
  /** Bumped on every local change so an older list response cannot
   *  overwrite a newer edit. */
  private version = 0;

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
      const res = await SessionEndMarksService.getApiV1SessionEndMarks();
      this.loaded = true;
      if (this.version !== version) return;
      this.marks = new Map(res.marks.map((mark) => [mark.session_id, mark.activity_at]));
      this.editable = res.editable;
    } catch {
      // Keep the current marks; sessions still show their live status.
    }
  }

  /** Whether a row standing for these sessions shows as ended: one of
   *  them is marked, and none has activity after that mark. */
  isEnded(sessions: EndMarkSession[]): boolean {
    if (this.marks.size === 0) return false;
    const latest = timeMs(lastActivity(sessions));
    return sessions.some((s) => {
      const at = this.marks.get(s.id);
      return at !== undefined && timeMs(at) >= latest;
    });
  }

  /** Mark a row's sessions as ended. The mark goes on the first session,
   *  the one the row shows, and the server stamps it with the whole row's
   *  latest activity, including sessions this client has not loaded. */
  async mark(sessions: EndMarkSession[]): Promise<void> {
    const first = sessions[0];
    if (!first) return;
    await this.apply(
      (marks) => marks.set(first.id, lastActivity(sessions)),
      async (version) => {
        const mark = await SessionEndMarksService.putApiV1SessionsByIdEnded({ id: first.id });
        if (this.version === version) {
          this.marks = new Map(this.marks).set(mark.session_id, mark.activity_at);
        }
      },
    );
  }

  /** Remove every mark on a row's sessions. */
  async unmark(sessions: EndMarkSession[]): Promise<void> {
    const ids = sessions.map((s) => s.id).filter((id) => this.marks.has(id));
    if (ids.length === 0) return;
    await this.apply(
      (marks) => {
        for (const id of ids) marks.delete(id);
      },
      () =>
        Promise.all(ids.map((id) => SessionEndMarksService.deleteApiV1SessionsByIdEnded({ id }))),
    );
  }

  /** Show the change at once. A failed request reloads the server's
   *  marks before rethrowing. */
  private async apply(
    change: (marks: Map<string, string>) => void,
    request: (version: number) => Promise<unknown>,
  ): Promise<void> {
    const version = ++this.version;
    const next = new Map(this.marks);
    change(next);
    this.marks = next;
    try {
      await request(version);
    } catch (error) {
      if (this.version === version) {
        this.version++;
        await this.fetchList();
      }
      throw error;
    }
  }
}

export function createSessionEndMarksStore(): SessionEndMarksStore {
  return new SessionEndMarksStore();
}

export const sessionEndMarks = createSessionEndMarksStore();
