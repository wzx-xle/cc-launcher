## ADDED Requirements

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
