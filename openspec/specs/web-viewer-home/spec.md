# web-viewer-home Specification

## Purpose
Shape the Core-hosted Viewer's Home: library-by-library shelves instead of one combined new-items shelf, a Home hero, and card frames that show landscape art without clipping it.

## Requirements

### Requirement: Home presents library shelves

The desktop Viewer SHALL retain the manifest's Continue Watching shelf and
present recent additions in separate library shelves. It SHALL render a
nonempty shelf when the library is returned by the `libraries` query, its
`showOnHome` field is anything other than `false`, and its scoped feed succeeds.

#### Scenario: Recently added items span libraries

- **WHEN** the scoped Home feeds for two libraries return recent items
- **THEN** each Home-visible library has its own shelf, including libraries
  with no items in the aggregate Home feed's first page
- **AND** libraries hidden from Home or whose scoped feeds return no items have no shelf

#### Scenario: Recent shelves exceed the layout budget

- **WHEN** more libraries have recent items than Home can display within the
  24-Section limit
- **THEN** the Viewer pages the library shelves within the remaining Section
  budget, counting each rendered library shelf separately

#### Scenario: The viewer opens a library shelf's See All

- **WHEN** the viewer opens See All on a Home library shelf
- **THEN** the Viewer pages the selected library's recent additions using its
  library ID
- **AND** it stores only the library ID in history and uses the current Home
  visibility response to resolve the heading and authorize the scoped feed

#### Scenario: A viewer returns to a library shelf after relock

- **WHEN** the viewer returns to See All and the library is absent from the
  current Home visibility response
- **THEN** neither its heading nor its items appear

#### Scenario: One library feed is unavailable

- **WHEN** a scoped feed fails with a connection error
- **THEN** successful, Home-visible shelves remain visible

#### Scenario: Viewer session cannot read library visibility

- **WHEN** the `libraries` query requires an Admin Session
- **THEN** the Viewer retains the server-filtered aggregate Recently Added shelf

### Requirement: Home features available artwork

The desktop Viewer SHALL show a hero when an eligible recent item has loadable
artwork among the recent additions shown on Home. Home card
frames SHALL follow the loaded artwork's intrinsic aspect ratio and use the
class-based fallback when artwork is absent or fails. The Home artwork hero
SHALL render outside the 24-Section layout budget.

#### Scenario: Home has mixed artwork

- **WHEN** the shelves include square, landscape and portrait artwork
- **THEN** the Viewer displays each at its intrinsic aspect ratio on Home and
  in See All from a Home shelf

#### Scenario: All 24 manifest sections and the hero are available

- **WHEN** artwork loads for the Home hero and all 24 manifest sections are populated
- **THEN** Home retains all 24 sections alongside the hero

#### Scenario: No eligible artwork loads

- **WHEN** no recent item has loadable artwork for the hero
- **THEN** Home shows its available shelves without a hero

#### Scenario: An artwork request stalls

- **WHEN** the hero's current image fails or does not settle within its attempt limit
- **THEN** the Viewer tries the next eligible artwork

#### Scenario: Shelves load out of order

- **WHEN** the current hero loads before other Home feeds settle
- **THEN** it keeps its artwork and keyboard focus while its item remains eligible

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
