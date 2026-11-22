## MODIFIED Requirements

### Requirement: Home leads with resumable items

When the Home layout includes a Continue Watching shelf, the Viewer SHALL show
the Recently Added hero only after that shelf settles empty or failed. While
the shelf loads, the Viewer SHALL show neither the hero nor its placeholder.
The Viewer SHALL render Home sections in the manifest's order, which leads with
Continue Watching unless the Profile saved a Home layout placing it elsewhere.

#### Scenario: Returning Profile opens Home

- **WHEN** the Continue Watching shelf loads with items and the Profile saved no layout
- **THEN** Home shows that shelf first and no Recently Added hero

#### Scenario: New Profile opens Home

- **WHEN** the Continue Watching shelf loads empty
- **THEN** Home shows the Recently Added hero above its shelves

#### Scenario: Continue Watching is still loading

- **WHEN** Recently Added artwork is ready before the Continue Watching shelf settles
- **THEN** Home shows no hero and no hero placeholder until the shelf settles

## ADDED Requirements

### Requirement: Edit home mode

The Viewer Home SHALL offer an "Edit home" mode listing every Home section the
Profile receives, including hidden ones. Each section SHALL be movable up and
down with buttons, reorderable by dragging, and shown or hidden with a toggle;
every control SHALL be operable from the keyboard. Saving SHALL store the
layout on the server and refresh Home; "Reset to default" SHALL clear it.

#### Scenario: Keyboard reorder

- **WHEN** a keyboard user focuses a section's "Move up" button and activates it, then saves
- **THEN** the server stores the new order and Home renders it

#### Scenario: Reset

- **WHEN** the user chooses "Reset to default"
- **THEN** the saved layout is cleared and Home shows the default order
