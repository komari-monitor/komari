# SIXOSN Komari

[English](./README.md) | [简体中文](./README_zh-cn.md)

SIXOSN Komari is a security-focused, independently maintained server-monitoring distribution. It keeps Komari's lightweight monitoring experience while providing a dedicated default theme, per-node traffic billing cycles, and a deliberately reduced remote-control surface.

> [!IMPORTANT]
> This distribution accepts only the matching [`SIXOSN/komari-agent`](https://github.com/SIXOSN/komari-agent). Agents from other distributions are rejected by design.

## Project relationship

This repository is derived from [`komari-monitor/komari`](https://github.com/komari-monitor/komari) and is maintained independently by SIXOSN. It is not an official upstream release, and upstream support and compatibility must not be assumed.

Compatibility intentionally differs because the server and agent validate each other's SIXOSN distribution identity. Source-code licensing and legally required attribution remain in [LICENSE](./LICENSE) and [NOTICE](./NOTICE); this relationship statement does not replace those files.

## Distribution features

- Glassmorphism public interface and administration frontend built from source in this repository.
- Per-node traffic reset day, reset time, and IANA time-zone configuration.
- Mutual distribution validation between the SIXOSN server and agent.
- Remote commands, Web SSH, browser terminals, file management, and file-transfer endpoints removed.
- Existing databases and server settings preserved during upgrades; new traffic-cycle fields default to disabled.
- Real-time metrics, history, plugins, and self-hosted administration retained.

## Related repositories

- Server: [`SIXOSN/komari`](https://github.com/SIXOSN/komari)
- Agent: [`SIXOSN/komari-agent`](https://github.com/SIXOSN/komari-agent)
- Frontend source: [`frontend/public`](./frontend/public) and [`frontend/admin`](./frontend/admin)

## Docker deployment

```bash
docker run -d \
  --name komari \
  --restart always \
  -p 25774:25774 \
  -v /path/to/komari-data:/app/data \
  ghcr.io/sixosn/komari:1.5.0-fix6
```

Back up the mounted data directory before replacing an existing container.

## Agent installation

Create or select a node in the administration panel, then follow the installation guidance shown there. Installation commands, credentials, and compatibility details are intentionally not published in this README.

## Security scope

Use this software only on systems you own or are authorized to administer. Removing remote-control features reduces exposure but does not replace TLS, access control, host hardening, backups, or timely dependency updates.
