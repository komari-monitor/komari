export interface SharedCacheOptions<T> {
  maxSize: number
  ttl: number
  cleanupInterval?: number
  canEvict?: (value: T) => boolean
  onEvict?: (value: T, key: string) => void
}

interface SharedCacheRecord<T> {
  value: T
  createdAt: number
  expiresAt: number
  lastAccessedAt: number
  references: number
}

export class SharedCache<T> {
  private readonly entries = new Map<string, SharedCacheRecord<T>>()
  private readonly cleanupTimer: ReturnType<typeof setInterval> | null

  constructor(private readonly options: SharedCacheOptions<T>) {
    this.cleanupTimer = typeof window !== 'undefined' && options.cleanupInterval && options.cleanupInterval > 0
      ? window.setInterval(() => this.sweep(), options.cleanupInterval)
      : null
  }

  get size(): number {
    return this.entries.size
  }

  get(key: string): T | undefined {
    const entry = this.entries.get(key)
    if (!entry)
      return undefined

    const now = Date.now()
    if (entry.expiresAt <= now && this.canEvict(entry)) {
      this.evict(key, entry)
      return undefined
    }

    entry.lastAccessedAt = now
    return entry.value
  }

  set(key: string, value: T): T {
    const now = Date.now()
    const previous = this.entries.get(key)
    this.entries.set(key, {
      value,
      createdAt: previous?.createdAt ?? now,
      expiresAt: now + this.options.ttl,
      lastAccessedAt: now,
      references: previous?.references ?? 0,
    })
    this.sweep()
    return value
  }

  getOrCreate(key: string, createValue: () => T): T {
    const cached = this.get(key)
    if (cached !== undefined)
      return cached
    return this.set(key, createValue())
  }

  retain(key: string): () => void {
    const entry = this.entries.get(key)
    if (!entry)
      return () => {}

    entry.references += 1
    let released = false
    return () => {
      if (released)
        return
      released = true
      const current = this.entries.get(key)
      if (current)
        current.references = Math.max(0, current.references - 1)
      this.sweep()
    }
  }

  delete(key: string): void {
    const entry = this.entries.get(key)
    if (entry)
      this.evict(key, entry)
  }

  clear(): void {
    for (const [key, entry] of this.entries)
      this.evict(key, entry)
  }

  dispose(): void {
    if (this.cleanupTimer)
      window.clearInterval(this.cleanupTimer)
    this.clear()
  }

  sweep(): void {
    const now = Date.now()
    for (const [key, entry] of this.entries) {
      if (entry.expiresAt <= now && this.canEvict(entry))
        this.evict(key, entry)
    }

    if (this.entries.size <= this.options.maxSize)
      return

    const idleEntries = Array.from(this.entries.entries())
      .filter(([, entry]) => this.canEvict(entry))
      .sort((left, right) => left[1].lastAccessedAt - right[1].lastAccessedAt)

    for (const [key, entry] of idleEntries) {
      if (this.entries.size <= this.options.maxSize)
        break
      this.evict(key, entry)
    }
  }

  private canEvict(entry: SharedCacheRecord<T>): boolean {
    if (entry.references > 0)
      return false
    return this.options.canEvict ? this.options.canEvict(entry.value) : true
  }

  private evict(key: string, entry: SharedCacheRecord<T>): void {
    if (!this.entries.delete(key))
      return
    this.options.onEvict?.(entry.value, key)
  }
}

export class PromiseCache<T> {
  private readonly cache: SharedCache<Promise<T>>

  constructor(options: Omit<SharedCacheOptions<Promise<T>>, 'canEvict'>) {
    this.cache = new SharedCache({ ...options, canEvict: () => true })
  }

  getOrCreate(key: string, createPromise: () => Promise<T>): Promise<T> {
    const cached = this.cache.get(key)
    if (cached)
      return cached

    let sourcePromise: Promise<T>
    try {
      sourcePromise = createPromise()
    }
    catch (error) {
      this.cache.delete(key)
      throw error
    }

    const promise = sourcePromise
      .catch((error) => {
        this.cache.delete(key)
        throw error
      })
      .finally(() => {
        this.cache.delete(key)
      })
    this.cache.set(key, promise)
    return promise
  }

  clear(): void {
    this.cache.clear()
  }
}
