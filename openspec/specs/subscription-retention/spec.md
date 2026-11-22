# subscription-retention Specification

## Purpose
Apply Subscription retention rules in receipt and feed order, rechecking eligibility so Media move only when every referencing Subscription permits it.

## Requirements

### Requirement: Keep-last-N uses receipt and feed order

Keep-last-N SHALL retain the N most recently seen distinct eligible Media. For entries with the same receipt time, an earlier position in the feed SHALL rank ahead of a later position. A receipt with no recorded position SHALL tie with every entry of its receipt time, and a tied group at the cutoff SHALL be kept whole. Favorites, Keep flags and feed-scoped pending imported stars SHALL be excluded from the count.

#### Scenario: One poll brings in more entries than the limit

- **WHEN** one poll adds more unprotected Media than the limit
- **THEN** one sweep keeps the first N in feed order and moves the remaining Media to recoverable Trash

#### Scenario: One legacy poll ties at the cutoff

- **WHEN** receipts with no feed position share one receipt time and the group straddles the keep-last-N cutoff
- **THEN** retention keeps every Media in the group

### Requirement: Retention rechecks eligibility before moving Media

Subscription retention SHALL decide and move each Media in one catalog write transaction, and a Favorite or Keep write SHALL be serialised with it. A Favorite, Keep flag or feed-scoped pending imported star SHALL protect the Media from both retention policies. Each move SHALL record an actorless `media.trash` activity entry.

#### Scenario: An imported star awaits replay

- **WHEN** a pending imported star resolves through the Subscription and entry GUID to a live file of the Media
- **THEN** retention leaves the Media available for Favorite replay

#### Scenario: A second sweep sees previously moved Media

- **WHEN** retention moves an eligible Media to Trash and runs again
- **THEN** the activity log has one actorless `media.trash` entry for that move

### Requirement: Every referencing Subscription permits the move

Every Subscription that references a Media SHALL permit its move to Trash: keep-last-N only past its cutoff, delete-after-played only when it has at least one following Profile and every such Profile has played the Media, and keep-all never.

#### Scenario: Two Subscriptions reference Versions of the same Media

- **WHEN** both Subscriptions use delete-after-played and a follower of either Subscription has yet to play the Media
- **THEN** retention leaves the Media available

#### Scenario: A keep-all Subscription references the Media

- **WHEN** a keep-last-N Subscription's Media past its cutoff is also referenced by a keep-all Subscription
- **THEN** retention leaves the Media available
