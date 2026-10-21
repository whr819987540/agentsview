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
 * read bound to an older revision is refused with 409.
 */
function serve(rows: Message[], meta: { message_count: number; relationship_type?: string }) {
  const state = { rows, revision: 1, meta };
  let hold: {
    matches: (params: PageParams) => boolean;
    arrived: () => void;
    gate: Promise<void>;
  } | null = null;
  api.getSession.mockImplementation(async (id: string) => ({
    id,
    ...state.meta,
    transcript_revision: String(state.revision),
  }));
  api.getMessages.mockImplementation(async (_id: string, params: PageParams = {}) => {
    if (hold?.matches(params)) {
      const current = hold;
      hold = null;
      current.arrived();
      await current.gate;
    }
    if (params.expected_revision && params.expected_revision !== String(state.revision)) {
      throw new ApiError(409, "transcript revision does not match");
    }
    const limit = params.limit ?? 1000;
    const page = state.rows
      .filter((message) => params.from === undefined || message.ordinal >= params.from)
      .slice(0, limit);
    return { messages: page, count: page.length, transcript_revision: String(state.revision) };
  });
  return {
    state,
    /** Hold the next page request that matches until release is called. */
    holdNext(matches: (params: PageParams) => boolean) {
      let arrived!: () => void;
      const arrival = new Promise<void>((resolve) => (arrived = resolve));
      let release!: () => void;
      const gate = new Promise<void>((resolve) => (release = resolve));
      hold = { matches, arrived, gate };
      return { arrived: arrival, release };
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

describe("a reload that has to start over", () => {
  // The fallback reloads from the first page and publishes each page as it
  // lands, so the list drops to 1,000 rows between pages and a reader below
  // them loses their place (see e2e/transcript-reading-position.spec.ts).
  it.fails("keeps a forked transcript whole while the fork's rows reload", async () => {
    const inherited = ordinals(-601, -2).map((ordinal) => row("parent", ordinal));
    const boundary = {
      ...row("fork", -1),
      role: "system",
      is_system: true,
      source_subtype: "fork_boundary",
    };
    const own = ordinals(0, 899).map((ordinal) => row("fork", ordinal));
    const backend = serve([...inherited, boundary, ...own], {
      message_count: 900,
      relationship_type: "fork",
    });
    const store = newStore();
    await store.loadSession("fork");
    expect(store.messages).toHaveLength(1501);

    // One new message syncs; every fork reload starts from the first page.
    backend.state.rows = [...backend.state.rows, row("fork", 900)];
    backend.state.meta = { ...backend.state.meta, message_count: 901 };
    backend.state.revision++;
    const secondPage = backend.holdNext((params) => params.from === 399);
    const reload = store.reload();
    await secondPage.arrived;

    expect(store.messages.length, "rows on screen between pages").toBeGreaterThanOrEqual(1501);

    secondPage.release();
    await reload;
    expect(store.messages.map((message) => message.ordinal)).toEqual(ordinals(-601, 900));
  });

  it.fails("keeps the transcript whole when the session count trails its rows", async () => {
    const backend = serve(
      ordinals(0, 1499).map((ordinal) => row("live", ordinal)),
      { message_count: 1500 },
    );
    const store = newStore();
    await store.loadSession("live");
    expect(store.messages).toHaveLength(1500);

    // The metadata read sees one new message and the row read sees two,
    // because another sync landed between them. The refreshed window then
    // ends past the count, and the store falls back to a full reload.
    backend.state.rows = [...backend.state.rows, row("live", 1500), row("live", 1501)];
    backend.state.meta = { message_count: 1501 };
    backend.state.revision++;
    let fromThousand = 0;
    const fullReloadSecondPage = backend.holdNext(
      (params) => params.from === 1000 && ++fromThousand === 2,
    );
    const reload = store.reload();
    await fullReloadSecondPage.arrived;

    expect(store.messages.length, "rows on screen between pages").toBeGreaterThanOrEqual(1500);

    fullReloadSecondPage.release();
    await reload;
    expect(store.messages.map((message) => message.ordinal)).toEqual(ordinals(0, 1501));
  });
});
