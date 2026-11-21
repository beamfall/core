## ADDED Requirements

### Requirement: Keep-last-N uses receipt and feed order

Keep-last-N SHALL retain the N most recently seen distinct eligible Media. For entries with the same receipt time, an earlier position in the feed SHALL rank ahead of a later position. Favorites, Keep flags and feed-scoped pending imported stars SHALL be excluded from the count.

#### Scenario: One poll brings in more entries than the limit

- **WHEN** one poll adds more unprotected Media than the limit
- **THEN** one sweep keeps the first N in feed order and moves the remaining Media to recoverable Trash

### Requirement: Retention rechecks eligibility before moving Media

Subscription retention SHALL recheck eligibility before each Trash write. A Favorite, Keep flag or feed-scoped pending imported star SHALL protect the Media from both retention policies. Every referencing Subscription with delete-after-played SHALL have at least one following Profile, and every such Profile SHALL have played the Media, before either policy may move it. Each move SHALL record an actorless `media.trash` activity entry.

#### Scenario: Two Subscriptions reference Versions of the same Media

- **WHEN** both Subscriptions use delete-after-played and a follower of either Subscription has yet to play the Media
- **THEN** retention leaves the Media available

#### Scenario: An imported star awaits replay

- **WHEN** a pending imported star resolves through the Subscription and entry GUID to a live file of the Media
- **THEN** retention leaves the Media available for Favorite replay

#### Scenario: A second sweep sees previously moved Media

- **WHEN** retention moves an eligible Media to Trash and runs again
- **THEN** the activity log has one actorless `media.trash` entry for that move
