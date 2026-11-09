## MODIFIED Requirements

### Requirement: the daemon serves the usage dashboard on its existing listener
The usage dashboard MUST be served by the already-running OTel daemon on the same listener it uses for OTLP ingestion. The dashboard MUST NOT require a second process, and MUST NOT occupy a second port.

The dashboard's HTTP surface MUST consist of:

- `GET /ui` — the dashboard page
- `GET /ui/<asset>` — the dashboard's static assets (script, stylesheet)
- `GET /api/stats` — the dashboard's JSON data endpoint

The dashboard page and its static assets are the console described by `web-config-console`; that capability specifies the additional read and write endpoints the console mounts, and those endpoints MUST be served by the same daemon on the same listener.

The OTLP ingestion routes (`/v1/metrics`, `/v1/logs`, `/v1/traces`) MUST keep their existing behavior and MUST NOT be affected by the dashboard routes.

Because the dashboard is hosted by the daemon, it is available exactly when the daemon is running. The daemon MUST NOT auto-exit when idle, so that a running daemon keeps serving the dashboard until it is explicitly stopped.

#### Scenario: the dashboard is reachable on the daemon's port
- **WHEN** the daemon is running and a client requests `GET /ui` on the daemon's port
- **THEN** the response is served by the daemon (not by a separate server process) and no additional port is opened

#### Scenario: the OTLP routes are unaffected
- **WHEN** the daemon is running and Claude Code posts an OTLP export to `/v1/metrics`
- **THEN** the export is parsed and persisted exactly as before the dashboard existed

#### Scenario: the dashboard stops when the daemon stops
- **WHEN** the user runs `ccl serve --stop` and then requests `GET /ui` on the daemon's port
- **THEN** the connection is refused, because the dashboard is hosted by the stopped daemon

#### Scenario: the console's endpoints share the daemon's listener
- **WHEN** the daemon is running and a client requests the provider list from `GET /api/providers` on the daemon's port
- **THEN** the response is served by the daemon on the same port that serves `/ui`, and no additional port is opened

### Requirement: the dashboard page renders the usage overview
`GET /ui` MUST serve a self-contained HTML page that renders, from the JSON endpoint:

- **KPI cards** — total input tokens, output tokens, cache-creation tokens, cache-read tokens, total cost, and session count for the selected window.
- **A trend chart** — token consumption over the window's time buckets, split by token type, plus the cost trend over the same buckets.
- **Share breakdowns** — the token and cost share per project, per provider and per model.
- **A session table** — one row per session with its start time, project, provider, model, and the same token and cost columns the terminal table shows.

These elements MUST be rendered in the usage view of the console, which is one of the console's three views; the console's structure is specified by `web-config-console`.

The page's assets MUST be embedded in the binary (no network fetch of scripts or stylesheets from the internet). The charts MUST be drawn as inline SVG, with no charting library. The page MUST remain readable on a narrow viewport, with the data tables scrolling horizontally inside their own container rather than widening the page.

The page MUST render a legible empty state when the selected window contains no usage.

#### Scenario: the page renders the KPI cards
- **WHEN** a client loads the dashboard and the window contains usage
- **THEN** the page shows the total token counts, the total cost, and the session count for that window

#### Scenario: the page renders the trend chart
- **WHEN** a client loads the dashboard with the default 7-day window
- **THEN** the page shows a chart with one bar group per day, each split into the four token types, and a cost trend over the same days

#### Scenario: the page renders the session table
- **WHEN** a client loads the dashboard and sessions exist in the window
- **THEN** the page shows a table whose columns are the session start time, project, provider, model, the four token counts, and the cost

#### Scenario: the page renders the provider share
- **WHEN** a client loads the dashboard and sessions in the window ran on two distinct providers
- **THEN** the page shows a per-provider token and cost share breakdown alongside the per-project and per-model ones

#### Scenario: the page renders an empty state
- **WHEN** a client loads the dashboard and the selected window contains no usage
- **THEN** the page shows an explicit "no usage in this window" message rather than an empty chart

#### Scenario: the page needs no external asset
- **WHEN** a client loads the dashboard with no outbound network access
- **THEN** the page renders fully, with its script and stylesheet served from the daemon itself

#### Scenario: the page does not scroll horizontally on a narrow viewport
- **WHEN** the dashboard is viewed at a narrow viewport width
- **THEN** the page body does not scroll horizontally; wide tables scroll inside their own container
