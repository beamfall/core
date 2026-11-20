## ADDED Requirements

### Requirement: The desktop sidebar is resizable

The expanded desktop Viewer sidebar SHALL default to 200 px and SHALL offer a focusable separator on
its inline-end edge that resizes it between 168 px and 360 px by pointer drag and by keyboard,
exposes its width through `aria-valuenow`, `aria-valuemin` and `aria-valuemax`, and resets to the
default on double-click. The collapsed icon rail and the phone drawer SHALL NOT offer it.

#### Scenario: Keyboard resize

- **WHEN** a person focuses the separator and presses ArrowRight twice
- **THEN** the sidebar is 232 px wide and the separator reports `aria-valuenow` 232

#### Scenario: Phone layout

- **WHEN** the viewport is phone width
- **THEN** the sidebar opens as a drawer with no resize separator

### Requirement: The sidebar width persists per browser

The chosen sidebar width SHALL persist per browser and be restored on load, clamped to the limits.
When storage is unavailable or holds an invalid value the sidebar SHALL use the default width.

#### Scenario: Width survives a reload

- **WHEN** a person resizes the sidebar to 280 px and reloads the page
- **THEN** the sidebar opens at 280 px

#### Scenario: Out-of-range stored width

- **WHEN** the browser's stored width is 900 px
- **THEN** the sidebar opens at the 360 px maximum
