## ADDED Requirements

### Requirement: the daemon serves the usage dashboard on its existing listener
The usage dashboard MUST be served by the already-running OTel daemon on the same listener it uses for OTLP ingestion. The dashboard MUST NOT require a second process, and MUST NOT occupy a second port.

The dashboard's HTTP surface MUST consist of:

- `GET /ui` — the dashboard page
- `GET /ui/<asset>` — the dashboard's static assets (script, stylesheet)
- `GET /api/stats` — the dashboard's JSON data endpoint

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

### Requirement: dashboard access is protected by a token
The dashboard MUST NOT be readable without a secret token.

The token MUST be 32 bytes of cryptographically random data rendered as lowercase hex, and MUST be stored in `~/.cc-launcher/web-token` with file mode `0600`. The daemon MUST generate the token on first start when the file does not exist, and MUST write it atomically (temporary file plus rename).

Access MUST be established as follows:

1. A client requests `GET /ui?token=<token>`.
2. The server compares the supplied token against the stored token in constant time.
3. On a match, the server sets a session cookie and responds with a redirect to `/ui` with the query string removed.
4. Subsequent requests authenticate with the session cookie alone.

The session cookie MUST be `HttpOnly` and `SameSite=Lax`, MUST be scoped to the site path, and MUST carry an expiry. Its value MUST be a signed timestamp (an HMAC over the issuance time, keyed by the token) and MUST NOT contain the token itself in plaintext.

Rotating the token MUST invalidate every session cookie previously issued, because the cookie's signature is keyed by the token.

Unauthenticated requests MUST be rejected: the dashboard page and its static assets MUST respond `401` with a minimal HTML page explaining that a token is required and pointing at the command that prints the access URL; the JSON endpoint MUST respond `401` with a JSON error object.

#### Scenario: a token file is created on first start
- **WHEN** the daemon starts and `~/.cc-launcher/web-token` does not exist
- **THEN** the daemon creates it containing 64 lowercase hex characters, with file mode `0600`

#### Scenario: the token file is not regenerated on later starts
- **WHEN** the daemon starts and `~/.cc-launcher/web-token` already exists
- **THEN** the existing token is preserved byte-for-byte

#### Scenario: a valid token grants access and is then removed from the URL
- **WHEN** a client requests `GET /ui?token=<correct token>`
- **THEN** the response is a redirect to `/ui` without the token, and it sets a session cookie

#### Scenario: the session cookie authenticates later requests
- **WHEN** a client that obtained a session cookie requests `GET /api/stats` without any token parameter
- **THEN** the request is served with `200` and the JSON payload

#### Scenario: an invalid token is rejected
- **WHEN** a client requests `GET /ui?token=<wrong token>`
- **THEN** the response status is `401` and no session cookie is set

#### Scenario: a request without token or cookie is rejected
- **WHEN** a client requests `GET /api/stats` with neither a token parameter nor a session cookie
- **THEN** the response status is `401` and the body is a JSON object carrying an `error` field

#### Scenario: rotating the token invalidates existing sessions
- **WHEN** the user rotates the token and a client still holds a session cookie issued under the old token
- **THEN** the client's next request is rejected with `401`

#### Scenario: the token file is not world-readable
- **WHEN** the token file exists
- **THEN** its file mode is `0600` (readable and writable by the owner only)

### Requirement: the dashboard exposes a JSON stats endpoint
`GET /api/stats` MUST return the usage aggregates for the requested window and filters as a single JSON object.

The endpoint MUST accept these query parameters:

- `window` — one of `24h`, `7d`, `30d`, `90d`; defaults to `7d`. `24h` MUST produce hourly buckets covering the current local day; `7d` / `30d` / `90d` MUST produce daily buckets covering the corresponding number of local calendar days. The window semantics MUST match the existing `ccl stats --day N` semantics exactly.
- `project` — a substring filter on the session's working directory.
- `provider` — a substring filter on the session's provider.
- `model` — a substring filter on the session's model.
- `group` — one of `session`, `project`, `provider`, `model`; defaults to `session`.

An unknown `window` or `group` value MUST be rejected with `400`.

The response MUST contain: `window`, `since`, `group_by`, `generated_at`, a `totals` object, a `sessions` array, a `buckets` array, and three share breakdowns (`projects`, `providers` and `models`) each carrying a name, token total, cost total, and the token and cost share of the window.

The response MUST NOT expose raw metric labels. Metric labels carry host-identifying attributes (a user fingerprint, host architecture, OS version); the endpoint MUST emit an explicit field whitelist and MUST NOT pass through label data.

The endpoint MUST open the database read-only and MUST NOT block the daemon's writes, and MUST NOT require the OTLP ingestion path to be involved.

#### Scenario: the default window is seven days
- **WHEN** a client requests `GET /api/stats` with no parameters
- **THEN** the response's `window` is `7d` and its `buckets` array holds 7 daily buckets

#### Scenario: the 24 hour window produces hourly buckets
- **WHEN** a client requests `GET /api/stats?window=24h`
- **THEN** the response's `buckets` array holds 24 hourly buckets for the current local day

#### Scenario: an unknown window is rejected
- **WHEN** a client requests `GET /api/stats?window=7days`
- **THEN** the response status is `400`

#### Scenario: the project filter narrows the result
- **WHEN** a client requests `GET /api/stats?project=cc-launcher` and sessions exist under that working directory
- **THEN** every row in `sessions` and every entry in `totals` reflects only those sessions

#### Scenario: the model filter narrows the result
- **WHEN** a client requests `GET /api/stats?model=MiniMax`
- **THEN** every row in `sessions` has a model containing `MiniMax`, and `totals` reflects only those rows

#### Scenario: the provider filter narrows the result
- **WHEN** a client requests `GET /api/stats?provider=minimax`
- **THEN** every row in `sessions` has a provider containing `minimax`, and `totals` reflects only those rows

#### Scenario: grouping by model is supported
- **WHEN** a client requests `GET /api/stats?group=model`
- **THEN** the `sessions` array holds one row per distinct model, and `group_by` is `model`

#### Scenario: grouping by provider is supported
- **WHEN** a client requests `GET /api/stats?group=provider`
- **THEN** the `sessions` array holds one row per distinct provider, and `group_by` is `provider`

#### Scenario: the provider breakdown is reported
- **WHEN** a client requests `GET /api/stats` and sessions ran on two distinct providers
- **THEN** the response's `providers` array holds one entry per provider, each with its token total, cost total and shares

#### Scenario: label data is not exposed
- **WHEN** a client requests `GET /api/stats`
- **THEN** no field of the response contains the metric label values (the user fingerprint, host architecture, or OS version)

#### Scenario: the endpoint reads without blocking the daemon
- **WHEN** the daemon is writing metrics while a client requests `GET /api/stats`
- **THEN** the request succeeds and returns promptly, with no lock contention error

### Requirement: the dashboard page renders the usage overview
`GET /ui` MUST serve a self-contained HTML page that renders, from the JSON endpoint:

- **KPI cards** — total input tokens, output tokens, cache-creation tokens, cache-read tokens, total cost, and session count for the selected window.
- **A trend chart** — token consumption over the window's time buckets, split by token type, plus the cost trend over the same buckets.
- **Share breakdowns** — the token and cost share per project, per provider and per model.
- **A session table** — one row per session with its start time, project, provider, model, and the same token and cost columns the terminal table shows.

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

### Requirement: the dashboard supports window switching, filtering and refresh
The dashboard MUST let the user change the time window, filter by project, by provider and by model, refresh manually, and enable or disable automatic refresh.

Automatic refresh MUST be enabled by default with an interval of 30 seconds, and MUST be switchable off. When a filter or window changes, any in-flight data request MUST be cancelled so that a stale response cannot overwrite the newly selected view.

#### Scenario: changing the window reloads the data
- **WHEN** the user selects the `30d` window in the dashboard
- **THEN** the page re-requests the data with `window=30d` and re-renders the charts, the cards and the table

#### Scenario: the project filter is applied
- **WHEN** the user types a project filter value and applies it
- **THEN** the next data request carries that value in the `project` parameter

#### Scenario: the model filter is applied
- **WHEN** the user selects a model in the filter control
- **THEN** the next data request carries that model in the `model` parameter

#### Scenario: the provider filter is applied
- **WHEN** the user selects a provider in the filter control
- **THEN** the next data request carries that provider in the `provider` parameter

#### Scenario: the manual refresh button reloads the data
- **WHEN** the user clicks the refresh button
- **THEN** the page re-requests the data and re-renders

#### Scenario: automatic refresh is on by default
- **WHEN** the user loads the dashboard and does nothing
- **THEN** the page re-requests the data every 30 seconds

#### Scenario: automatic refresh can be switched off
- **WHEN** the user switches automatic refresh off
- **THEN** the page stops issuing periodic data requests

#### Scenario: a stale response does not overwrite the current view
- **WHEN** the user changes the window while a request for the previous window is still in flight
- **THEN** the late response of the previous request is discarded and does not re-render the view

### Requirement: ccl web is the entry point to the dashboard
The `ccl` CLI MUST provide a `web` subcommand that gives the user access to the dashboard.

- `ccl web` MUST ensure the daemon is running (starting it if it is not), then print the dashboard URL including the token. The printed URL MUST include a LAN-reachable address and a loopback address.
- `ccl web --rotate` MUST regenerate the token, and MUST state that previously issued URLs and sessions are no longer valid.
- `ccl web --backfill` MUST run the historical cwd backfill on demand, without the per-start bound.
- `ccl web --open` MUST open the dashboard URL in the system's default browser.

`ccl serve --status` MUST also report the dashboard URL.

The `ccl web` command MUST NOT require the daemon to be running in order to print the URL or to rotate the token.

#### Scenario: ccl web prints the dashboard URL
- **WHEN** the user runs `ccl web` and the daemon is running
- **THEN** stdout contains a URL on the daemon's port whose query string carries the token

#### Scenario: ccl web starts the daemon when it is not running
- **WHEN** the user runs `ccl web` and the daemon's port is not reachable
- **THEN** the daemon is started and becomes reachable before the URL is printed

#### Scenario: ccl web works without a running daemon
- **WHEN** the user runs `ccl web` and the daemon cannot be started
- **THEN** the URL is still printed (with the token), and the failure to start the daemon is reported

#### Scenario: rotating the token invalidates the old URL
- **WHEN** the user runs `ccl web --rotate`
- **THEN** the stored token changes, and the output states that the previously issued URL and its sessions are no longer valid

#### Scenario: the explicit backfill is unbounded
- **WHEN** the user runs `ccl web --backfill`
- **THEN** every session row with an empty `cwd` is processed, not just the first 500

#### Scenario: ccl web --open launches a browser
- **WHEN** the user runs `ccl web --open`
- **THEN** the system browser is invoked with the dashboard URL

#### Scenario: serve --status reports the dashboard URL
- **WHEN** the user runs `ccl serve --status`
- **THEN** stdout includes the dashboard URL
