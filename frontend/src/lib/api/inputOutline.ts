import { SessionsService } from "./generated/index.js";
import type { ServiceInputOutline } from "./generated/index.js";

export interface FetchSessionInputOutlineOptions {
  includeForkContext?: boolean;
  signal?: AbortSignal;
}

export async function fetchSessionInputOutline(
  sessionId: string,
  opts: FetchSessionInputOutlineOptions = {},
): Promise<ServiceInputOutline> {
  return SessionsService.getApiV1SessionsByIdInputOutline(
    { id: sessionId },
    opts.includeForkContext ? { include_fork_context: true } : undefined,
    { signal: opts.signal },
  );
}
