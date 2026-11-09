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

The rendered values MUST use the same thresholds, the same bar rendering and the same value formats as the terminal status line, so that the console and the status line never disagree about the same provider.

#### Scenario: a fresh quota record is rendered

- **WHEN** a client requests the quota of a provider that has a freshly fetched record with two dimensions
- **THEN** the response carries both dimensions and reports the record as not stale

#### Scenario: a missing record is not reported as unlimited

- **WHEN** a client requests the quota of a provider that has no record in the quota data file
- **THEN** the response reports that no quota was collected, and the console does not render an empty or unlimited quota bar

#### Scenario: a stale record is marked

- **WHEN** a client requests the quota of a provider whose record is older than the freshness threshold
- **THEN** the response marks the record as stale and carries the fetch timestamp

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

`GET /api/claude/files` MUST return the identifiers and metadata of those files.

`GET /api/claude/file/{id}` MUST return the file's text and an entity tag derived from its content.

`PUT /api/claude/file/{id}` MUST replace the file's content. It MUST require an `If-Match` header carrying the entity tag returned by the read, MUST respond `409` when the tag does not match, MUST validate `settings.json` against the known-key schema and respond `400` when validation fails, MUST back up the previous content, and MUST write atomically.

The console MUST NOT expose `~/.claude.json`, any project-level configuration file, or any file outside the two listed above.

#### Scenario: the file list reports the two editable files

- **WHEN** a client requests `GET /api/claude/files`
- **THEN** the response holds exactly two entries, for `settings.json` and `CLAUDE.md`

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

