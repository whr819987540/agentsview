import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { SessionEndMarksService } from "../api/generated/index";
import { ApiError } from "../api/runtime.js";
import {
  createSessionEndMarksStore,
  lastActivity,
  rowSessions,
  type EndMarkSession,
} from "./sessionEndMarks.svelte.js";

vi.mock("../api/generated/index", () => ({
  SessionEndMarksService: {
    getApiV1SessionEndMarks: vi.fn(),
    putApiV1SessionsByIdEnded: vi.fn(),
    deleteApiV1SessionsByIdEnded: vi.fn(),
  },
}));

const service = SessionEndMarksService as unknown as Record<
  "getApiV1SessionEndMarks" | "putApiV1SessionsByIdEnded" | "deleteApiV1SessionsByIdEnded",
  ReturnType<typeof vi.fn>
>;

function session(
  id: string,
  endedAt: string | null,
  startedAt: string | null = null,
): EndMarkSession {
  return { id, ended_at: endedAt, started_at: startedAt, created_at: "2026-10-01T00:00:00Z" };
}

/**
 * A fake server. It applies a request only when the test lets it succeed,
 * and in the order the test lets requests through, so tests can fail
 * requests, reorder them, or take the server down.
 */
class FakeServer {
  marks = new Map<string, string>();
  /** The time the server stamps on new marks. */
  stampAt = "2026-10-01T10:00:00Z";
  down = false;
  /** When set, writes wait in `waiting` until the test releases them. */
  holdWrites = false;
  waiting: Array<{ key: string; ok: () => void; fail: (error: unknown) => void }> = [];
  log: string[] = [];

  list() {
    if (this.down) return Promise.reject(new TypeError("Failed to fetch"));
    return Promise.resolve({
      marks: [...this.marks].map(([session_id, activity_at]) => ({ session_id, activity_at })),
      editable: true,
    });
  }

  write<T>(key: string, apply: () => T): Promise<T> {
    this.log.push(`sent ${key}`);
    if (this.down) return Promise.reject(new TypeError("Failed to fetch"));
    if (!this.holdWrites) {
      this.log.push(`applied ${key}`);
      return Promise.resolve(apply());
    }
    return new Promise<T>((resolve, reject) => {
      this.waiting.push({
        key,
        ok: () => {
          this.log.push(`applied ${key}`);
          resolve(apply());
        },
        fail: reject,
      });
    });
  }

  release(key: string, outcome: "ok" | Error = "ok") {
    const index = this.waiting.findIndex((w) => w.key === key);
    if (index < 0) throw new Error(`no request ${key} is waiting`);
    const [w] = this.waiting.splice(index, 1);
    if (outcome === "ok") w!.ok();
    else w!.fail(outcome);
  }
}

let server: FakeServer;

/** Lets every pending promise callback run. */
async function settle() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

beforeEach(() => {
  vi.clearAllMocks();
  server = new FakeServer();
  service.getApiV1SessionEndMarks.mockImplementation(() => server.list());
  service.putApiV1SessionsByIdEnded.mockImplementation(({ id }: { id: string }) =>
    server.write(`put ${id}`, () => {
      server.marks.set(id, server.stampAt);
      return { session_id: id, activity_at: server.stampAt };
    }),
  );
  service.deleteApiV1SessionsByIdEnded.mockImplementation(({ id }: { id: string }) =>
    server.write(`delete ${id}`, () => {
      server.marks.delete(id);
    }),
  );
});

afterEach(() => {
  vi.useRealTimers();
});

describe("lastActivity", () => {
  it("takes the latest end, falling back to start and creation", () => {
    expect(
      lastActivity([
        session("a", "2026-10-01T10:00:00Z"),
        session("b", null, "2026-10-01T11:00:00Z"),
        session("c", null),
      ]),
    ).toBe("2026-10-01T11:00:00Z");
  });

  it("compares times, not strings", () => {
    // "00Z" sorts after "00.5Z" as text, but ".5Z" is the later instant.
    expect(
      lastActivity([session("a", "2026-10-01T10:00:00Z"), session("b", "2026-10-01T10:00:00.5Z")]),
    ).toBe("2026-10-01T10:00:00.5Z");
  });

  it("treats an empty end time as missing", () => {
    expect(lastActivity([session("a", "", "2026-10-01T09:00:00Z")])).toBe("2026-10-01T09:00:00Z");
  });
});

describe("rowSessions", () => {
  it("puts the shown session first and keeps the rest of the group", () => {
    const root = session("root", "2026-10-01T10:00:00Z");
    const cont = session("cont", "2026-10-01T11:00:00Z");
    expect(rowSessions(root, [cont, root]).map((s) => s.id)).toEqual(["root", "cont"]);
    expect(rowSessions(root).map((s) => s.id)).toEqual(["root"]);
  });
});

describe("SessionEndMarksStore", () => {
  let store: ReturnType<typeof createSessionEndMarksStore>;

  beforeEach(async () => {
    server.marks.set("done", "2026-10-01T10:00:00Z");
    store = createSessionEndMarksStore();
    await store.load();
  });

  it("shows a marked session as ended until it has newer activity", () => {
    expect(store.editable).toBe(true);
    expect(store.isEnded([session("done", "2026-10-01T10:00:00Z")])).toBe(true);
    expect(store.isEnded([session("done", "2026-10-01T10:00:00.001Z")])).toBe(false);
    expect(store.isEnded([session("other", "2026-10-01T09:00:00Z")])).toBe(false);
  });

  it("clears a row's mark when another session in the row is newer", () => {
    const root = session("done", "2026-10-01T10:00:00Z");
    expect(store.isEnded([root, session("subagent", "2026-10-01T09:59:00Z")])).toBe(true);
    expect(store.isEnded([root, session("continuation", "2026-10-01T10:05:00Z")])).toBe(false);
  });

  it("counts a mark on any session of the row", () => {
    const row = [session("root", "2026-10-01T09:00:00Z"), session("done", "2026-10-01T10:00:00Z")];
    expect(store.isEnded(row)).toBe(true);
  });

  it("marks the shown session and keeps the time the server stamped", async () => {
    // The server saw a continuation this client has not loaded.
    server.stampAt = "2026-10-03T08:00:00Z";
    const row = [session("root", "2026-10-01T09:00:00Z"), session("cont", "2026-10-02T08:00:00Z")];

    await store.mark(row);

    expect(service.putApiV1SessionsByIdEnded).toHaveBeenCalledWith({ id: "root" });
    expect(store.marks.get("root")).toBe("2026-10-03T08:00:00Z");
    expect(store.isEnded([...row, session("unloaded", "2026-10-03T08:00:00Z")])).toBe(true);
  });

  it("removes every mark on the row's sessions", async () => {
    server.marks.set("root", "2026-10-01T10:00:00Z");
    server.marks.set("cont", "2026-10-01T10:00:00Z");
    await store.refresh();
    const row = [
      session("root", "2026-10-01T09:00:00Z"),
      session("cont", "2026-10-01T10:00:00Z"),
      session("sub", null),
    ];

    await store.toggle(row);

    expect(service.deleteApiV1SessionsByIdEnded.mock.calls.map(([arg]) => arg)).toEqual([
      { id: "root" },
      { id: "cont" },
    ]);
    expect(store.isEnded(row)).toBe(false);
  });

  it("shows a change at once, before the server answers", async () => {
    server.holdWrites = true;
    const row = [session("open", "2026-10-01T10:00:00Z")];

    const marking = store.mark(row);

    expect(store.isEnded(row)).toBe(true);
    await settle();
    server.release("put open");
    await marking;
    expect(store.isEnded(row)).toBe(true);
  });

  it("drops a failed mark even when another mark is still in flight", async () => {
    server.holdWrites = true;
    const a = [session("a", "2026-10-01T10:00:00Z")];
    const b = [session("b", "2026-10-01T10:00:00Z")];
    const markingA = store.mark(a).catch((error: unknown) => error);
    const markingB = store.mark(b);
    await settle();

    server.release("put a", new ApiError(404, "session not found"));
    expect(await markingA).toBeInstanceOf(ApiError);
    expect(store.isEnded(a)).toBe(false);
    expect(store.isEnded(b)).toBe(true);

    server.release("put b");
    await markingB;
    await settle();
    expect([...store.marks.keys()].sort()).toEqual([...server.marks.keys()].sort());
  });

  it("drops a failed mark while the server is unreachable", async () => {
    server.down = true;
    const row = [session("open", "2026-10-01T10:00:00Z")];

    await expect(store.mark(row)).rejects.toThrow("Failed to fetch");
    await settle();

    expect(store.isEnded(row)).toBe(false);
    expect(store.isEnded([session("done", "2026-10-01T10:00:00Z")])).toBe(true);
  });

  it("reads the marks again after the last change, catching drift", async () => {
    server.holdWrites = true;
    const row = [session("open", "2026-10-01T10:00:00Z")];
    const marking = store.mark(row);
    // Another tab removes a mark meanwhile.
    server.marks.delete("done");
    await settle();
    server.release("put open");
    await marking;
    await settle();

    expect(service.getApiV1SessionEndMarks).toHaveBeenCalledTimes(2);
    expect([...store.marks.keys()]).toEqual(["open"]);
  });

  it("sends a session's requests in the order the user made them", async () => {
    server.holdWrites = true;
    const row = [session("open", "2026-10-01T10:00:00Z")];

    const marking = store.toggle(row);
    const unmarking = store.toggle(row);
    expect(store.isEnded(row)).toBe(false);
    await settle();
    expect(server.log).toEqual(["sent put open"]);

    server.release("put open");
    await marking;
    await settle();
    expect(server.log).toEqual(["sent put open", "applied put open", "sent delete open"]);
    expect(store.isEnded(row)).toBe(false);

    server.release("delete open");
    await unmarking;
    await settle();
    expect(server.marks.has("open")).toBe(false);
    expect(store.isEnded(row)).toBe(false);
  });

  it("does not let a read that overlapped a change undo it", async () => {
    let answerStaleRead!: () => void;
    service.getApiV1SessionEndMarks.mockImplementationOnce(() => {
      const before = server.list();
      return new Promise((resolve) => {
        answerStaleRead = () => resolve(before);
      });
    });
    const refreshing = store.refresh();
    const row = [session("open", "2026-10-01T10:00:00Z")];

    await store.mark(row);
    answerStaleRead();
    await refreshing;

    expect(store.isEnded(row)).toBe(true);
    expect(server.marks.has("open")).toBe(true);
  });
});

describe("SessionEndMarksStore loading", () => {
  it("retries a failed first read", async () => {
    vi.useFakeTimers();
    server.down = true;
    server.marks.set("done", "2026-10-01T10:00:00Z");
    const store = createSessionEndMarksStore();

    await store.load();
    expect(store.editable).toBe(false);

    server.down = false;
    await vi.advanceTimersByTimeAsync(2_000);

    expect(store.editable).toBe(true);
    expect(store.isEnded([session("done", "2026-10-01T10:00:00Z")])).toBe(true);
  });

  it("reads again when asked, so marks the server dropped disappear", async () => {
    server.marks.set("done", "2026-10-01T10:00:00Z");
    const store = createSessionEndMarksStore();
    await store.load();

    server.marks.clear();
    await store.refresh();

    expect(store.marks.size).toBe(0);
  });
});
