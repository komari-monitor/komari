# v3 Cache Lifecycle

## Shared cache service

`services/cache.service.ts` provides:

- TTL expiry
- LRU-style eviction by last access time
- Reference counting
- Auto cleanup interval
- Eviction callbacks
- Promise cache helper for in-flight deduplication

## Provider metadata cache

`useNodeProviderMetadata()` uses `SharedCache` with `CACHE_CONFIG.providerMetadata`:

- max size: 1000 entries
- TTL: 24 hours
- cleanup interval: 5 minutes
- active subscribers and in-flight lookups are not evicted

The cache key includes whether geo lookup is allowed, so public metadata-only resolution cannot reuse private geo-enriched entries. Geo-enriched lookups can also require a typed permission key before they start.

## Load history cache

`useNodeLoadStats()` keys shared history by both time range and max-count. This prevents capped disk-prediction data from being confused with uncapped chart data.

Shared load-history entries keep subscriber counts. When the last subscriber releases an entry, refresh timers are stopped and related in-flight history requests are aborted through `RequestManager`.

## Request deduplication

`history.service.ts` routes load/ping history through `requestManager` with keys that include:

- record type
- node UUID or aggregate scope
- time range
- `maxCount`

This deduplicates identical in-flight requests and keeps retry/timeout/concurrency policy centralized.
