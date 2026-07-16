/** Per-session reading-position memory.
 *
 * Positions are stored as an anchor (the ordinal of the first
 * display item visible in the viewport) plus the pixel offset
 * scrolled past that item's start. Anchors survive session
 * switches where raw scrollTop would not: item heights are
 * estimated until measured, and large sessions load
 * progressively, so absolute offsets shift between visits.
 */

export interface ScrollAnchor {
  /** Ordinal of the first display item visible in the viewport. */
  ordinal: number;
  /** Pixels scrolled past the anchor item's start (>= 0). */
  offsetPx: number;
}

const MAX_ENTRIES = 100;

export class ScrollMemory {
  #anchors = new Map<string, ScrollAnchor>();

  remember(sessionId: string, anchor: ScrollAnchor): void {
    // Re-insert to move the entry to the end of the Map's
    // insertion order, making eviction least-recently-updated.
    this.#anchors.delete(sessionId);
    this.#anchors.set(sessionId, anchor);
    if (this.#anchors.size > MAX_ENTRIES) {
      const oldest = this.#anchors.keys().next().value;
      if (oldest !== undefined) {
        this.#anchors.delete(oldest);
      }
    }
  }

  get(sessionId: string): ScrollAnchor | null {
    return this.#anchors.get(sessionId) ?? null;
  }

  forget(sessionId: string): void {
    this.#anchors.delete(sessionId);
  }

  clear(): void {
    this.#anchors.clear();
  }
}

/** Shared across MessageList mounts so positions survive
 *  session switches and route changes. */
export const scrollMemory = new ScrollMemory();

export interface VirtualItemLike {
  index: number;
  start: number;
  end: number;
}

/** First virtual item that is at least partially visible below
 *  the viewport top. Virtual items include overscanned rows
 *  above the viewport, which this skips. */
export function findFirstVisibleVirtualItem(
  virtualItems: readonly VirtualItemLike[],
  scrollTop: number,
): VirtualItemLike | null {
  for (const vi of virtualItems) {
    if (vi.end > scrollTop) return vi;
  }
  return null;
}

/** Resolve an anchor ordinal to an ascending display index.
 *
 * Returns the item containing the ordinal when present. When the
 * anchor is gone (trimmed content, hidden by filters), returns
 * the nearest following item so the viewport stays close to what
 * the user was reading; past-the-end anchors resolve to the last
 * item. Returns -1 only when there are no items.
 */
export function findAnchorIndexAsc(
  items: readonly { ordinals: number[] }[],
  ordinal: number,
): number {
  for (let i = 0; i < items.length; i++) {
    const ordinals = items[i]!.ordinals;
    const max = ordinals[ordinals.length - 1];
    if (max !== undefined && max >= ordinal) {
      return i;
    }
  }
  return items.length - 1;
}
