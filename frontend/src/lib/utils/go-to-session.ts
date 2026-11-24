export type SessionId = string;

export type SessionIdResolution =
  | { kind: "resolved"; id: SessionId }
  | { kind: "empty" | "unknown" | "ambiguous" | "capped" };

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function resolveSessionId(
  input: string,
  candidates: readonly string[],
  capped: boolean,
): SessionIdResolution {
  const value = input.trim();
  if (!value) return { kind: "empty" };
  if (capped) return { kind: "capped" };

  const normalizedUuid = UUID_PATTERN.test(value) ? value.toLowerCase() : null;
  const matches = [
    ...new Set(
      candidates.filter((candidate) => {
        if (candidate === value) return true;
        if (!normalizedUuid) return false;
        const normalizedCandidate = candidate.toLowerCase();
        return (
          normalizedCandidate === normalizedUuid ||
          normalizedCandidate.endsWith(`:${normalizedUuid}`) ||
          normalizedCandidate.endsWith(`~${normalizedUuid}`)
        );
      }),
    ),
  ];

  if (matches.length === 1) return { kind: "resolved", id: matches[0]! };
  if (matches.length > 1) return { kind: "ambiguous" };
  return { kind: "unknown" };
}

export function sessionLookupPartial(input: string): string {
  const value = input.trim();
  return UUID_PATTERN.test(value) ? value.toLowerCase() : value;
}

// Session IDs are single tokens built from letters, digits, and the
// separators agents and hosts use (`codex:`, `host~`, dashes, dots, and
// underscores). Requiring a digit keeps ordinary words such as "refactor"
// from triggering an ID lookup, and six characters keeps short fragments from
// matching unrelated IDs; the palette shows eight-character short IDs.
const SESSION_ID_QUERY_PATTERN = /^[A-Za-z0-9._:~-]{6,}$/;

export function isSessionIdQuery(input: string): boolean {
  const value = input.trim();
  return SESSION_ID_QUERY_PATTERN.test(value) && /\d/.test(value);
}

// sessionIdsParam encodes IDs for the list endpoint's `ids` parameter. Plain
// IDs are comma-joined. When any ID contains a comma or line break, every ID
// is quoted with RFC 4180 rules, which the server parses as CSV. The server
// rejects CRLF, so IDs containing it are dropped.
export function sessionIdsParam(ids: readonly string[]): string {
  const sendable = ids.filter((id) => !id.includes("\r\n"));
  if (!sendable.some((id) => /[,\r\n]/.test(id))) return sendable.join(",");
  return sendable.map((id) => `"${id.replaceAll('"', '""')}"`).join(",");
}
