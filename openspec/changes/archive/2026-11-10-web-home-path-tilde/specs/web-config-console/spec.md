## MODIFIED Requirements

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
