/** Upper bound on the total size of a cache's entries. */
export interface LRUCacheBudget<K, V> {
  /** The most the summed entry sizes may reach. */
  maxSize: number;
  /** Size of one entry, in the unit of maxSize. */
  sizeOf: (key: K, value: V) => number;
}

/**
 * Map-based LRU cache. Exploits the insertion-order guarantee
 * of ES2015 Map: the least-recently-used entry is always first.
 * An optional budget also evicts by total size, and an entry larger
 * than the whole budget is not kept.
 */
export class LRUCache<K, V> {
  private map = new Map<K, V>();
  private sizes = new Map<K, number>();
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
    if (!this.map.has(key)) return undefined;
    const value = this.map.get(key)!;
    // Delete and re-insert to mark as most recently used
    this.map.delete(key);
    this.map.set(key, value);
    return value;
  }

  set(key: K, value: V): void {
    if (this.map.has(key)) {
      this.remove(key);
    } else if (this.map.size >= this.capacity) {
      this.remove(this.map.keys().next().value!);
    }
    if (!this.budget) {
      this.map.set(key, value);
      return;
    }
    const size = this.budget.sizeOf(key, value);
    if (size > this.budget.maxSize) return;
    while (this.map.size > 0 && this.totalSize + size > this.budget.maxSize) {
      this.remove(this.map.keys().next().value!);
    }
    this.map.set(key, value);
    this.sizes.set(key, size);
    this.totalSize += size;
  }

  clear(): void {
    this.map.clear();
    this.sizes.clear();
    this.totalSize = 0;
  }

  get size(): number {
    return this.map.size;
  }

  /** Summed entry sizes under a budget; 0 without one. */
  get weight(): number {
    return this.totalSize;
  }

  private remove(key: K) {
    this.map.delete(key);
    const size = this.sizes.get(key);
    if (size === undefined) return;
    this.sizes.delete(key);
    this.totalSize -= size;
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
