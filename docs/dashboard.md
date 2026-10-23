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
  - A filter bar for both tables:
    - title or group, Sonarr or Radarr, and what to show;
    - **Show optional upgrades**, which adds the upgrades where you already have a SeaDex alt;
    - **Show unmonitored**, off by default, which adds series, seasons and movies set to unmonitored in Sonarr or Radarr.

    The dashboard reads the monitored flags straight from the Sonarr and Radarr APIs (`/api/v3/series` and `/api/v3/movie`, or `/api/v5` on Sonarr 5) every 15 minutes, so a change there shows without waiting for a SeaDex check. A row counts as monitored until its flags have been read.
  - **Upgrades available** lists everything with something to do. The **Why** column says what:
    - Upgrades come first: **Newer version** of your group, **Neither best nor alt**, and optionally **SeaDex alt**.
    - Then **Not downloaded**: seasons and films Sonarr or Radarr has, with no file yet, that SeaDex has an entry for.
    - Then the release groups that need a look: **Several release groups** and **Group could not be read**.
    - Then **No SeaDex best**: entries where SeaDex names only a theoretical best, the entry is incomplete, or the best is filtered out or on a tracker you have turned off. The SeaDex best column says which, and the alt still shows.
  - **Already on a SeaDex pick** lists what you hold at SeaDex's best, or at an alt where SeaDex names no best. A negative alt size change there is disk space the alt would save.

  Both tables have the same columns:
  - **On disk:** the size of the files Sonarr or Radarr holds.
  - SeaDex's best and its headline alt each get a **Size / change** column and a **Link** column, Nyaa first. The size is the download, and the change below it is how much your library grows or shrinks if you take it.
  - Both group names link to the show's SeaDex page. Any other alt groups SeaDex lists appear under the alt's name.
  - An incomplete SeaDex entry with a best still lists as an upgrade, marked "SeaDex entry incomplete".
  - A size change is worked out by matching each episode the download holds to the file Sonarr has for it, then subtracting the files it would replace. When the files can't be matched one by one, for example because of a naming style the scout can't read, a release that covers more or fewer episodes than you have, or a season Sonarr numbers differently, the change falls back to the download minus the **On disk** size. That fallback is marked **≈**.

  To list all of this, this fork's comparison runs in an extended mode (`compare.Config.Extended`). It also reports entries upstream stays silent on: no file, already at best, and no obtainable best. It adds attributes to every finding line: `library_bytes`, `entry_incomplete`, `no_best_reason`, the `alt_*` ones, and the sizes. The new rows log at `info` as `SeaDex entry not downloaded`, `library holds the SeaDex best` and `SeaDex lists no best you can get`. None of this is in the log contract upstream's alert rules read, apart from one change: an incomplete entry with a best now logs `better release available` rather than `SeaDex entry is incomplete`. The log grows by one line per SeaDex entry in your library on each check.
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

## Notifications

The container can send notifications to [ntfy](https://ntfy.sh) by itself, without Loki or Alertmanager. Set `SEADEX_SCOUT_NTFY_URL` to your topic's full address, such as `https://ntfy.sh/my-seadex-topic` or the address of your own ntfy server, and subscribe to that topic in the ntfy app. On the public server, anyone who knows a topic name can read it, so pick a long, hard-to-guess name or protect it with an access token.

| Variable | Description | Default |
| --- | --- | --- |
| `SEADEX_SCOUT_NTFY_URL` | The topic's full address. Empty turns notifications off. | _(unset)_ |
| `SEADEX_SCOUT_NTFY_TOKEN` | An access token (`tk_...`) for a protected topic. | _(unset)_ |
| `SEADEX_SCOUT_NTFY_USERNAME`, `SEADEX_SCOUT_NTFY_PASSWORD` | A user for a protected topic, instead of a token. | _(unset)_ |
| `SEADEX_SCOUT_NTFY_EVENTS` | What to send, comma-separated. | `upgrades,problems,errors,reports` |
| `SEADEX_SCOUT_NTFY_OPTIONAL` | `true` also sends upgrades where you already have a SeaDex alt. | `false` |
| `SEADEX_SCOUT_NTFY_UNMONITORED` | `true` also sends upgrades for unmonitored series, seasons and movies. | `false` |
| `SEADEX_SCOUT_DASHBOARD_URL` | The dashboard's address, for a Dashboard button on each message. | _(unset)_ |

The events:

- **upgrades**: one message per new upgrade or newer version, when a check first finds it. It gives the groups, the best and alt sizes and size changes, and buttons for Nyaa (or the release's tracker), Sonarr or Radarr, and the dashboard. Tapping the message opens the SeaDex entry. Upgrades found right after a start are not sent, so a restart or an image update sends nothing. More than 10 in one check send the first 10 and a summary.
- **problems**: a lasting problem, such as SeaDex being unreachable or the library coming back much smaller, when it starts and when it ends. It also sends when no check has finished or started in three check intervals (at least 3 hours), and when checks run again. A problem is announced once, and a restart does not repeat it.
- **errors**: a one-off error, such as a failed Sonarr read. The same error sends again at most every 6 hours.
- **reports**: a report you started from the dashboard finished.

These follow the same conditions as upstream's alert rules (see [Monitoring and alerts](monitoring.md#alerting)), so you get the useful part of that setup from the container alone. The **Notifications** card on the Scout health tab shows where messages go, how many were sent and the last error, and has a **Send test** button.

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
