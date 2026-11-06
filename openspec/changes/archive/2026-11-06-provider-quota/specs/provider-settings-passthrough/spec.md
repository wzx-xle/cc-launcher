## MODIFIED Requirements

### Requirement: ccl delivers the provider's non-env top-level keys through the --settings parameter

When `ccl` launches Claude Code with a selected provider, every top-level key of that provider configuration other than `env` MUST be delivered through the `--settings` parameter, with the key name and the value preserved exactly as the provider wrote them. ccl MUST NOT rename the key, coerce the value, or wrap the value in another structure.

The keys `note`, `order` and `quota` MUST NOT be delivered: they are ccl's own metadata — the remark line and the sort order of the provider list, and the provider's quota query configuration (which adapter serves it and which quota endpoint to query) — not Claude Code settings.

The delivered keys MUST be merged into the same settings object that carries the provider's `env` block, the OTel environment and the launch context.

The delivered settings object MUST be deterministic: the same provider configuration MUST always produce the same settings content, so that the content-hash naming of the cache file reuses one file instead of accumulating a new one per launch.

When a delivered key collides with one of ccl's own keys in the same settings object, ccl's own key MUST win.

#### Scenario: the provider's model key is delivered
- **WHEN** the selected provider's configuration contains `"model": "some-model"` and the user runs `ccl`
- **THEN** the delivered settings contain the top-level key `model` with the value `some-model`

#### Scenario: the provider's permissions key is delivered
- **WHEN** the selected provider's configuration contains `"permissions": {"allow": ["Bash(ls:*)"]}` and the user runs `ccl`
- **THEN** the delivered settings contain the top-level key `permissions` with that object as its value

#### Scenario: ccl's provider metadata is not delivered
- **WHEN** the selected provider's configuration contains `"note": "a remark"`, `"order": 3` and `"quota": {"source": "newapi"}` and the user runs `ccl`
- **THEN** the delivered settings contain neither `note` nor `order` nor `quota`

#### Scenario: a provider with only an env block delivers the same as before
- **WHEN** the selected provider's configuration contains only an `env` block
- **THEN** the delivered settings contain the `env` block and no other provider key

#### Scenario: the delivered content is stable across launches
- **WHEN** the user runs `ccl` twice with the same provider and the same environment
- **THEN** both launches deliver settings content with the same keys in the same order

#### Scenario: ccl's own key wins a collision
- **WHEN** the selected provider's configuration contains a top-level key that ccl also sets in the delivered settings object
- **THEN** the delivered settings carry ccl's value for that key
