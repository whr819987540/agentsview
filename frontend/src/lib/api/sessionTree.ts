import { ApiError, authHeaders, getBase } from "./runtime.js";
import type { SessionTreeResponse } from "./types/core.js";

export async function fetchSessionTree(
  sessionId: string,
): Promise<SessionTreeResponse> {
  const res = await fetch(
    `${getBase()}/sessions/${encodeURIComponent(sessionId)}/tree`,
    authHeaders(),
  );
  if (!res.ok) {
    const body = await res.text();
    throw new ApiError(
      res.status,
      body.trim() || `session tree ${res.status}`,
    );
  }
  return (await res.json()) as SessionTreeResponse;
}
