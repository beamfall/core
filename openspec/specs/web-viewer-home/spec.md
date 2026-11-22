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

### Requirement: The Home hero names what it features

The Home hero SHALL be headed the way a card for its item is headed, so an item held by a container
names the container with the item's own line beneath it. It SHALL show the item's byline, year,
content rating, runtime and audience score where known, and SHALL NOT repeat any of them in its
summary. It SHALL take at most 340px of height at desktop width.

#### Scenario: An episode is featured

- **WHEN** Home features episode 9 of season 34 of "The Graham Norton Show", rated TV-14, 49 minutes, 2026, score 7.4
- **THEN** the hero is headed "The Graham Norton Show", reads "S34 · E9 · Episode 9" beneath it, and lists 2026, TV-14, 49 min and ★ 7.4

### Requirement: Home leads with resumable items

When the Home layout includes a Continue Watching shelf, the Viewer SHALL show
the Recently Added hero only after that shelf settles empty or failed. While
the shelf loads, the Viewer SHALL show neither the hero nor its placeholder.
When the shelf has items, it SHALL be the first Home block.

#### Scenario: Returning Profile opens Home

- **WHEN** the Continue Watching shelf loads with items
- **THEN** Home shows that shelf first and no Recently Added hero

#### Scenario: New Profile opens Home

- **WHEN** the Continue Watching shelf loads empty
- **THEN** Home shows the Recently Added hero above its shelves

#### Scenario: Continue Watching is still loading

- **WHEN** Recently Added artwork is ready before the Continue Watching shelf settles
- **THEN** Home shows no hero and no hero placeholder until the shelf settles

### Requirement: Remove from Continue Watching

On a shelf bound to the `continue-watching` Source, a Media card's More menu SHALL offer "Remove
from Continue Watching", and cards elsewhere SHALL NOT. Choosing it SHALL take the card off the row
at once, call `setContinueWatchingHidden` with `hidden: true`, and show the Undo notice naming the
item.

#### Scenario: Remove

- **WHEN** a viewer chooses "Remove from Continue Watching" on a card
- **THEN** the card leaves the row at once and the notice names the item and offers Undo

#### Scenario: Other shelves

- **WHEN** a viewer opens the More menu of a card on a Recently Added shelf
- **THEN** the menu does not offer "Remove from Continue Watching"

#### Scenario: Last card

- **WHEN** a viewer removes the only card on the row
- **THEN** the row disappears and Home treats it as an empty Continue Watching shelf

### Requirement: Continue Watching removal Undo and failure

Undo SHALL call `setContinueWatchingHidden` with `hidden: false` and return the card to the row. A
removed card SHALL stay off each row until that row holds a read started after the write settled,
and that read's answer SHALL stand. A failed write SHALL return the card and report the failure in
the notice, with no Undo. A removal or Undo started under one Profile SHALL NOT notify or write
under another.

#### Scenario: Undo

- **WHEN** a viewer chooses Undo on the removal notice
- **THEN** the card returns to the row

#### Scenario: Write fails

- **WHEN** the removal cannot be saved
- **THEN** the card returns to the row and the notice says the change did not save, with no Undo

#### Scenario: Played again

- **WHEN** a read of the row started after the removal settled lists the card again
- **THEN** the card shows on that row

#### Scenario: Profile switch

- **WHEN** the Profile changes while a removal is pending, or before its Undo
- **THEN** no notice shows for the new Profile and the Undo writes nothing
