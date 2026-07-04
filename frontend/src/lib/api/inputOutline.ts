import type { InputOutlineResponse } from "./types/core.js";
import {
  ApiError,
  authHeaders,
  getBase,
  responseErrorMessage,
} from "./runtime.js";

export interface FetchSessionInputOutlineOptions {
  includeForkContext?: boolean;
  signal?: AbortSignal;
}

export async function fetchSessionInputOutline(
  sessionId: string,
  opts: FetchSessionInputOutlineOptions = {},
): Promise<InputOutlineResponse> {
  const params = new URLSearchParams();
  if (opts.includeForkContext) {
    params.set("include_fork_context", "true");
  }
  const encoded = params.toString();
  const query = encoded ? `?${encoded}` : "";
  const res = await fetch(
    `${getBase()}/sessions/${encodeURIComponent(sessionId)}/input-outline${query}`,
    authHeaders({ signal: opts.signal }),
  );
  if (!res.ok) {
    throw new ApiError(
      res.status,
      await responseErrorMessage(res),
    );
  }
  return (await res.json()) as InputOutlineResponse;
}
