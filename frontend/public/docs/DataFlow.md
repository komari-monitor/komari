# v3 Data Flow

## Node data

```text
Komari RPC / API
  -> init manager
  -> nodes store
  -> visible node computed state
  -> HomeView / InstanceDetail / components
```

Logged-out users receive public rendering only. Hidden nodes are filtered through `nodesStore.visibleNodes` unless `appStore.privateFeaturesAllowed` is true.

## Provider metadata

```text
Component
  -> useNodeProviderMetadata()
  -> AuthService permission check when geo is requested
  -> ProviderService
  -> SharedCache
  -> Geo lookup / provider resolver
```

Geo lookup is gated by `allowGeoLookup` and, when supplied, a typed permission key. Cache keys include the geo mode, so public metadata-only resolution cannot reuse private geo-enriched entries.

## History and prediction

```text
Component
  -> useNodeLoadStats() / useNodePingStats() / PingChart
  -> optional AuthService permission check
  -> HistoryService
  -> RequestManager
  -> RPC / REST fallback
  -> PredictionService
```

Disk prediction uses `LOAD_RECORD_MAX_COUNT` by default, verifies the `diskPrediction` permission before loading private history, and does not run for logged-out public cards. Ping history is public and shares capped request-manager-backed loads keyed by node, time range, and `maxCount`.

## Snapshot export

```text
SnapshotExportPanel
  -> AuthService permission check
  -> optional export secondary password
  -> Provider metadata lookup with snapshot export permission
  -> SnapshotService
  -> CSV helper / JSON download
```

## Request lifecycle

History requests are keyed by record type, node UUID, time range, and `maxCount`. The shared request manager deduplicates identical in-flight requests, enforces the global concurrency cap, applies timeout/retry policy, and exposes abort hooks used when shared load-history subscribers are released.
