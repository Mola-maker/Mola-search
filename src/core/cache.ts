/** 简单 TTL + LRU 缓存：相同搜索参数短路，节省免费额度（2000 次/月） */
export class TtlLruCache<T> {
  private map = new Map<string, { value: T; expireAt: number }>();

  constructor(
    private readonly maxEntries = 256,
    private readonly ttlMs = 10 * 60 * 1000,
  ) {}

  private isFresh(entry: { expireAt: number }): boolean {
    return entry.expireAt > Date.now();
  }

  get(key: string): T | undefined {
    const entry = this.map.get(key);
    if (!entry) return undefined;
    if (!this.isFresh(entry)) {
      this.map.delete(key);
      return undefined;
    }
    // LRU touch
    this.map.delete(key);
    this.map.set(key, entry);
    return entry.value;
  }

  set(key: string, value: T): void {
    this.map.delete(key);
    if (this.map.size >= this.maxEntries) {
      const oldest = this.map.keys().next().value;
      if (oldest !== undefined) this.map.delete(oldest);
    }
    this.map.set(key, { value, expireAt: Date.now() + this.ttlMs });
  }

  get size(): number {
    return this.map.size;
  }
}
