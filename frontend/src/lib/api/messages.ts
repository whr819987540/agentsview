import type { MessagesResponse } from "./types/core.js";
import {
  ApiError,
  authHeaders,
  getBase,
  responseErrorMessage,
} from "./runtime.js";

export interface FetchSessionMessagesOptions {
  from?: number;
  limit?: number;
  direction?: "asc" | "desc";
  includeForkContext?: boolean;
  signal?: AbortSignal;
}

export async function fetchSessionMessages(
  sessionId: string,
  opts: FetchSessionMessagesOptions = {},
): Promise<MessagesResponse> {
  const params = new URLSearchParams();
  if (opts.from !== undefined) {
    params.set("from", String(opts.from));
  }
  if (opts.limit !== undefined) {
    params.set("limit", String(opts.limit));
  }
  if (opts.direction) {
    params.set("direction", opts.direction);
  }
  if (opts.includeForkContext) {
    params.set("include_fork_context", "true");
  }

  const encoded = params.toString();
  const query = encoded ? `?${encoded}` : "";
  const res = await fetch(
    `${getBase()}/sessions/${encodeURIComponent(sessionId)}/messages${query}`,
    authHeaders({ signal: opts.signal }),
  );
  if (!res.ok) {
    throw new ApiError(
      res.status,
      await responseErrorMessage(res),
    );
  }
  return (await res.json()) as MessagesResponse;
}
