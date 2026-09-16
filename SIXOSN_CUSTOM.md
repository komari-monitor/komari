# SIXOSN Komari customization

This fork builds [`SIXOSN/komari-theme-Glassmorphism`](https://github.com/SIXOSN/komari-theme-Glassmorphism) branch `sixosn/traffic-cycle-default` as Komari's embedded `default` theme.

## Scheduled traffic counters

The customized theme calculates upload and download usage from Komari's persisted `traffic.up` and `traffic.down` delta metrics. It does not alter or reset the operating system or Agent cumulative network counters.

The schedule is configured in **Admin → Theme settings**:

- Enable cycle traffic counter
- Monthly reset day (`1`–`31`; shorter months use their last day)
- Reset time (`HH:mm`)
- IANA timezone (for example `Asia/Shanghai`, `Asia/Singapore`, `UTC`)

At the configured boundary the displayed cycle advances automatically. Traffic quota percentages, node cards, list rows, overview cards, comparisons and the instance detail page all use the current-cycle value. If Metric Store data is temporarily unavailable, the UI keeps the original cumulative counters as a compatibility fallback.

## Build contract

The composite action at `.github/actions/build-frontend/action.yml` clones and builds the customized theme with Bun, normalizes its embedded manifest to `short: default`, and packages `dist/` as `web/public/defaultTheme/dist.tar.zst` before compiling Komari.
