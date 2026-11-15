## ADDED Requirements

### Requirement: Library browse reports where each title initial starts

When `libraryItems` orders by `title`, the response SHALL include `letters`, one entry per title
initial present in the filtered set, each with the `offset` of the first item carrying that initial
in the requested order and the `count` of items carrying it. The initial SHALL be the title's first character upper-cased when
it is a letter, and `#` otherwise. Entries SHALL be listed in order of increasing offset. For any
other sort, `letters` SHALL be empty.

#### Scenario: Jump offsets match the title order

- **WHEN** a client requests `libraryItems` with `sort: "title"` for a library whose visible titles
  start with "a", "B", "b" and "3"
- **THEN** `letters` is `#` at 0 with count 1, `A` at 1 with count 1 and `B` at 2 with count 2, and
  requesting `offset: 2` returns the first "B" item

#### Scenario: Filters narrow the letters

- **WHEN** a watch-state filter leaves no item starting with "B"
- **THEN** `letters` has no `B` entry and the remaining offsets count only the filtered set

#### Scenario: Other sorts carry no letters

- **WHEN** a client requests `libraryItems` with `sort: "year"`
- **THEN** `letters` is empty

### Requirement: Web browse offers a letter rail on a title sort

The web browse view SHALL show a letter rail while the order is by title and the filtered set holds
more than 60 items. The rail SHALL offer `#` and A to Z; letters absent from the filtered set SHALL
be disabled and skipped by focus, and each letter's accessible name SHALL give its title count.
Choosing a letter SHALL bring its first item into view without loading earlier pages.

#### Scenario: Jump to a letter

- **WHEN** a user activates `S` on the rail of a title-sorted library
- **THEN** the grid scrolls to the first "S" item, only the pages in view load, and focus moves to
  that item

#### Scenario: Short or differently sorted lists

- **WHEN** the user sorts by year, or the filtered set holds 60 items or fewer
- **THEN** no letter rail is shown

#### Scenario: Current letter follows the scroll

- **WHEN** the user scrolls the grid until the first "M" item reaches the top
- **THEN** `M` is marked current on the rail

#### Scenario: Cramped viewport

- **WHEN** the viewport is narrower than 360px or shorter than 520px
- **THEN** a "Jump to" control replaces the rail and opens a sheet of the same letters

### Requirement: The letter rail is keyboard operable

The rail SHALL be a single tab stop with an accessible name. Activating a letter (click or Enter)
SHALL move focus to its first item. Arrow keys, Home and End SHALL move along enabled letters and
scroll the grid while focus stays on the rail. Typing a letter SHALL jump to it, or to the next
letter with titles and announce the substitution.

#### Scenario: Arrow keys scan without leaving the rail

- **WHEN** focus is on `A` and the user presses ArrowDown
- **THEN** focus moves to the next letter with titles and the grid scrolls to its first item

#### Scenario: Typed letter with no titles

- **WHEN** no title starts with `J` and the user types "j" on the rail
- **THEN** the rail jumps to the next letter with titles and announces that none start with J
