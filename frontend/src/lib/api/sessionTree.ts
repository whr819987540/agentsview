import { SessionsService } from "./generated/index.js";
import type { SessionTreeResponse } from "./generated/index.js";

export async function fetchSessionTree(
  sessionId: string,
): Promise<SessionTreeResponse> {
  return SessionsService.getApiV1SessionsByIdTree({ id: sessionId });
}
