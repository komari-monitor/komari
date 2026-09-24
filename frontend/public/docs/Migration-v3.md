# Migration Notes for v3

## Breaking changes

No public Komari API response formats are changed by the v3 foundation work.

## Theme settings

A new optional managed setting is available:

- `exportSecondaryPassword`: when set, snapshot export requires this secondary password in addition to a verified logged-in session.

Leaving it empty preserves the previous logged-in export behavior, with stronger session verification.

## Export behavior

CSV export now neutralizes spreadsheet formula prefixes (`=`, `+`, `-`, `@`) to reduce CSV/Formula Injection risk.

## Privacy behavior

Private metadata and history-heavy operations now require verified login:

- advanced home tools
- snapshot export
- health summary generation
- disk prediction history loading
- provider geo metadata lookup

Public home/detail rendering still works without login; private data paths degrade to public metadata-only behavior instead of blocking the route.

## Request and cache behavior

History loading now goes through a shared request manager for keyed deduplication, timeout, retry, concurrency limiting, and abort on release. Provider metadata and load-history caches have centralized TTL/eviction rules.

## Developer migration

New v3 code should follow:

```text
Component -> Composable -> Service -> RequestManager / CacheService -> API / RPC
```

Use `src/constants/` for new limits, timings, and security settings. Sensitive operations should use a typed `PermissionKey` rather than a raw `isLoggedIn` boolean.
