import { beforeEach, describe, expect, it, vi } from "vite-plus/test";
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

function mark(sessionId: string, activityAt: string) {
  return { session_id: sessionId, activity_at: activityAt, created_at: "2026-10-01T12:00:00Z" };
}

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
    vi.clearAllMocks();
    service.getApiV1SessionEndMarks.mockResolvedValue({
      marks: [mark("done", "2026-10-01T10:00:00Z")],
      editable: true,
    });
    service.putApiV1SessionsByIdEnded.mockResolvedValue(undefined);
    service.deleteApiV1SessionsByIdEnded.mockResolvedValue(undefined);
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

  it("marks the shown session as of the row's latest activity", async () => {
    const row = [session("root", "2026-10-01T09:00:00Z"), session("cont", "2026-10-02T08:00:00Z")];

    await store.mark(row);

    expect(service.putApiV1SessionsByIdEnded).toHaveBeenCalledWith(
      { id: "root" },
      { activity_at: "2026-10-02T08:00:00Z" },
    );
    expect(store.isEnded(row)).toBe(true);
  });

  it("removes every mark on the row's sessions", async () => {
    service.getApiV1SessionEndMarks.mockResolvedValue({
      marks: [mark("root", "2026-10-01T10:00:00Z"), mark("cont", "2026-10-01T10:00:00Z")],
      editable: true,
    });
    await store.refresh();
    const row = [
      session("root", "2026-10-01T09:00:00Z"),
      session("cont", "2026-10-01T10:00:00Z"),
      session("sub", null),
    ];

    await store.unmark(row);

    expect(service.deleteApiV1SessionsByIdEnded.mock.calls.map(([arg]) => arg)).toEqual([
      { id: "root" },
      { id: "cont" },
    ]);
    expect(store.isEnded(row)).toBe(false);
  });

  it("restores the server's marks when a change fails", async () => {
    service.putApiV1SessionsByIdEnded.mockRejectedValue(new ApiError(500, "boom"));
    const row = [session("open", "2026-10-01T10:00:00Z")];

    await expect(store.mark(row)).rejects.toThrow("boom");

    expect(service.getApiV1SessionEndMarks).toHaveBeenCalledTimes(2);
    expect(store.isEnded(row)).toBe(false);
    expect(store.isEnded([session("done", "2026-10-01T10:00:00Z")])).toBe(true);
  });

  it("does not let an older list response undo a newer mark", async () => {
    let resolveList!: (value: unknown) => void;
    service.getApiV1SessionEndMarks.mockReturnValue(
      new Promise((resolve) => {
        resolveList = resolve;
      }),
    );
    const refreshing = store.refresh();
    const row = [session("open", "2026-10-01T10:00:00Z")];

    await store.mark(row);
    resolveList({ marks: [], editable: true });
    await refreshing;

    expect(store.isEnded(row)).toBe(true);
  });
});
