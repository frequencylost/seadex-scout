# The built-in dashboard

This fork serves its own dashboard from the container, on port `9119`. You don't need Grafana, Loki, Alertmanager or a log collector. Open `http://<host>:9119`.

## How it works

seadex-scout reports everything through its structured log, as [Monitoring and alerts](monitoring.md) describes. The upstream Grafana dashboard reads those lines back out of Loki. The built-in dashboard reads the same lines inside the process instead. A logging handler copies each line into the dashboard's memory before writing it to standard output as usual.

As a result, the two dashboards show the same numbers, the scout's checks are unchanged, and the container log stays the same. You can still load the Grafana dashboard and the Loki rules alongside it.

The page refreshes every 30 seconds, but only from the container's own memory. It never contacts SeaDex. Only the scout's check schedule does: in this fork's starter config, a quick check every hour (`poll_interval: "1h"`), one of which is the daily full check. The attention tiles scale with `poll_interval`. **Last check** allows three intervals, as the container's health check does. A lasting problem counts as cleared once one and a half intervals pass without it being logged again.

The dashboard reads the lines at `info` even when `log.level` is `warn` or `error`, so raising the level does not empty it.

Its state goes in `webui-state.json` beside `config.yaml`, saved every minute and at shutdown. After a restart it shows the last findings, library overview and health until the next check replaces them. Delete the file to start over.

## Tabs

- **Overview**:
  - Attention tiles, shown only while something needs a look. They use the same conditions as the Grafana tiles and Loki alerts.
  - The number of upgrades, findings to check by hand, the share of your anime at SeaDex's best or an alt, and when the last check ran.
  - The **Library overview** bars from the daily full check.
  - **Upgrades available**, with filters, sortable columns, sizes and links to Sonarr or Radarr, SeaDex and the release. **Show optional upgrades** adds the upgrades where you already have a SeaDex alt.
  - **Check by hand**, plus the informational findings.
- **Activity**: upgrades entering and leaving the list.
- **Scout health**:
  - Lasting problems such as a SeaDex outage.
  - The configuration in effect, a chart of full-check durations, errors and degraded checks.
  - Torznab feed traffic.
- **Reports**: every report in `report.dir`, viewable in the page with verdict filters, or downloadable as Markdown or JSON. **Run a report now** starts `seadex-scout report` inside the container, the same as `docker exec seadex-scout /seadex-scout report`.
- **Log**: the newest 500 log lines, minus the per-finding and Torznab request lines.

## Settings

| Variable | Description | Default |
| --- | --- | --- |
| `SEADEX_SCOUT_WEBUI_PORT` | Port to listen on. `off` turns the dashboard off. | `9119` |
| `SEADEX_SCOUT_WEBUI_BIND` | Address to listen on. Empty means every interface. | _(empty)_ |
| `SEADEX_SCOUT_WEBUI_PASSWORD` | Set it to require HTTP Basic sign-in. | _(unset)_ |
| `SEADEX_SCOUT_WEBUI_USERNAME` | User name for the sign-in. | `admin` |

These are environment variables rather than `config.yaml` keys. Upstream rejects unknown keys in `config.yaml`, so keeping the dashboard settings out of it leaves the file compatible both ways.

`GET /healthz` answers `ok` without a sign-in, for a reverse proxy's health check.

## Limits

- The dashboard runs in daemon mode only. A container started with `mode: report` exits after its report and serves nothing.
- With `poll_interval: off`, checks run in `docker exec` children whose log lines never reach the daemon. The dashboard then stays mostly empty, the same as the Grafana dashboard.
- **First seen** resets when the container restarts, as upstream.
- Torznab request counts start from when the dashboard was first installed.

## Security

The page loads nothing from outside the container and sends a strict Content-Security-Policy. It escapes every value from SeaDex, Sonarr and Radarr, and links only to `http` and `https` addresses. A report run can only be started by the page itself, through a custom request header that another site cannot send. Without a password, anyone who can reach the port can see your library's findings and start a report, so keep the port on your local network or set `SEADEX_SCOUT_WEBUI_PASSWORD`.

## Working on the page

`internal/webui/static` holds the page: plain HTML, CSS and JavaScript, built into the binary. To see it with made-up data, without Sonarr:

```sh
WEBUI_DEMO_ADDR=127.0.0.1:9119 go test -run TestDemo -timeout 0 ./internal/webui/
```
