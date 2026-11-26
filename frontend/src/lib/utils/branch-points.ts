import type {
  ServiceBranchLeave as BranchLeave,
  ServiceBranchPoint as BranchPoint,
} from "../api/generated/index.js";
import { m } from "../i18n/index.js";
import type { DisplayItem } from "./display-items.js";
import { formatNumber } from "./format.js";

/** Branch points shown with one transcript row. */
export interface RowBranchMarkers {
  /** Points that leave the transcript before the row. */
  before: BranchPoint[];
  /** Points that leave the transcript after the row. */
  after: BranchPoint[];
}

/**
 * Attaches each branch point to the transcript row it follows: the last row
 * of the session's own messages that starts at or before the point's
 * message. When rows hide that message (a filtered system row, say), the
 * point stays with the row before it. A point that comes before every own
 * row attaches before the first one. Inherited rows, which have negative
 * ordinals, never carry points.
 *
 * Returns the markers by ascending display index.
 */
export function attachBranchPoints(
  items: readonly DisplayItem[],
  points: readonly BranchPoint[],
): Map<number, RowBranchMarkers> {
  const own: { index: number; start: number }[] = [];
  items.forEach((item, index) => {
    const ownOrdinals = item.ordinals.filter((o) => o >= 0);
    if (ownOrdinals.length > 0) {
      own.push({ index, start: Math.min(...ownOrdinals) });
    }
  });
  const out = new Map<number, RowBranchMarkers>();
  if (own.length === 0) return out;
  const markersAt = (index: number) => {
    let markers = out.get(index);
    if (!markers) {
      markers = { before: [], after: [] };
      out.set(index, markers);
    }
    return markers;
  };
  for (const point of points) {
    let row = -1;
    for (const candidate of own) {
      if (candidate.start > point.after_ordinal) break;
      row = candidate.index;
    }
    if (row < 0) {
      markersAt(own[0]!.index).before.push(point);
    } else {
      markersAt(row).after.push(point);
    }
  }
  return out;
}

/** The title a branch session shows: its name, else its first prompt. */
export function branchSessionTitle(
  session: { display_name?: string; first_message: string | null },
  untitled: string,
): string {
  const title = (session.display_name || session.first_message || "").trim();
  return title || untitled;
}

/** Says which parent turn a fork or continuation leaves from. */
export function leaveLabel(leave: BranchLeave): string {
  const params = {
    title: branchSessionTitle(leave.parent, m.session_tree_untitled()),
    turn: formatNumber(leave.after_turn),
  };
  return leave.relationship_type === "fork"
    ? m.branch_leave_fork(params)
    : m.branch_leave_continuation(params);
}
