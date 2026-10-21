// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import type { DbMessage as Message } from "../api/generated/index.js";
const api = vi.hoisted(() => ({ getSession: vi.fn(), getMessages: vi.fn() }));
vi.mock("../api/generated/index", () => ({
  SessionsService: {
    getApiV1SessionsById: vi.fn(({ id }: { id: string }) => api.getSession(id)),
    getApiV1SessionsByIdMessages: vi.fn(
      (path: { id: string }, params: PageParams, options?: { signal?: AbortSignal }) =>
        api.getMessages(path.id, params, options),
    ),
  },
}));
vi.mock("../api/runtime.js", () => ({
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      message: string,
    ) {
      super(message);
    }
  },
  isAbortError: (error: unknown) => error instanceof DOMException && error.name === "AbortError",
}));
vi.mock("./sessions.svelte.js", () => ({ sessions: { markActiveSessionMissing: vi.fn() } }));
vi.mock("./read-progress.svelte.js", () => ({
  buildReadProgressToken: () => null,
  readProgress: { get: () => null },
}));
import { ApiError } from "../api/runtime.js";
import { MessagesStore } from "./messages.svelte.js";

interface PageParams {
  from?: number;
  limit?: number;
  direction?: string;
  expected_revision?: string;
}

/** What the list showed when the store made a request. */
interface Shown {
  rows: number;
  first: number | undefined;
  last: number | undefined;
}

function row(sessionId: string, ordinal: number): Message {
  return {
    has_context_tokens: false,
    has_output_tokens: false,
    id: 50_000 + ordinal,
    ordinal,
    session_id: sessionId,
    role: ordinal % 2 === 0 ? "user" : "assistant",
    content: `row ${ordinal}`,
    content_length: `row ${ordinal}`.length,
    has_thinking: false,
    thinking_text: "",
    has_tool_use: false,
    is_system: false,
    model: "",
    context_tokens: 0,
    output_tokens: 0,
    timestamp: "2026-01-01T00:00:00Z",
  };
}

function ordinals(from: number, to: number): number[] {
  return Array.from({ length: to - from + 1 }, (_, i) => from + i);
}

/**
 * A session served the way the backend pages it: ascending reads start at
 * `from` or the first row, every page names the transcript revision, and a
 * read bound to an older revision is refused with 409. Every request also
 * records what the store's list shows at that moment, so a test can check
 * the list between the steps of a reload whichever requests the store makes.
 */
function serve(
  store: MessagesStore,
  rows: Message[],
  meta: { message_count: number; relationship_type?: string },
) {
  const state = { rows, revision: 1, meta };
  const shown: Shown[] = [];
  const servedOrdinals: number[] = [];
  const record = () =>
    shown.push({
      rows: store.messages.length,
      first: store.messages[0]?.ordinal,
      last: store.messages.at(-1)?.ordinal,
    });
  api.getSession.mockImplementation(async (id: string) => {
    record();
    return { id, ...state.meta, transcript_revision: String(state.revision) };
  });
  api.getMessages.mockImplementation(async (_id: string, params: PageParams = {}) => {
    record();
    if (params.expected_revision && params.expected_revision !== String(state.revision)) {
      throw new ApiError(409, "transcript revision does not match");
    }
    const limit = params.limit ?? 1000;
    const page = state.rows
      .filter((message) => params.from === undefined || message.ordinal >= params.from)
      .slice(0, limit);
    servedOrdinals.push(...page.map((message) => message.ordinal));
    return { messages: page, count: page.length, transcript_revision: String(state.revision) };
  });
  return {
    state,
    shown,
    servedOrdinals,
    /** Forget what earlier requests recorded. */
    reset() {
      shown.length = 0;
      servedOrdinals.length = 0;
    },
  };
}

const stores: MessagesStore[] = [];
function newStore() {
  const store = new MessagesStore();
  stores.push(store);
  return store;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  stores.splice(0).forEach((store) => store.clear());
  vi.restoreAllMocks();
});

describe("a reload of a transcript already on screen", () => {
  // A reload that starts over reads the transcript from its first page. Showing
  // each page as it lands would drop the list to 1,000 rows between pages, and
  // a reader below them would lose their place (see
  // e2e/transcript-reading-position.spec.ts).
  it("keeps a forked transcript whole while the fork's rows reload", async () => {
    const inherited = ordinals(-601, -2).map((ordinal) => row("parent", ordinal));
    const boundary = {
      ...row("fork", -1),
      role: "system",
      is_system: true,
      source_subtype: "fork_boundary",
    };
    const own = ordinals(0, 899).map((ordinal) => row("fork", ordinal));
    const store = newStore();
    const backend = serve(store, [...inherited, boundary, ...own], {
      message_count: 900,
      relationship_type: "fork",
    });
    await store.loadSession("fork");
    expect(store.messages).toHaveLength(1501);

    // One new message syncs, and the fork reloads.
    backend.state.rows = [...backend.state.rows, row("fork", 900)];
    backend.state.meta = { ...backend.state.meta, message_count: 901 };
    backend.state.revision++;
    backend.reset();
    await store.reload();

    expect(backend.shown.length, "requests the reload made").toBeGreaterThan(1);
    expect(
      backend.shown.filter((s) => s.rows < 1501 || s.first !== -601 || s.last! < 899),
      "requests that found the list cut short",
    ).toEqual([]);
    expect(store.messages.map((message) => message.ordinal)).toEqual(ordinals(-601, 900));
  });

  it("keeps the transcript whole when the session count trails its rows", async () => {
    const store = newStore();
    const backend = serve(
      store,
      ordinals(0, 1499).map((ordinal) => row("live", ordinal)),
      { message_count: 1500 },
    );
    await store.loadSession("live");
    expect(store.messages).toHaveLength(1500);

    // The metadata read sees one new message and the row read sees two,
    // because another sync landed between them.
    backend.state.rows = [...backend.state.rows, row("live", 1500), row("live", 1501)];
    backend.state.meta = { message_count: 1501 };
    backend.state.revision++;
    backend.reset();
    await store.reload();

    expect(backend.shown.length, "requests the reload made").toBeGreaterThan(1);
    expect(
      backend.shown.filter((s) => s.rows < 1500 || s.first !== 0 || s.last! < 1499),
      "requests that found the list cut short",
    ).toEqual([]);
    expect(store.messages.map((message) => message.ordinal)).toEqual(ordinals(0, 1501));
  });

  it("takes rows that run past the session count without reading them again", async () => {
    const store = newStore();
    const backend = serve(
      store,
      ordinals(0, 1499).map((ordinal) => row("live", ordinal)),
      { message_count: 1500 },
    );
    await store.loadSession("live");

    backend.state.rows = [...backend.state.rows, row("live", 1500), row("live", 1501)];
    backend.state.meta = { message_count: 1501 };
    backend.state.revision++;
    backend.reset();
    await store.reload();

    expect(store.messages.map((message) => message.ordinal)).toEqual(ordinals(0, 1501));
    expect(store.messageCount, "count taken from the rows").toBe(1502);
    // Starting over would read every row a second time.
    expect(backend.servedOrdinals.length, "rows the reload read").toBe(
      new Set(backend.servedOrdinals).size,
    );
  });
});
