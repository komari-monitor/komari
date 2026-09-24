# v3 M2-M6 Milestones

This document records the acceptance scope for the post-foundation v3 milestones. Public home and detail routes remain public throughout these milestones; private work is gated at the data/action boundary.

## M2 — Performance only

- Reuse shared `RequestManager` and cache services for history-heavy paths.
- Keep request keys specific enough to distinguish record type, node UUID, time range, and `maxCount`.
- Keep realtime node updates reactive by mutating the reactive node objects held in the nodes store.
- Use virtualized list rendering for dense node lists without changing list interactions.
- Avoid adding new user-facing behavior when only optimizing performance.

## M3 — Security and permissions only

- Verify sensitive actions through `appStore.requireLoginPermission()` or the auth service before work starts.
- Protected surfaces include advanced home tools, snapshot export, provider geo lookup, and disk prediction history; ordinary node metrics and Ping history stay public.
- Keep public rendering available for logged-out visitors; degrade to public metadata instead of blocking routes.
- Snapshot export keeps CSV formula neutralization for `=`, `+`, `-`, and `@` prefixes.
- The optional export secondary password is a client-side friction layer in addition to verified login, not a replacement for backend authorization.
- Managed markdown is rendered through Vue bindings and URL schemes are restricted for links/images.

## M4 — UI/UX only

- UI settings are normalized in the app store from managed theme settings.
- Keep the default node card size as `compact`; `mini` is an additional high-density option.
- Quick controls, detail tabs, list metadata fields, glass color presets, and privacy display toggles should not introduce new data permissions by themselves.
- Compose UI from existing local primitives under `src/components/ui/`.

## M5 — New features

- Advanced home tools: topology, provider value, health summary, and snapshot export.
- Disk exhaustion prediction appears only when enabled and authenticated history loading is allowed.
- Provider metadata enrichment separates public provider-only resolution from private geo-enriched resolution in cache keys.
- Topology upstream inference uses node tags containing upstream/parent markers such as `upstream:<name>` or `parent:<name>`.

## M6 — Docs, tests, and developer experience

- Validate source changes with `bun run lint` and `bun run build` from the repository root.
- There is currently no test suite; do not add or document `bun test` unless a real test runner is introduced.
- Keep architecture docs aligned with the v3 chain: Component → Composable → Service → RequestManager / CacheService → API / RPC.
- Document security limitations and public/private degradation behavior alongside new features.
