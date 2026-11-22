## ADDED Requirements

### Requirement: Catalogue entries report the Profile's played state

Each Subscription catalogue entry SHALL carry `played`, true when the calling Profile has played
the entry's Media, and false without a Profile.

#### Scenario: Played entry is reported

- **WHEN** the Profile marks one catalogue entry played
- **THEN** that entry reports `played` true and the others report false

### Requirement: The web catalogue filters, sorts and acts on entries

The web feed catalogue SHALL filter entries by All, Unplayed or Downloaded and sort them Newest or
Oldest by publish time, falling back to feed order, with undated entries after dated ones in
Newest and before them in Oldest. Each row SHALL offer Play and Mark played or unplayed, and SHALL
announce failures.

#### Scenario: Unplayed filter hides a played entry

- **WHEN** the listener marks an entry played and picks Unplayed
- **THEN** that entry leaves the list

### Requirement: Any viewer reads its own follow state for a Subscription

The `subscriptionFollow(subscriptionID)` query SHALL return the calling Profile's follow state and
new-entry route for a Subscription that Profile can see, without manager rights.

#### Scenario: Viewer reads back a saved route

- **WHEN** a viewer follows a Subscription and sets its new-entry route to `queue-end`
- **THEN** `subscriptionFollow` reports it followed with route `queue-end`

### Requirement: A followed feed offers a new-entry route selector

The web page of a followed feed SHALL offer a "New entries go to" selector with New, End of queue
and Front of queue, saving the choice through `setFollowNewEntryRoute` and announcing a failure.

#### Scenario: Route change is saved

- **WHEN** the listener picks End of queue
- **THEN** the follow's new-entry route becomes `queue-end`
