# v3 Architecture

Komari Theme Glassmorphism v3 moves new code toward a layered architecture:

```text
Vue Component
  -> Composable (reactive state and lifecycle)
  -> Service (business logic)
  -> RequestManager / CacheService
  -> API / RPC
  -> Komari backend
```

## Rules

- Components render UI and invoke composables or services.
- Composables own `ref`, `computed`, `watch`, subscriptions, and lifecycle cleanup.
- Services own business logic such as auth, provider metadata, history loading, prediction, snapshot export, cache, and request orchestration.
- Constants belong in `src/constants/`; avoid new magic numbers in components.
- Utils stay pure and framework-light.
- Public routes remain public; sensitive operations require verified auth through the auth service.

## Foundation directories

- `src/services/` — business and infrastructure services.
- `src/constants/` — grouped runtime and architecture constants.
- `src/hooks/` — reserved for future cross-cutting hooks.
- `src/workers/` — reserved for future Web Worker workloads.
- `src/types/` — shared type entry point.

## Core services

- `auth.service.ts` — verified session state, login verification, and permission checks.
- `cache.service.ts` — shared TTL/LRU/reference-counted cache and promise deduplication helper.
- `request.service.ts` — keyed request deduplication, concurrency limiting, abort, timeout, and retry policy.
- `history.service.ts` — load/ping history normalization and request-manager-backed data fetching.
- `prediction.service.ts` — disk growth prediction from normalized history records.
- `provider.service.ts` — provider and geo metadata composition.
- `snapshot.service.ts` — JSON/CSV export composition and download boundary.

## Current M2-M6 scope

M2-M6 build on the v3 foundation without changing the public route contract:

- M2 keeps user interactions stable while routing history-heavy paths through shared request/cache infrastructure and virtualizing dense lists.
- M3 gates private tools, sensitive metadata, snapshot export, and disk prediction through verified auth; ordinary node and Ping history metrics stay public.
- M4 adds configurable presentation options such as card density, quick controls, detail tabs, list metadata, glass colors, and privacy display toggles.
- M5 adds advanced home tools: topology, provider value, health summary, snapshot export, disk prediction, and richer summary cards.
- M6 documents the architecture, auth, cache, data flow, migration behavior, and milestone acceptance points.

New work should continue to use the same layered chain rather than reintroducing component-local request, cache, or permission logic.
