## ADDED Requirements

### Requirement: A middle container names its ancestor

An Entity page for a container with playable Media of its own SHALL treat a related container of
another kind that holds at least as many items as an ancestor. The page SHALL name the first
ancestor above its title as a link to that ancestor's page, and SHALL NOT render the ancestor as a
related card rail.

#### Scenario: A child container opens

- **WHEN** a container with eight Media is related to a container of another kind holding twenty
- **THEN** the larger container's title links above the page title and no one-card rail shows it

### Requirement: A container's video Media list as detail rows

When a container's detail Layout only lists its own Media and those Media are not an audio
listing, the page SHALL list them as rows. Each row SHALL show the item's still, the parts of its
plugin subtitle its siblings do not share (without the title) and its runtime, a summary clamped to
two lines, and the title once. Activating the row SHALL open the item's detail page; a separate
control SHALL play it with the container's Media as the queue. Missing values SHALL be left out.

#### Scenario: Rows open detail and play separately

- **WHEN** the viewer clicks a row's title
- **THEN** the item's detail page opens
- **WHEN** the viewer presses the row's play control
- **THEN** playback starts with the container's Media as the queue

#### Scenario: Subtitle parts shared by every sibling are dropped

- **WHEN** every sibling's subtitle starts with the same part and ends with its own title
- **THEN** each row shows only its own differing part beside the runtime

### Requirement: On Deck names the next child once

The On Deck line SHALL name the next-up child by its plugin subtitle, with the title appended only
when the subtitle does not already contain it, and by its title alone when it has no subtitle.

#### Scenario: Subtitle already includes the title

- **WHEN** the next child's subtitle is "S1 · E1 · Countdown" and its title is "Countdown"
- **THEN** the line reads "Next up: S1 · E1 · Countdown"

### Requirement: A container lists similar containers

An Entity page for a container SHALL, after its people, list other Entities of the same kind that
share its related category or person Entities, ranked by the number shared and then by first
appearance. The rail SHALL be hidden when there are none and SHALL never list the page's own
Entity.

#### Scenario: Ranked by shared Entities

- **WHEN** one container shares a category and a person with the page and another shares only a category
- **THEN** the first ranks above the second

### Requirement: Browse catalog shelves are ranked

A browse Layout's shelf Sections over `entity-items` or `all-items` SHALL be ordered on the client:
items in progress first, then unstarted, then finished, newer year first within each tier, then
the server's order. Grids and See All SHALL keep the server's order.

#### Scenario: An in-progress container leads the shelf

- **WHEN** a shelf lists an unstarted, a finished and an in-progress container in title order
- **THEN** it renders the in-progress, the unstarted, then the finished one

### Requirement: A middle container switches between its siblings

An Entity page for a container that names an ancestor SHALL list the ancestor's related Entities
of its own kind, in the server's order, as a row of links under its title, marking the page's own
Entity as current. The row SHALL be hidden when the ancestor holds no other Entity of that kind.
Each entry SHALL be a link, so keyboard and remote focus reach it in order.

#### Scenario: A sibling container opens from the switcher

- **WHEN** a container's ancestor holds three containers of its kind
- **THEN** the page lists all three under its title, its own marked current, and the others link to their pages

### Requirement: A long text field reads as a clamped overview

When an Entity has no builtin summary, its first viewer-facing field of type `text` that runs to
at least 120 characters SHALL render as the page's overview, clamped to three lines with a
More/Less control once it is long, and SHALL NOT also be listed among the page's fields. A shorter
text field stays among the fields.

#### Scenario: A container's long overview field is clamped

- **WHEN** an Entity without a summary carries a 120-word text field
- **THEN** the text shows above the fields clamped to three lines with a More control, and the fields list omits it
