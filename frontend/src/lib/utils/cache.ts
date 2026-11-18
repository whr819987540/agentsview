/** Upper bound on the total size of a cache's entries. */
export interface LRUCacheBudget<K, V> {
  /** The most the summed entry sizes may reach. */
  maxSize: number;
  /** Size of one entry, in the unit of maxSize. */
  sizeOf: (key: K, value: V) => number;
}

interface Entry<K, V> {
  // The key as stored. A lookup can pass an equal string that is a view
  // into a larger one; reinserting under that would keep the larger alive.
  key: K;
  value: V;
  size: number;
}

/**
 * Map-based LRU cache. Exploits the insertion-order guarantee
 * of ES2015 Map: the least-recently-used entry is always first.
 * An optional budget also evicts by total size, and an entry larger
 * than the whole budget is not kept.
 */
export class LRUCache<K, V> {
  private map = new Map<K, Entry<K, V>>();
  private totalSize = 0;

  constructor(
    private capacity: number,
    private budget?: LRUCacheBudget<K, V>,
  ) {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new Error(`LRUCache capacity must be a positive integer, got ${capacity}`);
    }
    if (budget && !(budget.maxSize > 0)) {
      throw new Error(`LRUCache budget must be positive, got ${budget.maxSize}`);
    }
  }

  get(key: K): V | undefined {
    const entry = this.map.get(key);
    if (entry === undefined) return undefined;
    // Delete and re-insert to mark as most recently used
    this.map.delete(key);
    this.map.set(entry.key, entry);
    return entry.value;
  }

  set(key: K, value: V): void {
    const size = this.budget ? this.budget.sizeOf(key, value) : 0;
    this.remove(key);
    if (this.budget && size > this.budget.maxSize) return;
    if (this.map.size >= this.capacity) this.removeOldest();
    while (this.budget && this.map.size > 0 && this.totalSize + size > this.budget.maxSize) {
      this.removeOldest();
    }
    this.map.set(key, { key, value, size });
    this.totalSize += size;
  }

  clear(): void {
    this.map.clear();
    this.totalSize = 0;
  }

  get size(): number {
    return this.map.size;
  }

  /** Summed entry sizes under a budget; 0 without one. */
  get weight(): number {
    return this.totalSize;
  }

  private removeOldest() {
    const oldest = this.map.keys().next();
    if (!oldest.done) this.remove(oldest.value);
  }

  private remove(key: K) {
    const entry = this.map.get(key);
    if (entry === undefined) return;
    this.map.delete(key);
    this.totalSize -= entry.size;
  }
}

/**
 * A copy of text that does not share memory with the string it came from.
 * V8 represents a substring as a view into its parent, so a cache key cut
 * from a message would keep the whole message alive after the transcript
 * drops it. Concatenating and slicing again leaves a view into a fresh,
 * same-sized string instead.
 */
export function detachedString(text: string): string {
  return (" " + text).slice(1);
}
