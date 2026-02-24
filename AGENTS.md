# Repository Guidelines

## Project Structure & Module Organization
Komari is a Go service with a Cobra CLI entrypoint.

- `main.go`: process entrypoint.
- `cmd/`: CLI commands (`server`, auth/maintenance helpers).
- `api/`: HTTP handlers and middleware (`api/admin`, `api/client`, `api/public`, `api/jsonRpc`).
- `database/`: GORM models and data-access logic (`models`, `clients`, `tasks`, `records`).
- `utils/`: shared infrastructure (logging, OAuth providers, notifier/message senders, geoip, RPC helpers).
- `ws/`: WebSocket connection helpers.
- `public/`: embedded static assets/theme files (`public/defaultTheme/dist`).
- `compat/`: compatibility protocol code (e.g., Nezha proto bindings).

## Build, Test, and Development Commands
- `go run . server -l 0.0.0.0:25774`: run locally with explicit listen address.
- `go build -o komari .`: build a local binary.
- `go test ./...`: run all unit tests.
- `go test ./api -run TestLogin`: run a focused test target.
- `docker run -d -p 25774:25774 -v $(pwd)/data:/app/data --name komari ghcr.io/komari-monitor/komari:latest`: run containerized build.

If you are rebuilding UI assets manually, build `komari-web` and copy output into `public/defaultTheme/dist` plus `komari-theme.json` into `public/defaultTheme/`.

## Coding Style & Naming Conventions
- Follow standard Go formatting: run `gofmt` on changed files before committing.
- Use idiomatic Go naming: exported identifiers in `PascalCase`, package-local in `camelCase`.
- Keep package names short and lowercase (match existing directories).
- Place HTTP handlers in the closest domain package (`api/admin`, `api/client`, etc.), and keep business/data logic in `database/` or `utils/`.

## Testing Guidelines
- Use Go’s `testing` package with `testify/assert` (current project pattern).
- Keep tests adjacent to implementation as `*_test.go`.
- Prefer table-driven tests for handlers and data logic.
- Cover new branches and error paths; run `go test ./...` before opening a PR.

## Commit & Pull Request Guidelines
- Match existing commit style: Conventional Commit prefixes such as `feat:`, `fix:`, `refactor:`, `docs:`.
- Keep each commit scoped to one logical change.
- PRs should include:
  - clear summary of behavior change,
  - linked issue (if applicable),
  - test evidence (`go test ./...` output or equivalent),
  - screenshots for UI/theme-impacting changes.

## Security & Configuration Tips
- Do not commit credentials or runtime data from `./data/`.
- Prefer environment variables for sensitive/runtime config (for example `KOMARI_DB_*`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `KOMARI_LISTEN`).

## Remarks
- 所用回复必须使用中文