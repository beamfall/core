## ADDED Requirements

### Requirement: Catalogue entries report the Profile's played state

Each Subscription catalogue entry SHALL carry `played`, true when the calling Profile has played
the entry's Media, and false without a Profile.

#### Scenario: Played entry is reported

- **WHEN** the Profile marks one catalogue entry played
- **THEN** that entry reports `played` true and the others report false

### Requirement: The web catalogue filters, sorts and acts on entries

The web feed catalogue SHALL filter entries by All, Unplayed or Downloaded and sort them Newest or
Oldest by publish time, falling back to feed order. Each row SHALL offer Play, Play next (after the
current queue item) and Mark played or unplayed, and SHALL announce failures.

#### Scenario: Unplayed filter hides a played entry

- **WHEN** the listener marks an entry played and picks Unplayed
- **THEN** that entry leaves the list

### Requirement: A followed feed offers a new-entry route selector

The web page of a followed feed SHALL offer a "New entries go to" selector with New, End of queue
and Front of queue, saving the choice through `setFollowNewEntryRoute` and announcing a failure.

#### Scenario: Route change is saved

- **WHEN** the listener picks End of queue
- **THEN** the follow's new-entry route becomes `queue-end`
