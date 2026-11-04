import { SessionEndMarksService } from "../api/generated/index";

/** The fields of a session that an end mark is compared with. */
export interface EndMarkSession {
  id: string;
  parent_session_id?: string | null;
  ended_at?: string | null;
  started_at?: string | null;
  created_at?: string;
}

/**
 * What an end mark covers. A row mark covers a whole sidebar row: the
 * session tree under its root. A branch mark covers one child row of that
 * tree, such as a fork: the session and its descendants. Each kind is read
 * and changed on its own, so marking a fork leaves its row unmarked.
 */
export type EndMarkKind = "row" | "branch";

/** A mark as the store holds it. */
export interface EndMark {
  /** The covered sessions' latest activity when the mark was set. */
  at: string;
  kind: EndMarkKind;
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

/** The sessions a branch mark on `shown` covers: the session first, then
 *  its descendants in `group` through parent_session_id, as the server
 *  walks them. */
export function branchSessions<T extends EndMarkSession>(shown: T, group?: T[]): T[] {
  if (!group) return [shown];
  const children = new Map<string, T[]>();
  for (const s of group) {
    const parent = s.parent_session_id;
    if (!parent || s.id === shown.id) continue;
    const list = children.get(parent);
    if (list) list.push(s);
    else children.set(parent, [s]);
  }
  const out = [shown];
  const seen = new Set([shown.id]);
  for (let i = 0; i < out.length; i++) {
    for (const child of children.get(out[i]!.id) ?? []) {
      if (seen.has(child.id)) continue;
      seen.add(child.id);
      out.push(child);
    }
  }
  return out;
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
 * Sidebar rows the user marked as ended. A mark holds its sessions' latest
 * activity when it was set, and stops counting once they have newer
 * activity, so a resumed session no longer shows as ended. A session holds
 * at most one mark, of either kind.
 */
class SessionEndMarksStore {
  /** The mark on each marked session: the server's marks with this
   *  client's unfinished changes applied. */
  marks: Map<string, EndMark> = $state(new Map());
  /** False on read-only servers: marks cannot change. */
  editable: boolean = $state(false);
  /** Rows toggled from their sidebar button, by shown session, with
   *  whether each sorted as ended before. The sidebar keeps their place
   *  until the pointer and focus leave it, so another row's button does
   *  not slide under the pointer. */
  sortHolds: Map<string, boolean> = $state(new Map());
  /** The server's marks as last read or written. */
  private confirmed = new Map<string, EndMark>();
  /** Each session's latest change whose request has not finished: the
   *  mark, or null to remove it. */
  private intents = new Map<string, EndMark | null>();
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
      let marks: Map<string, EndMark>;
      let editable: boolean;
      try {
        const res = await SessionEndMarksService.getApiV1SessionEndMarks();
        marks = new Map(
          res.marks.map((mark) => [
            mark.session_id,
            // A server from before kinds sends none; all its marks cover a row.
            { at: mark.activity_at, kind: mark.kind === "branch" ? "branch" : "row" },
          ]),
        );
        editable = res.editable;
      } catch {
        // A failed request or a malformed answer keeps the current marks;
        // sessions still show their live status.
        this.scheduleRetry();
        return;
      }
      this.loaded = true;
      this.retries = 0;
      this.editable = editable;
      // A request that finished meanwhile reads again when it is the last.
      if (this.writes !== writes) continue;
      this.confirmed = marks;
      this.show();
    }
  }

  /**
   * Read the marks again after the sidebar loaded these rows, when the
   * first read never succeeded or when a row or one of its branches has a
   * mark it has outlived. The server deletes an outlived mark; keeping it
   * here would bring the check back if the newer session left the row, such
   * as into the trash.
   */
  syncWithRows(rows: EndMarkSession[][]): void {
    if (!this.loaded) {
      void this.refresh();
      return;
    }
    if (this.marks.size === 0) return;
    const outlived = rows.some(
      (row) =>
        (row.some((s) => this.marks.get(s.id)?.kind === "row") && !this.isEnded(row)) ||
        row.some(
          (s) =>
            this.marks.get(s.id)?.kind === "branch" &&
            !this.isEnded(branchSessions(s, row), "branch"),
        ),
    );
    if (outlived) void this.refresh();
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
    for (const [id, mark] of this.intents) {
      if (mark === null) next.delete(id);
      else next.set(id, mark);
    }
    this.marks = next;
  }

  /**
   * Whether a row standing for these sessions shows as ended. A row is
   * ended when one of its sessions holds a row mark and none has activity
   * after the newest one. A branch, listed by branchSessions, is ended when
   * its first session holds a branch mark and none of its sessions has
   * activity after it. Marks of the other kind do not count.
   */
  isEnded(sessions: EndMarkSession[], kind: EndMarkKind = "row"): boolean {
    if (this.marks.size === 0) return false;
    let markMs: number | null = null;
    const candidates = kind === "branch" ? sessions.slice(0, 1) : sessions;
    for (const s of candidates) {
      const mark = this.marks.get(s.id);
      if (mark?.kind === kind) markMs = Math.max(markMs ?? -Infinity, timeMs(mark.at));
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

  /** Mark the row or branch if it is not ended, else remove its marks. */
  toggle(sessions: EndMarkSession[], kind: EndMarkKind = "row"): Promise<void> {
    return this.isEnded(sessions, kind) ? this.unmark(sessions, kind) : this.mark(sessions, kind);
  }

  /** Mark a row's or branch's sessions as ended. The mark goes on the first
   *  session, replacing a mark of the other kind there, and the server
   *  stamps it with the covered sessions' latest activity, including
   *  sessions this client has not loaded. */
  mark(sessions: EndMarkSession[], kind: EndMarkKind = "row"): Promise<void> {
    const first = sessions[0];
    if (!first) return Promise.resolve();
    return this.change(first.id, { at: lastActivity(sessions), kind }, async () => {
      const mark = await SessionEndMarksService.putApiV1SessionsByIdEnded(
        { id: first.id },
        { kind },
      );
      this.confirmed.set(mark.session_id, { at: mark.activity_at, kind });
    });
  }

  /** Remove a row's row marks, or a branch's mark on its first session.
   *  Marks of the other kind stay. */
  async unmark(sessions: EndMarkSession[], kind: EndMarkKind = "row"): Promise<void> {
    const candidates = kind === "branch" ? sessions.slice(0, 1) : sessions;
    const ids = candidates.map((s) => s.id).filter((id) => this.marks.get(id)?.kind === kind);
    await Promise.all(
      ids.map((id) =>
        this.change(id, null, async () => {
          await SessionEndMarksService.deleteApiV1SessionsByIdEnded({ id }, { kind });
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
  private change(id: string, intent: EndMark | null, request: () => Promise<void>): Promise<void> {
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
