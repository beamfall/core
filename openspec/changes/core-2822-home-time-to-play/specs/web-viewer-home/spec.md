## ADDED Requirements

### Requirement: Home offers the next item after a finished one

The Home layout SHALL place an Up Next shelf after Continue Watching. Its source SHALL yield, for
each ordered container the Profile recently engaged with, the first unfinished child, advancing into
the next ordered sibling container when the container is finished. It SHALL yield nothing for a
container group holding a resumable child, which Continue Watching shows. Only Entity kinds of the
container archetype SHALL count as containers.

#### Scenario: The viewer finishes an episode

- **WHEN** the Profile finishes the first episode of a season
- **THEN** Home's Up Next shelf offers the second episode

#### Scenario: The viewer finishes the last episode of a season

- **WHEN** the Profile finishes the last episode of season one and season two exists
- **THEN** Up Next offers season two's first episode

#### Scenario: A credited person is not a container

- **WHEN** the Profile finishes one film crediting a person whose credits carry a billing order
- **THEN** Up Next offers no other film from that person

### Requirement: Recently Added folds by container without hiding other items

The Viewer SHALL read 100 recent items for a Recently Added shelf, fold items sharing a container
into one container card, and show up to 20 cards. A folded card SHALL count the items the Profile
has not finished and SHALL name no count when every item is finished.

#### Scenario: One show adds a whole season

- **WHEN** 40 episodes of one show are newer than 12 films
- **THEN** the shelf shows the show's card with "40 new" and all 12 films

#### Scenario: Every new item is finished

- **WHEN** every new item of a folded container is finished
- **THEN** its card names no count
