# Loki

English | [简体中文](README.md)

Loki is a plugin for [DBX](https://github.com/t8y2/dbx), the open-source database workbench. It lets you query, browse and live-tail [Grafana Loki](https://grafana.com/oss/loki/) logs with LogQL inside DBX, without running Grafana. Addresses [t8y2/dbx#13126](https://github.com/t8y2/dbx/issues/13126).

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## Features

**Connection**

- HTTP or HTTPS, with a path prefix for Loki behind a reverse proxy (e.g. `/loki-gateway`).
- Multi-tenant Loki through `X-Scope-OrgID`.
- No auth, basic auth or a bearer token; optional TLS certificate skip for HTTPS.
- DBX SSH and proxy tunnels. When a tunnel is configured but the host gives no endpoint, the connection fails instead of bypassing the tunnel.

**Browse and query**

- Label browser: click a value to add `label="value"` to the query, or hover to exclude it. Below 720px it moves behind a Labels button in the query bar and opens as a drawer.
- LogQL editor with highlighting and completion for label names, label values, functions and parsers; run with `Shift+Enter` or `Ctrl/Cmd+Enter`.
- Log list: streams merged by timestamp, colored by level, with `|=` and `|~` matches highlighted. Expand a line to see all labels, add or exclude a label filter, or copy the line.
- Preset or custom time ranges, line limit, sort direction, line wrap, UTC display and query history.

**Log volume and live tail**

- Log volume histogram: matching lines per time bucket; click a bar to zoom in. Partial buckets at the window edges are counted exactly, so the total matches the log list when it is not truncated.
- Live tail: polls `query_range` every 2 s (no WebSocket needed). Each poll re-reads the last 10 s and skips lines already shown, so lines that batching agents push late are not lost. Keeps up to 5000 lines.

**Metric queries**

- `rate`, `count_over_time`, `sum by` and other metric queries render as a chart with a time axis and a series table.

**AI**

- "Ask AI" sends a sample of the current lines to the DBX built-in AI.
- Four read-only AI tools: `loki_labels`, `loki_label_values`, `loki_query_logs` and `loki_query_metrics`. Turn them on under Plugin Center → Installed → AI tools, then ask in Agent mode, e.g. "why did the api service fail in the last hour".

The UI is available in English and Chinese and follows the DBX light or dark theme.

## Requirements

- DBX 0.6.43 or later.
- Grafana Loki, or a gateway serving the Loki HTTP API. Verified with Loki 3.5.0.
- Release builds cover 5 targets: macOS (Apple Silicon / Intel), Windows x64 and Linux (x64 / arm64).

## Install

Once listed, search for **Loki** in the DBX plugin marketplace and install it. Marketplace installs use builds signed by the DBX Store.

To try it before it is listed, download the `.dbxp` for your platform from [Releases](https://github.com/seaways1015-afk/dbx-plugin-loki/releases), enable unsigned development packages in the DBX Plugin Center and install it from disk.

## Connection settings

| Field | Required | Notes |
| --- | --- | --- |
| Connection name | Yes | Name shown in DBX. |
| Protocol | Yes | `http` or `https`. |
| Host | Yes | Hostname or IP only, without scheme, port or path. |
| Port | Yes | Defaults to `3100`. |
| Path prefix | No | Set when Loki sits behind a reverse proxy, e.g. `/loki-gateway`. Defaults to `/`. |
| Tenant ID (X-Scope-OrgID) | No | Required for multi-tenant Loki; leave empty for single-tenant setups. |
| Authentication | Yes | None, basic auth or bearer token. |
| Username / Password | For basic auth | Shown when basic auth is selected. |
| Bearer token | For bearer auth | Shown when bearer token is selected. |
| Skip TLS certificate verification | No | Shown only for `https`; use for self-signed certificates. |

After saving, double-click the Loki connection in the sidebar to open the Loki Explorer workbench.

## Security

- Only Loki's read-only GET endpoints are called: `labels`, `label/<name>/values`, `query`, `query_range` and `status/buildinfo`. Nothing is written or deleted.
- Redirects are refused. A single response is capped at 16 MiB, results sent to the UI at 4 MiB, and lines longer than 16 KiB are truncated and marked.
- Passwords and tokens are kept in DBX password and secret fields and never appear in logs, workbench context or AI context.
- The plugin requests only `host.storage` (UI settings and query history) and `host.ai` (Ask AI and the AI tools). It does not listen on local ports, start child processes or write local files.

## Development

Prerequisites: Node.js 22+, Go 1.22+ and `@dbx-app/plugin-cli` 0.1.9.

```bash
npm install --global @dbx-app/plugin-cli@0.1.9
npm ci
npm test                         # frontend unit tests
go -C backend test -race ./...   # backend unit tests (Loki mocked with httptest)
npm run build                    # builds ui/index.html
```

- UI only (mock data, no DBX or Loki): run `npm run dev` and open the printed address plus `/preview.html`.
- Against a real Loki: start one with `docker run -d --name loki -p 3100:3100 grafana/loki:3.5.0`, run `dbx-plugin dev --path . --port 5190`, open `http://127.0.0.1:5190` and create a connection to host `127.0.0.1`, port `3100`. The dev host does not show or hide fields by authentication mode; check that in DBX desktop.
- Unsigned candidate package for the current platform: `npm run build && dbx-plugin package .` (output in `dist/`).

## Releasing (maintainers)

1. Publish a GitHub Release. `.github/workflows/plugin-release.yml` builds unsigned candidates for the 5 targets and `release-candidates.json`.
2. Open a PR to [t8y2/dbx-store](https://github.com/t8y2/dbx-store) adding `publishers/seaways1015-afk.json` (first submission only) and `candidates/io.github.seaways1015-afk.loki.json`, as described in its CONTRIBUTING.md.
3. Once a maintainer reviews, signs and merges it, the plugin appears in the DBX marketplace.

## License

[Apache-2.0](LICENSE); see also [NOTICE](NOTICE). Grafana Loki is a trademark of Grafana Labs. This project is not affiliated with Grafana Labs.
