## MODIFIED Requirements

### Requirement: Home offers the next item after a finished one

The Home layout SHALL place an Up Next shelf after Continue Watching. Its source SHALL yield, for
each ordered container the Profile recently engaged with, the first unfinished child, advancing into
the next ordered sibling container when the container is finished. It SHALL yield nothing for a
container group holding a resumable child, which Continue Watching shows. Only Entity kinds of the
container archetype SHALL count as containers. The Up Next shelf SHALL offer only video Media, and
SHALL NOT offer audio Media.

#### Scenario: The viewer finishes an episode

- **WHEN** the Profile finishes the first episode of a season
- **THEN** Home's Up Next shelf offers the second episode

#### Scenario: The viewer finishes the last episode of a season

- **WHEN** the Profile finishes the last episode of season one and season two exists
- **THEN** Up Next offers season two's first episode

#### Scenario: A credited person is not a container

- **WHEN** the Profile finishes one film crediting a person whose credits carry a billing order
- **THEN** Up Next offers no other film from that person

#### Scenario: Music does not mix into Up Next

- **WHEN** the Profile finished an episode and played the first track of an album
- **THEN** Up Next offers the next episode and not the album's next track

## ADDED Requirements

### Requirement: Home offers the next track in a separate Continue Listening shelf

The Home layout SHALL place a Continue Listening shelf after Up Next. It SHALL follow the Up Next
rules for audio Media only. The `next-up` Source SHALL filter by the media class its value names
(`video` or `audio`), SHALL offer every class when it has no value, and SHALL yield nothing for any
other value. The Profile SHALL be able to hide or reorder Up Next and Continue Listening
independently with its Home layout.

#### Scenario: The listener finishes a track

- **WHEN** the Profile finishes the first track of an album
- **THEN** Continue Listening offers the second track and Up Next does not

#### Scenario: The Profile hides Continue Listening

- **WHEN** the Profile saves a Home layout hiding Continue Listening
- **THEN** Home shows Up Next without Continue Listening

#### Scenario: A plugin section asks for every class

- **WHEN** a section reads the `next-up` Source with no value
- **THEN** it offers the next child of both video and audio containers
