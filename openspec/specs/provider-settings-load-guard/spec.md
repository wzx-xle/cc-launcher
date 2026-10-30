# provider-settings-load-guard Specification

## Purpose
TBD - created by archiving change provider-settings-passthrough. Update Purpose after archive.
## Requirements
### Requirement: ccl validates the delivered settings against Claude Code's settings schema before launching

Before writing the settings file, ccl MUST validate the settings object it is about to deliver against Claude Code's settings schema. A validation failure MUST stop the launch and MUST print which key is at fault and what type that key expects.

The validation rules MUST be:

- a key that ccl knows, whose value's type does not match the type the key expects (string, number, boolean, array or object), is a validation failure;
- a value inside the `env` block that is not a string is not a validation failure: Claude Code turns such a value into a string itself, so ccl MUST turn it into a string as well and print a warning;
- a value inside a known object key that has the wrong type is not a validation failure: ccl MUST drop that one field and print a warning, because Claude Code itself drops only that field;
- a value inside a known array key that is not of the type the array holds is not a validation failure: ccl MUST drop that one element and print a warning, because Claude Code itself tolerates such an element;
- a key ccl does not know is not a validation failure: ccl MUST deliver it unchanged, matching Claude Code's own behavior of ignoring an unknown top-level key.

The validation MUST NOT change any value that passes it, and MUST NOT change the shape of the delivered settings object beyond turning the `env` block's values into strings and dropping the tolerated bad fields and elements.

ccl MUST keep the schema table it validates against, and MUST record where that table came from and which Claude Code version it was taken from, so that it can be refreshed when Claude Code adds keys.

#### Scenario: a known key with the wrong type stops the launch
- **WHEN** the selected provider's configuration contains `"model": {"name": "x"}` and the user runs `ccl`
- **THEN** ccl prints an error naming `model` and stating that a string is expected, and does not launch Claude Code

#### Scenario: a non-string value in the env block does not stop the launch
- **WHEN** the selected provider's configuration contains `"env": {"API_TIMEOUT_MS": 3000000}` and the user runs `ccl`
- **THEN** ccl prints a warning naming `env.API_TIMEOUT_MS`, delivers that value as the string `3000000`, and launches Claude Code

#### Scenario: an unknown key is delivered unchanged
- **WHEN** the selected provider's configuration contains `"someNewKey": {"a": 1}` and the user runs `ccl`
- **THEN** ccl launches Claude Code and the delivered settings contain `someNewKey` with that value

#### Scenario: a bad element in a known array is dropped with a warning
- **WHEN** the selected provider's configuration contains `"permissions": {"allow": [123, "Bash(ls:*)"]}` and the user runs `ccl`
- **THEN** ccl prints a warning that the non-string element was dropped, and the delivered settings contain `permissions.allow` with only `Bash(ls:*)`

#### Scenario: a valid configuration is delivered unchanged
- **WHEN** the selected provider's configuration contains `"model": "some-model"` and `"env": {"ANTHROPIC_BASE_URL": "https://api.example.com"}`
- **THEN** ccl launches Claude Code and the delivered settings contain both keys exactly as configured

### Requirement: ccl confirms that the Claude Code in use accepts the --settings parameter

ccl MUST confirm before launching that the Claude Code it is about to run accepts the `--settings` parameter. When it does not, ccl MUST print an error that says the provider's configuration cannot be delivered and tells the user to update Claude Code, and MUST NOT launch Claude Code.

The result of that confirmation MUST be cached, keyed by the path, size and modification time of the Claude Code executable, so that a launch does not repeat the check when the executable has not changed.

#### Scenario: an unsupported Claude Code stops the launch
- **WHEN** the Claude Code in use does not accept `--settings` and the user runs `ccl`
- **THEN** ccl prints an error that the provider's configuration cannot be delivered and suggests updating Claude Code, and does not launch Claude Code

#### Scenario: a supported Claude Code launches normally
- **WHEN** the Claude Code in use accepts `--settings` and the user runs `ccl`
- **THEN** ccl launches Claude Code with the `--settings` parameter

#### Scenario: the check is not repeated for an unchanged executable
- **WHEN** the user runs `ccl` twice and the Claude Code executable has not changed in between
- **THEN** the second launch does not perform the check again

