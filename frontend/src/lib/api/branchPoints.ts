import { SessionsService } from "./generated/index.js";
import type { ServiceBranchPoints } from "./generated/index.js";

/** Fetches where conversations branch off a session's transcript. */
export async function fetchBranchPoints(
  sessionId: string,
  signal?: AbortSignal,
): Promise<ServiceBranchPoints> {
  return SessionsService.getApiV1SessionsByIdBranchPoints(
    { id: sessionId },
    { signal },
  );
}
