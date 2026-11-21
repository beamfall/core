## MODIFIED Requirements

### Requirement: The desktop sidebar is resizable

The expanded desktop Viewer sidebar SHALL default to 200 px and SHALL offer a focusable separator on
its inline-end edge that resizes it between 168 px and 360 px by pointer drag and by keyboard,
exposes its width through `aria-valuenow`, `aria-valuemin` and `aria-valuemax`, and resets to the
default on double-click. A single click or tap on the separator without dragging SHALL open a
width control that narrows, widens and resets the sidebar with single presses, so no width needs a
dragging movement. The collapsed icon rail and the phone drawer SHALL NOT offer it.

#### Scenario: Keyboard resize

- **WHEN** a person focuses the separator and presses ArrowRight twice
- **THEN** the sidebar is 232 px wide and the separator reports `aria-valuenow` 232

#### Scenario: Resize without dragging

- **WHEN** a person clicks the separator without moving it and presses Wider twice
- **THEN** the sidebar is 232 px wide and the width persists like a dragged width

#### Scenario: A drag does not open the width control

- **WHEN** a person drags the separator to a new width and releases it
- **THEN** the sidebar takes the dragged width and no width control opens

#### Scenario: Phone layout

- **WHEN** the viewport is phone width
- **THEN** the sidebar opens as a drawer with no resize separator
