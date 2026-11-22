# web-container-entity Specification

## Purpose
Defines the web Viewer page for a container Entity: what it leads with, how it lists its children and how it hands them to the player, using only the labels the owning plugin declares.

## Requirements

### Requirement: Container Entity page leads with the next-up child

The web Entity page SHALL show a lockup of the title, a chip with the plugin's display name for the Entity's kind, and only viewer-facing fields under their declared labels. When the Entity has a playable child, it SHALL offer one accent-filled action that plays the first unlocked child in progress, else the first unlocked unfinished child, else the first unlocked child. The player SHALL receive the container's order as its queue. The page SHALL NOT name any plugin kind to decide this.

#### Scenario: A child is in progress
- **GIVEN** a container whose second child has a saved position
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control is Resume for the second child
- **AND** choosing it opens the player with all the container's children as the queue

#### Scenario: Nothing is in progress
- **GIVEN** a container whose first child is finished and second is not started
- **THEN** the action is Play for the second child

### Requirement: A container of containers plays through its next-up child container

The page SHALL read the Entity's ordered Media for its primary action even when no detail Layout renders them. With no playable Media of its own, the primary action SHALL play the next-up child of its next-up child container: the first unlocked item with children left to finish in the first related child group whose archetype is a container, else that group's first unlocked item, never a parent group's. The player SHALL queue that container's order. The page SHALL NOT name a plugin kind.

#### Scenario: A container of containers
- **GIVEN** a container with a detail Layout, no Media of its own, and two related child containers, the first finished and the second with children left
- **WHEN** its Entity page loads
- **THEN** the only accent-filled control plays the second child container's next-up child
- **AND** the player receives the second child container's order as its queue

#### Scenario: A middle container with a parent container sorting first
- **GIVEN** a container with no Media of its own, a related parent container group sorting first and a related child container group
- **WHEN** its Entity page loads
- **THEN** its primary action plays through the child container, never the parent

### Requirement: Container children are grouped, ordered links

The Entity page SHALL render its Media children as ordered links grouped by item kind in first-seen order, each showing a thumbnail, its number, title, runtime and progress or watched state. Document order SHALL match visual order, so keyboard focus follows it. On a phone width the children SHALL render as list rows.

#### Scenario: Keyboard order
- **WHEN** a container lists three children
- **THEN** their links appear in the server's order with no positive tabindex

#### Scenario: Two kinds of child
- **WHEN** the children carry two kinds
- **THEN** each kind is a headed group in the order the kinds first appear

### Requirement: An empty detail Layout renders nothing inside an Entity

When every Section of an Entity's detail Layout is empty, or the Layout has no Sections, the web Entity page SHALL render nothing for the Layout. It SHALL NOT show the library empty or onboarding state (first scan, migrate) inside an Entity page.

#### Scenario: Owner opens a container whose detail Layout is empty
- **GIVEN** a manager and an Entity whose detail Layout's only Section returns no items
- **WHEN** the Entity page loads
- **THEN** no "Start first scan" or "Migrate" action appears on it

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

#### Scenario: Newer unstarted containers appear first

- **GIVEN** two unstarted containers whose earliest visible linked Media years differ
- **WHEN** a catalog shelf receives the older container first in the Source response
- **THEN** the newer container renders first

### Requirement: Section Source Entity cards carry their earliest linked Media year

For an Entity card returned by a Section Source, the server SHALL populate the existing year
field with the earliest positive year among directly linked, untrashed Media allowed by the
request's Library and certification policy. The field SHALL be absent when no such year exists.

#### Scenario: A container without a dated visible Media has no year

- **GIVEN** a container whose directly linked Media are all undated, trashed or outside the request's Library and certification policy
- **WHEN** a Section Source returns its Entity card
- **THEN** the card's year field is absent

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

### Requirement: The primary action names the child it plays

When the next-up child has a plugin subtitle, the container Entity page's accent button SHALL read
Play or Resume followed by the subtitle's parts other than the child's title, joined by spaces, and
its accessible name SHALL also carry the child's title. An audio listing, or a child without a
subtitle, SHALL keep the plain Play or Resume label. The page SHALL NOT name a plugin kind.

#### Scenario: Next-up child with a subtitle

- **WHEN** the next-up child's subtitle is "S1 · E3" and it has no saved position
- **THEN** the button reads "Play S1 E3"

#### Scenario: Child in progress

- **WHEN** that child has a saved position
- **THEN** the button reads "Resume S1 E3"

#### Scenario: No subtitle

- **WHEN** the next-up child has no subtitle
- **THEN** the button reads "Play"
