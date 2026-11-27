# provider-list-rendering Specification

## Purpose
TBD - created by archiving change provider-list-display-improvements. Update Purpose after archive.
## Requirements
### Requirement: Provider config supports optional note and order fields
The provider configuration file (`~/.cc-launcher/providers/<name>.json`) MUST support two optional top-level fields alongside `env`:
- `note` (string): a one-line human-readable description, displayed in the TUI list next to the provider name.
- `order` (number): an integer sort weight. Providers are listed in ascending order of `order`; ties (including missing values) fall back to ascending alphabetical order by `<name>`.

When `note` or `order` is absent, the field defaults to `note = ""` and `order = 0`. Existing configuration files without these fields MUST continue to work without modification.

#### Scenario: Config without note or order loads successfully
- **WHEN** `<name>.json` is `{"env": {"ANTHROPIC_AUTH_TOKEN": "sk-xxx"}}` (no `note` / `order`)
- **THEN** `Manager.List()` returns an entry with `Name = "<name>"`, `Note = ""`, `Order = 0`, and `Error = ""`

#### Scenario: Config with note loads into Info.Note
- **WHEN** `<name>.json` is `{"env": {...}, "note": "国内 / 按量计费"}`
- **THEN** the corresponding `Info` entry has `Note = "国内 / 按量计费"` and `Error = ""`

#### Scenario: Config with order loads into Info.Order
- **WHEN** `<name>.json` is `{"env": {...}, "order": 10}`
- **THEN** the corresponding `Info` entry has `Order = 10` and `Error = ""`

#### Scenario: Config with non-numeric order marks only that provider as Error
- **WHEN** `<name>.json` is `{"env": {...}, "order": "ten"}` and another `<other>.json` is valid
- **THEN** `<name>`'s `Info.Error` is non-empty, but `<other>`'s `Info.Error = ""` and `<other>`'s `Note` / `Order` are still parsed correctly

### Requirement: Manager.List() returns providers sorted by Order then Name
`Manager.List()` MUST return entries sorted by `(Order asc, Name asc)` using a stable sort. Entries where `Error != ""` MUST be included in the result (one bad provider MUST NOT hide others) and MUST be sorted by the same rule.

#### Scenario: Three providers with distinct order
- **WHEN** `~/.cc-launcher/providers/` contains `alpha.json` (order=20), `bravo.json` (order=5), `charlie.json` (order=10), all with valid `env` blocks
- **THEN** `Manager.List()` returns entries in order: `bravo, charlie, alpha` (regardless of underlying filesystem read order)

#### Scenario: Providers with same order fall back to name
- **WHEN** `bravo.json` and `alpha.json` both have `order = 5`
- **THEN** `Manager.List()` returns `alpha, bravo` (alphabetical tiebreak)

#### Scenario: Providers without order sort by name
- **WHEN** `charlie.json` and `alpha.json` have no `order` field (default `0`)
- **THEN** `Manager.List()` returns `alpha, charlie`

#### Scenario: Errored providers are included and sorted normally
- **WHEN** `bravo.json` has invalid JSON and `alpha.json` has valid `order = 0` (default; no `order` field)
- **THEN** `Manager.List()` returns `alpha` first, then `bravo` (with `Error` populated); neither entry is silently dropped. The order between them falls back to `Name` ascending (both have `Order = 0`).

### Requirement: TUI pre-selected provider is marked with a dim asterisk
In the TUI provider list, the entry that matches the resolved pre-selection (cwd binding or active provider) MUST be rendered with a dim-styled `[*]` marker in the leading two-character slot. Non-selected entries MUST be rendered with `[ ]`. The pre-selected marker uses `dimStyle` (grey); the cursor row continues to be rendered with `cursorStyle` (highlight) and the `[*]` / `[ ]` distinction remains visible (cursor highlights the entire line; the pre-selected `[*]` is visible in the leading slot when the pre-selected entry is not the cursor).

The trailing "默认" tag SHALL NOT be rendered in the provider list (it is replaced by the leading dim `[*]` marker).

#### Scenario: Pre-selected provider not at cursor shows dim [*]
- **WHEN** the TUI pre-selects `deepseek` and the cursor starts on `deepseek` (top of list), and the user presses `↓` once to move the cursor to `anthropic`
- **THEN** the `deepseek` row's leading slot shows `[*]` rendered in `dimStyle`, the `anthropic` row's leading slot shows `[ ]`, and the cursor row (`anthropic`) is wrapped in `cursorStyle`

#### Scenario: Pre-selected provider is the cursor
- **WHEN** the TUI pre-selects `deepseek` and the cursor is on `deepseek`
- **THEN** the `deepseek` row's leading slot shows `[*]` (its color is overridden by the `cursorStyle` row-highlight; the marker shape `[*]` is still recognizable), and no other row shows `[*]`

#### Scenario: No pre-selection available
- **WHEN** no `dir_bindings[wd]` exists and `active_provider` is empty
- **THEN** no row's leading slot shows `[*]`; all rows show `[ ]` until the user moves the cursor

### Requirement: TUI provider list shows note after name
When a provider's `Info.Note` is non-empty, the TUI MUST render the note text after the provider name, separated by two spaces, in `dimStyle`. When `Note` is empty, the row MUST NOT show a note placeholder. An errored provider (`Error != ""`) MUST continue to render its `Error` text (preserving existing behavior).

#### Scenario: Provider with note displays note in dim style
- **WHEN** the TUI renders a provider whose `Note = "国内 / 按量计费"` and `Error = ""`
- **THEN** the rendered line contains the provider name followed by two spaces and the text `国内 / 按量计费` rendered in `dimStyle`

#### Scenario: Provider without note shows no note slot
- **WHEN** the TUI renders a provider whose `Note = ""` and `Error = ""`
- **THEN** the rendered line contains only the leading marker and the provider name; no trailing note text is appended

#### Scenario: Errored provider keeps error rendering precedence
- **WHEN** the TUI renders a provider whose `Note = "x"` and `Error = "JSON 解析失败"`
- **THEN** the rendered line shows the `Error` text in `dimStyle` after the name; the `Note` is NOT rendered (error takes precedence; avoiding two `dimStyle` segments in the same row)

### Requirement: TUI order does not change selection semantics
The visual ordering change MUST NOT alter the TUI selection contract: pressing `Enter` on a cursor row still selects that row's `Info.Name`; the pre-selected provider is still the one whose `Name` matches the resolved pre-selection (cwd binding first, then `active_provider`).

#### Scenario: Order affects list position only
- **WHEN** `bravo` (order=5) sorts above `alpha` (order=20), and the pre-selection resolves to `alpha`
- **THEN** the cursor is positioned on `alpha` (row index 1, not 0); pressing `Enter` selects `alpha`

### Requirement: Provider configuration files accept JSONC comments

ccl MUST accept `//` line comments and `/* */` block comments in a provider configuration file (`~/.cc-launcher/providers/<name>.json`), and MUST parse such a file as if the comments were absent.

ccl MUST treat every place that reads a provider configuration file as accepting comments, so that a provider with comments is recognized identically wherever it is read. A configuration that is accepted by the listing MUST NOT be rejected by the quota lookup, and vice versa.

ccl MUST NOT treat a comment marker that appears inside a string value as a comment. A URL such as `https://ark.cn-beijing.volces.com/api/plan` contains `//` and MUST be parsed as a plain string value.

ccl MUST report the position of a parse error against the original file, so that the line number of the reported error MUST be the line the error is on in the file the user wrote.

ccl MUST preserve the comments of a provider configuration file when it writes that file back, so that a configuration edited in the web console keeps its comments.

ccl MUST NOT deliver comments to Claude Code. The settings file that ccl delivers is built by re-serializing the configuration, so a comment MUST NOT become a key of the delivered settings.

ccl MUST NOT accept a trailing comma. A configuration with a trailing comma MUST be reported as a parse failure, because that is not valid JSON with comments and a trailing comma is most often the result of a half-finished edit.

ccl MUST report a configuration that carries comments as a valid configuration, and MUST NOT mark it as an error in the provider listing.

#### Scenario: a config with a line comment loads
- **WHEN** `<name>.json` is `{"env": {...}, "note": "国内 / 按量计费"}` with a `//` comment on its own line
- **THEN** the corresponding `Info` entry has `Note = "国内 / 按量计费"` and `Error = ""`

#### Scenario: a config with a block comment loads
- **WHEN** `<name>.json` contains a `/* ... */` block comment above the `env` key
- **THEN** the configuration parses and its `env` values are read as usual

#### Scenario: a comment between entries of an object is ignored
- **WHEN** `<name>.json` is `{"env": {"A": "1"}, /* 说明 */ "note": "x"}`
- **THEN** the configuration parses and the `note` is `"x"`

#### Scenario: a URL inside a string is not a comment
- **WHEN** `<name>.json` contains `"ANTHROPIC_BASE_URL": "https://ark.cn-beijing.volces.com/api/plan"`
- **THEN** the configuration parses and the base URL is read as the whole string, including the `//`

#### Scenario: a comment marker inside a string is not a comment
- **WHEN** `<name>.json` contains `"note": "use /* this */ literally"`
- **THEN** the configuration parses and the note is `use /* this */ literally`

#### Scenario: an escaped quote inside a string does not end the string
- **WHEN** `<name>.json` contains `"note": "say \"hi\" // now"`
- **THEN** the configuration parses and the note is `say "hi" // now`

#### Scenario: the error line points at the original file
- **WHEN** `<name>.json` has a syntax error on its tenth line and comments on the lines above it
- **THEN** the reported error names line ten

#### Scenario: a trailing comma is rejected
- **WHEN** `<name>.json` is `{"env": {"A": "1"},}`
- **THEN** the configuration is reported as a parse failure

#### Scenario: comments survive an edit in the web console
- **WHEN** a provider configuration carries a comment and the user saves it from the web console without changing the comment
- **THEN** the saved file still carries the comment

#### Scenario: the comments of a provider are not delivered to Claude Code
- **WHEN** a provider configuration carries a comment and ccl launches Claude Code with that provider
- **THEN** the delivered settings file carries no comment and no key that holds one

### Requirement: The provider editor in the web console accepts comments

The JSON editor of the provider view in the web console MUST accept a configuration that carries comments, and MUST NOT report it as a parse failure nor disable the save action because of them.

The editor MUST report the position of a parse failure against the original file, in the same way as the backend does.

The syntax highlighting of the editor MUST recognize a comment as a comment, so that a quotation mark inside a comment does not start a string that runs past the end of the comment.

#### Scenario: a config with comments loads into the editor
- **WHEN** a provider configuration carries comments and the user opens it in the web console
- **THEN** the editor shows the comments and the save action is enabled

#### Scenario: saving from the editor keeps the comments
- **WHEN** the user saves a provider configuration that carries comments from the web console
- **THEN** the saved file still carries the comments

#### Scenario: a quotation mark inside a comment does not break the highlighting
- **WHEN** a comment contains a quotation mark
- **THEN** the text after the comment is highlighted as it would be without the comment

