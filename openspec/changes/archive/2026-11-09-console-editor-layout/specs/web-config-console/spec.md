## ADDED Requirements

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
