import type { DisplayItem } from "./display-items.js";

/**
 * Inherited parent messages in a fork's transcript have ordinals below -1;
 * the synthetic fork boundary row is -1.
 */
export function isInheritedOrdinal(ordinal: number): boolean {
  return ordinal < -1;
}

/** Counts the inherited messages the rows hold. */
export function countInheritedMessages(items: readonly DisplayItem[]): number {
  let count = 0;
  for (const item of items) {
    count += item.ordinals.filter(isInheritedOrdinal).length;
  }
  return count;
}

/**
 * Drops rows that hold only inherited messages while the inherited context
 * is collapsed. The same array comes back when nothing is hidden.
 */
export function hideInheritedRows(items: DisplayItem[], collapsed: boolean): DisplayItem[] {
  if (!collapsed) return items;
  const kept = items.filter((item) => item.ordinals.some((o) => !isInheritedOrdinal(o)));
  return kept.length === items.length ? items : kept;
}
