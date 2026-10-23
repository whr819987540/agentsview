import { SessionEndMarksService } from "../api/generated/index";

/** The fields of a session that an end mark is compared with. */
export interface EndMarkSession {
  id: string;
  ended_at?: string | null;
  started_at?: string | null;
  created_at?: string;
}

/** Delays before retrying a failed read of the marks. */
const RETRY_DELAYS_MS = [2_000, 4_000, 8_000];

function timeMs(at: string): number {
  const ms = Date.parse(at);
  return Number.isNaN(ms) ? -Infinity : ms;
}

/** A session's last activity, as the sidebar orders sessions: its end,
 *  else its start, else its creation. Empty times count as missing. */
function activityAt(s: EndMarkSession): string {
  return s.ended_at || s.started_at || s.created_at || "";
}

/** The sessions a sidebar row stands for, the one it shows first. */
export function rowSessions<T extends EndMarkSession>(shown: T, group?: T[]): T[] {
  if (!group) return [shown];
  return [shown, ...group.filter((s) => s.id !== shown.id)];
}

/** The latest activity across sessions. */
export function lastActivity(sessions: EndMarkSession[]): string {
  let latest = "";
  let latestMs = -Infinity;
  for (const s of sessions) {
    const at = activityAt(s);
    const ms = timeMs(at);
    if (latest === "" || ms > latestMs) {
      latest = at;
      latestMs = ms;
    }
  }
  return latest;
}

/**
 * Sidebar rows the user marked as ended. A mark holds its row's latest
 * activity when it was set, and stops counting once the row has newer
 * activity, so a resumed session no longer shows as ended.
 */
class SessionEndMarksStore {
  /** The activity time of each marked session: the server's marks with
   *  this client's unfinished changes applied. */
  marks: Map<string, string> = $state(new Map());
  /** False on read-only servers: marks cannot change. */
  editable: boolean = $state(false);
  /** Rows toggled from their sidebar button, by shown session, with
   *  whether each sorted as ended before. The sidebar keeps their place
   *  until the pointer and focus leave it, so another row's button does
   *  not slide under the pointer. */
  sortHolds: Map<string, boolean> = $state(new Map());
  /** The server's marks as last read or written. */
  private confirmed = new Map<string, string>();
  /** Each session's latest change whose request has not finished: the
   *  mark's activity time, or null to remove it. */
  private intents = new Map<string, string | null>();
  /** Each session's request chain, so its requests reach the server in
   *  the order the user made them. */
  private queues = new Map<string, Promise<void>>();
  /** Counts finished requests, so a read that overlapped one is not
   *  trusted: it may predate the change. */
  private writes = 0;
  private reading: Promise<void> | null = null;
  private readWanted = false;
  private loaded = false;
  private retries = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;

  /** Read the marks unless a read already succeeded. */
  load(): Promise<void> {
    if (this.loaded) return Promise.resolve();
    return this.refresh();
  }

  /** Read the marks again. A call during a read starts one more read
   *  after it, so the result reflects everything before the call. */
  refresh(): Promise<void> {
    this.readWanted = true;
    this.reading ??= this.read().finally(() => {
      this.reading = null;
    });
    return this.reading;
  }

  private async read(): Promise<void> {
    while (this.readWanted) {
      this.readWanted = false;
      const writes = this.writes;
      let res;
      try {
        res = await SessionEndMarksService.getApiV1SessionEndMarks();
      } catch {
        // Keep the current marks; sessions still show their live status.
        this.scheduleRetry();
        return;
      }
      this.loaded = true;
      this.retries = 0;
      this.editable = res.editable;
      // A request that finished meanwhile reads again when it is the last.
      if (this.writes !== writes) continue;
      this.confirmed = new Map(res.marks.map((mark) => [mark.session_id, mark.activity_at]));
      this.show();
    }
  }

  private scheduleRetry() {
    if (this.retryTimer !== null || this.retries >= RETRY_DELAYS_MS.length) return;
    const delay = RETRY_DELAYS_MS[this.retries++]!;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      void this.refresh();
    }, delay);
  }

  private show() {
    const next = new Map(this.confirmed);
    for (const [id, at] of this.intents) {
      if (at === null) next.delete(id);
      else next.set(id, at);
    }
    this.marks = next;
  }

  /** Whether a row standing for these sessions shows as ended: one of
   *  them is marked, and none has activity after the newest mark. */
  isEnded(sessions: EndMarkSession[]): boolean {
    if (this.marks.size === 0) return false;
    let markMs: number | null = null;
    for (const s of sessions) {
      const at = this.marks.get(s.id);
      if (at !== undefined) markMs = Math.max(markMs ?? -Infinity, timeMs(at));
    }
    if (markMs === null) return false;
    const latest = markMs;
    return sessions.every((s) => timeMs(activityAt(s)) <= latest);
  }

  /** Keep the row's place in the sidebar order until releaseSortHolds. */
  holdSortPlace(sessions: EndMarkSession[]) {
    const first = sessions[0];
    if (!first || this.sortHolds.has(first.id)) return;
    this.sortHolds = new Map(this.sortHolds).set(first.id, this.isEnded(sessions));
  }

  releaseSortHolds() {
    if (this.sortHolds.size > 0) this.sortHolds = new Map();
  }

  /** Whether the sidebar sorts a row as ended. A held row sorts as it did
   *  before the user toggled it. */
  sortsAsEnded(sessions: EndMarkSession[]): boolean {
    if (this.sortHolds.size > 0) {
      for (const s of sessions) {
        const held = this.sortHolds.get(s.id);
        if (held !== undefined) return held;
      }
    }
    return this.isEnded(sessions);
  }

  /** Mark the row if it is not ended, else remove its marks. */
  toggle(sessions: EndMarkSession[]): Promise<void> {
    return this.isEnded(sessions) ? this.unmark(sessions) : this.mark(sessions);
  }

  /** Mark a row's sessions as ended. The mark goes on the first session,
   *  the one the row shows, and the server stamps it with the whole row's
   *  latest activity, including sessions this client has not loaded. */
  mark(sessions: EndMarkSession[]): Promise<void> {
    const first = sessions[0];
    if (!first) return Promise.resolve();
    return this.change(first.id, lastActivity(sessions), async () => {
      const mark = await SessionEndMarksService.putApiV1SessionsByIdEnded({ id: first.id });
      this.confirmed.set(mark.session_id, mark.activity_at);
    });
  }

  /** Remove every mark on a row's sessions. */
  async unmark(sessions: EndMarkSession[]): Promise<void> {
    const ids = sessions.map((s) => s.id).filter((id) => this.marks.has(id));
    await Promise.all(
      ids.map((id) =>
        this.change(id, null, async () => {
          await SessionEndMarksService.deleteApiV1SessionsByIdEnded({ id });
          this.confirmed.delete(id);
        }),
      ),
    );
  }

  /**
   * Show a session's change at once and send its request after the
   * session's earlier ones. Once a session's last request finishes, it
   * shows the server's state, so a failed change does not linger. Once no
   * request is left, the marks are read again to catch any other drift.
   */
  private change(id: string, intent: string | null, request: () => Promise<void>): Promise<void> {
    this.intents.set(id, intent);
    this.show();
    const sent = (this.queues.get(id) ?? Promise.resolve()).then(request);
    const settled: Promise<void> = sent.then(
      () => this.finish(id, settled),
      () => this.finish(id, settled),
    );
    this.queues.set(id, settled);
    return sent;
  }

  private finish(id: string, settled: Promise<void>) {
    this.writes++;
    if (this.queues.get(id) === settled) {
      this.queues.delete(id);
      this.intents.delete(id);
    }
    this.show();
    if (this.queues.size === 0) void this.refresh();
  }
}

export function createSessionEndMarksStore(): SessionEndMarksStore {
  return new SessionEndMarksStore();
}

export const sessionEndMarks = createSessionEndMarksStore();
