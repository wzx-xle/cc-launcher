# web-config-console Specification

## Purpose
TBD - created by archiving change web-config-console. Update Purpose after archive.
## Requirements
### Requirement: the dashboard is a three-view console with a top navigation bar

`GET /ui` MUST serve a single self-contained page that presents three views — usage, providers and Claude — switched by a top navigation bar.

The page MUST use hash-based routing, so that a browser reload never depends on server-side route fallback.

Each view MUST consist of a left column holding a list of objects and a right column holding that object's detail or editor. The usage view's left column MUST be empty and its right column MUST hold the existing usage dashboard, whose rendering behavior MUST be unchanged by the restructuring.

The console's scripts and stylesheets MUST be embedded in the binary and MUST NOT be fetched from the internet. The console MUST NOT introduce a front-end build step: its assets MUST be plain files embedded with `go:embed`.

#### Scenario: switching views changes the visible content

- **WHEN** the user selects "providers" in the top navigation bar
- **THEN** the page shows the provider list in the left column and the provider's detail in the right column, and the URL's fragment identifies the providers view

#### Scenario: the usage view still renders the existing dashboard

- **WHEN** the user selects "usage" in the top navigation bar
- **THEN** the page shows the KPI cards, the trend chart, the share breakdowns and the session table exactly as before, and the left column is empty

#### Scenario: the console needs no external asset

- **WHEN** a client loads the console with no outbound network access
- **THEN** the page renders fully, with every script and stylesheet served by the daemon

### Requirement: providers can be listed and read

`GET /api/providers` MUST return the configured providers, each entry carrying the provider name, its `order` and `note` metadata when present, whether it declares a quota interface, and whether it carries a credential.

`GET /api/providers/{name}` MUST return the provider's configuration file as raw JSON text together with an entity tag derived from that file's content. The endpoint MUST return the file's bytes as stored and MUST NOT re-serialize or reorder them.

#### Scenario: the provider list reports the display metadata

- **WHEN** a client requests `GET /api/providers` and three providers are configured, one of them with a `note`
- **THEN** the response holds three entries, and the entry for the annotated provider carries the note

#### Scenario: reading a provider returns its raw file and a tag

- **WHEN** a client requests `GET /api/providers/iflytek` and the provider's file contains a specific JSON document
- **THEN** the response body carries that JSON document's text and an entity tag that changes when the file's content changes

#### Scenario: an unknown provider is rejected

- **WHEN** a client requests `GET /api/providers/does-not-exist`
- **THEN** the response status is `404`

### Requirement: provider configuration can be saved from the console

`PUT /api/providers/{name}` MUST accept the provider's configuration as raw JSON text and MUST replace the provider's configuration file with it.

The endpoint MUST require an `If-Match` header carrying the entity tag returned by the read. If the tag does not match the file's current content, the endpoint MUST respond `409`, MUST NOT modify the file, and MUST return the file's current content so the client can reconcile.

Before writing, the endpoint MUST validate the submitted configuration against the known-key schema. If validation fails, the endpoint MUST respond `400` with the list of offending keys and MUST NOT modify the file.

Before writing, the endpoint MUST copy the current file into the backup directory. The write MUST be atomic (temporary file plus rename).

#### Scenario: saving with a stale tag is rejected

- **WHEN** a client PUTs a provider configuration with an `If-Match` tag that no longer matches the file's current content
- **THEN** the response status is `409` and the file's content is unchanged

#### Scenario: an invalid configuration is rejected before writing

- **WHEN** a client PUTs a provider configuration whose known key has the wrong type
- **THEN** the response status is `400`, the response lists the offending keys, and the file's content is unchanged

#### Scenario: malformed JSON is rejected before writing

- **WHEN** a client PUTs a provider configuration whose body is not valid JSON
- **THEN** the response status is `400` and the file's content is unchanged

#### Scenario: saving backs up the previous configuration

- **WHEN** a client successfully saves a provider configuration
- **THEN** a copy of the previous file's content exists in the backup directory

### Requirement: providers can be created, copied and deleted

`POST /api/providers` MUST create a provider. The request body MUST carry either a template name — resolved with the same template catalogue the CLI uses — or the full configuration text together with a name. The endpoint MUST reject a name that already exists, and MUST reject a name that is not a valid file name.

`POST /api/providers/{name}/copy` MUST create a provider holding a copy of the named provider's configuration, under the name given in the request body.

`DELETE /api/providers/{name}` MUST remove the provider's configuration file, and MUST also remove that provider's quota entry from the quota data file, so that a deleted provider does not keep showing a quota.

The delete endpoint MUST NOT remove a provider's quota entry when the provider does not exist.

#### Scenario: creating a provider from a template

- **WHEN** a client POSTs `{"template": "deepseek", "name": "my-deepseek"}` and no provider by that name exists
- **THEN** the response status is `200` and a provider configuration file for `my-deepseek` exists

#### Scenario: creating a provider with an existing name is rejected

- **WHEN** a client POSTs a creation request whose name matches an existing provider
- **THEN** the response status is `409` and the existing provider's file is unchanged

#### Scenario: copying a provider

- **WHEN** a client POSTs `{"name": "copy-of-it"}` to `/api/providers/iflytek/copy`
- **THEN** the new provider's configuration equals the source provider's configuration

#### Scenario: deleting a provider removes its quota entry

- **WHEN** a client deletes a provider that has an entry in the quota data file
- **THEN** the provider's configuration file is gone and the quota data file no longer holds an entry for that provider

### Requirement: provider configuration is edited as JSON with syntax highlighting

The provider editor MUST display the configuration as JSON text with syntax highlighting for keys, string values, numbers, booleans, null and punctuation.

The editor MUST render credential values in plaintext and MUST NOT mask or obfuscate them.

The editor MUST show a parse error and MUST NOT offer a save action while the displayed text is not valid JSON.

#### Scenario: the editor highlights JSON tokens

- **WHEN** the user opens a provider whose configuration contains a string key and a number
- **THEN** the key, the string and the number are rendered in distinguishable styles

#### Scenario: credentials are shown in plaintext

- **WHEN** the user opens a provider whose configuration carries an authentication token
- **THEN** the token's value is visible in the editor as stored

#### Scenario: invalid JSON blocks saving

- **WHEN** the user edits the configuration into invalid JSON
- **THEN** the editor reports a parse error and the save action is unavailable

### Requirement: the quota of the selected provider is displayed

`GET /api/providers/{name}/quota` MUST return the quota currently recorded for that provider: its dimensions, whether the record is stale, and when it was fetched.

The endpoint MUST read the local quota data file and MUST NOT send any network request.

The console MUST distinguish three states and MUST NOT present a missing record as an unlimited quota:

- a fresh record — rendered with the dimension values,
- a stale record — rendered as stale, with the age of the record,
- **no record** — rendered as "not collected recently", explicitly noting that this does not mean the provider has no limits.

The rendered values MUST use the same thresholds, the same value formats and the same per-kind rendering rules as the terminal status line, so that the console and the status line never disagree about the same provider.

The bar MAY be drawn differently in the two media — the terminal draws it with block characters, the console draws it as a CSS progress bar — because block characters have different heights in a browser font and render as a ragged edge. What MUST agree is the ratio the bar represents, the thresholds that colour it, and the numeric text beside it.

The console MUST select the rendering rules by dimension kind, the same way the terminal status line does:

- a `window` dimension shows the percentage only — without the used/total pair, and with the percentage rounded to a whole number,
- a `balance` dimension shows the remaining amount only — without a bar,
- any other dimension shows the percentage, the bar and the used/total pair.

#### Scenario: a fresh quota record is rendered

- **WHEN** a client requests the quota of a provider that has a freshly fetched record with two dimensions
- **THEN** the response carries both dimensions and reports the record as not stale

#### Scenario: a missing record is not reported as unlimited

- **WHEN** a client requests the quota of a provider that has no record in the quota data file
- **THEN** the response reports that no quota was collected, and the console does not render an empty or unlimited quota bar

#### Scenario: a stale record is marked

- **WHEN** a client requests the quota of a provider whose record is older than the freshness threshold
- **THEN** the response marks the record as stale and carries the fetch timestamp

#### Scenario: each dimension kind is rendered the way the status line renders it

- **WHEN** a provider's record holds a `window` dimension with a used percentage and a limit
- **THEN** the console shows the percentage alone beside the bar, without the used/total pair, so that its text matches what the terminal status line shows for the same dimension

### Requirement: the quota can be refreshed on demand

`POST /api/providers/{name}/quota/refresh` MUST query the provider's quota interface and write the result back to the quota data file, then return the refreshed dimensions.

The endpoint MUST honour the persisted rate-limit backoff: when the provider's quota pool is currently backing off, the endpoint MUST respond `429` and MUST report the remaining backoff duration, and MUST NOT send a request.

A failed query MUST leave the previous entry in place, so that the previous values remain readable and simply age into the stale state.

#### Scenario: refreshing writes the new values

- **WHEN** a client POSTs to refresh the quota of a provider that has no backoff and whose interface answers
- **THEN** the response carries the fetched dimensions and the quota data file now holds the new values

#### Scenario: refreshing during a backoff is rejected

- **WHEN** a client POSTs to refresh the quota of a provider whose quota pool is in backoff
- **THEN** the response status is `429`, the response carries the remaining backoff duration, and no request is sent to the provider

#### Scenario: a failed refresh keeps the previous values

- **WHEN** a client POSTs to refresh the quota of a provider whose interface returns an error
- **THEN** the quota data file still holds the provider's previous entry

### Requirement: the user-level Claude configuration files can be edited

The console MUST expose exactly two editable Claude configuration files: `~/.claude/settings.json` and `~/.claude/CLAUDE.md`.

`GET /api/claude/files` MUST return the identifiers and metadata of those files. Each returned `path` MUST be expressed in tilde form — the user's home directory prefix replaced by `~` — and MUST NOT contain the absolute home directory path. The tilde form MUST keep the platform's own path separator, so a Windows home directory of `C:\Users\me` yields `~\.claude\settings.json` rather than a slash-normalized path.

`GET /api/claude/file/{id}` MUST return the file's text and an entity tag derived from its content.

`PUT /api/claude/file/{id}` MUST replace the file's content. It MUST require an `If-Match` header carrying the entity tag returned by the read, MUST respond `409` when the tag does not match, MUST validate `settings.json` against the known-key schema and respond `400` when validation fails, MUST back up the previous content, and MUST write atomically.

The console MUST NOT expose `~/.claude.json`, any project-level configuration file, or any file outside the two listed above. Reading and writing those files MUST continue to use the real absolute path; the tilde form is a presentation of the path, not its replacement.

#### Scenario: the file list reports the two editable files

- **WHEN** a client requests `GET /api/claude/files`
- **THEN** the response holds exactly two entries, for `settings.json` and `CLAUDE.md`

#### Scenario: the file list reports tilde-form paths

- **WHEN** a client requests `GET /api/claude/files`
- **THEN** each entry's `path` begins with `~` and does not contain the user's home directory path

#### Scenario: the tilde form keeps the platform's path separator

- **WHEN** a client requests `GET /api/claude/files` on a host whose home directory is `C:\Users\me`
- **THEN** the reported path for the settings file is `~\.claude\settings.json`

#### Scenario: saving a settings file with a stale tag is rejected

- **WHEN** a client PUTs `settings.json` with an `If-Match` tag that no longer matches the file's content
- **THEN** the response status is `409` and the file's content is unchanged

#### Scenario: an invalid settings file is rejected before writing

- **WHEN** a client PUTs a `settings.json` whose known key has the wrong type
- **THEN** the response status is `400` and the file's content is unchanged

#### Scenario: saving a markdown file backs up the previous content

- **WHEN** a client successfully saves `CLAUDE.md`
- **THEN** a copy of the previous content exists in the backup directory

#### Scenario: an unknown file identifier is rejected

- **WHEN** a client requests `GET /api/claude/file/anything-else`
- **THEN** the response status is `404`

### Requirement: write operations are confined to a path allowlist

No endpoint MUST write to a location derived from a client-supplied path.

A provider name MUST match `^[A-Za-z0-9._-]{1,64}$` and MUST NOT contain a path separator or a parent-directory reference. The provider's file path MUST be composed from the providers directory and that name, and MUST be verified to lie inside the providers directory.

The Claude file endpoints MUST accept only the two known identifiers and MUST map them to fixed paths.

A request naming anything else MUST be rejected without touching the filesystem.

#### Scenario: a traversal attempt is rejected

- **WHEN** a client requests `GET /api/providers/..%2F..%2Fetc%2Fpasswd`
- **THEN** the response status is `404` and no file outside the providers directory is read

#### Scenario: a provider name with a separator is rejected

- **WHEN** a client requests `GET /api/providers/foo%2Fbar`
- **THEN** the response status is `404`

### Requirement: write operations require a same-origin JSON request

Every endpoint that modifies a file MUST require the request's content type to be `application/json`, and MUST reject a request whose `Origin` header is present and does not match the request's own origin.

This guards the console against cross-site requests that ride on the user's session cookie.

#### Scenario: a cross-origin write is rejected

- **WHEN** a client sends a write request with `Content-Type: application/json` and an `Origin` header naming another site
- **THEN** the response status is `403` and nothing is written

#### Scenario: a write without a JSON content type is rejected

- **WHEN** a client sends a write request whose content type is `application/x-www-form-urlencoded`
- **THEN** the response status is `415` and nothing is written

### Requirement: writes are backed up before they are applied

Every write to a configuration file MUST copy the file's current content into the backup directory before the new content is written, and the backup MUST be kept under the launcher's own backup directory.

Backups MUST be retained for a bounded number of versions per file, older ones being removed, so that the backup directory does not grow without bound.

#### Scenario: every write leaves a backup

- **WHEN** a client successfully writes any editable configuration file
- **THEN** a copy of that file's content before the write exists in the backup directory

#### Scenario: old backups are pruned

- **WHEN** more backups exist for a file than the retention limit
- **THEN** the oldest backups for that file are removed and the newest are kept

### Requirement: the editors fill the available height without falling below their current size

Both editing surfaces — the provider configuration editor and the Claude file editor — MUST grow to occupy the height left over after the surrounding content, so that a tall window does not leave a small editor stranded in the middle of the page.

Neither editor's visible height MUST fall below the height it had before this change. On a window too short to hold the surrounding content, the page MUST scroll instead of shrinking the editor below that floor.

#### Scenario: the editor grows with the window

- **WHEN** the window grows by 200 pixels while the surrounding content stays the same size
- **THEN** the editor grows by at least 150 pixels

#### Scenario: a short window does not shrink the editor

- **WHEN** the window is too short to hold the surrounding content and the editor together
- **THEN** the editor's visible height is at least 340 pixels and the page scrolls

### Requirement: the editing surfaces carry their actions in a title row

Each editing surface MUST present a title row directly above the editor. That row MUST carry the surface's type label — JSON for a provider configuration, JSON or Markdown for a Claude file — a slot for the status message of the last action, and the actions themselves.

The status slot MUST be present whether or not an action has produced a message, and MUST stay empty until one does.

The actions MUST appear in that row, and MUST NOT be repeated in a row beneath the editor.

The action that saves the edited content MUST come first among the actions.

#### Scenario: the title row carries the label, the status slot and the actions

- **WHEN** the user opens a provider's configuration
- **THEN** the row above the editor shows the JSON label, an empty status slot and the save, copy and delete actions

#### Scenario: the status slot fills after an action

- **WHEN** a save succeeds
- **THEN** the status slot in the title row shows the confirmation, and no action row appears beneath the editor

#### Scenario: the save action comes first

- **WHEN** an editing surface's title row lists its actions
- **THEN** the save action is the first one listed

### Requirement: the quota card's refresh action sits at the far right of its title row

The quota card's refresh action MUST be placed at the far right of the quota card's title row, separated from the quota label.

#### Scenario: the refresh action is separated from the label

- **WHEN** the quota card is rendered
- **THEN** the refresh action is at the right end of the card's title row, not next to the quota label

### Requirement: the editors' height is determined by the layout

An editor's height MUST be determined by the page layout. The user MUST NOT be able to change it by dragging the editor's edge, because a manually resized height breaks the layout's distribution of the remaining height.

#### Scenario: the editor cannot be dragged to a different height

- **WHEN** the user drags the editor's bottom edge
- **THEN** the editor's height does not change

### Requirement: a late response never lands on a newly selected object

When the user selects a different provider or a different Claude file, a request still in flight for the previously selected object MUST NOT write into the newly selected object's view — neither into its editor content, nor into its status message, nor into its quota card.

#### Scenario: a late response does not overwrite the new editor

- **WHEN** the user switches from one provider to another while the first provider's configuration is still being fetched
- **THEN** the second provider's editor shows the second provider's configuration and the first provider's late response changes nothing

### Requirement: a failed quota refresh leaves the refresh action in place

When a manual quota refresh fails, the quota card MUST keep its title row and its refresh action, so that the user can try again without leaving the provider. Only the card's content area may report the failure.

#### Scenario: the refresh action survives a failure

- **WHEN** a manual quota refresh fails
- **THEN** the quota card still shows its refresh action and the failure is reported in the card's content area

### Requirement: the usage view keeps scrolling as a page

The console MUST NOT change how the usage view scrolls. For that view the page element MUST remain the scroll container: the content area MUST NOT scroll on its own.

#### Scenario: the usage view scrolls the page

- **WHEN** the usage view's content is taller than the window
- **THEN** the page element scrolls and the content area does not scroll on its own

