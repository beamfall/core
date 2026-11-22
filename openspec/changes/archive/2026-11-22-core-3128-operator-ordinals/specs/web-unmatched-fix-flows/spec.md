## ADDED Requirements

### Requirement: Fix Match supports a manual ordinal

Managers SHALL be able to preview, set and clear an ordinal on visible Media supported by Fix Match.
The ordinal SHALL persist by Media ID and take precedence over the parser in identification lookups.
A supplied sequence SHALL have at most 32 characters and a supplied index SHALL be nonnegative.

#### Scenario: An operator previews and applies an ordinal

- **WHEN** the manager supplies a sequence or index to Fix Match preview or apply
- **THEN** the lookup uses the supplied ordinal, with an omitted sequence or index defaulting to empty or zero
- **AND** the Apply commits the ordinal with the replacement metadata and lists it among the item's locked values
- **AND** an Apply refused before that commit (a failed lookup, invalid input, or hydration the server cannot run) changes neither the ordinal nor the Media's Entity links
- **AND** Entity links follow the commit, so a retry after a failed link write converges

#### Scenario: An operator replaces a match or clears the ordinal

- **WHEN** the manager applies a match without an ordinal or lock selection
- **THEN** the same candidate keeps the saved ordinal and a different candidate clears it
- **AND** explicitly clearing the ordinal restores parser lookup, with a standalone clear preserving the fingerprint pin
