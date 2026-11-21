## MODIFIED Requirements

### Requirement: Category counts use the visible browse unit

The Categories pivot SHALL count distinct outermost container IDs when every visible member of a category belongs to the same plugin-declared browse container kind. It SHALL name that unit using the plugin's browse label, and SHALL use the plugin's singular display name for that kind when the count takes the display locale's singular form; without a singular name a singular count SHALL read as an item count. Raw Media counts SHALL remain available. A category of loose Media, mixed units, or a kind without a browse label SHALL retain its item count. Visibility filtering SHALL precede counting.

#### Scenario: Several children of two containers

- **WHEN** a category contains several visible Media from two outermost containers of one browse kind
- **THEN** its tile shows two with the plugin's label, irrespective of the number of children

#### Scenario: One container

- **WHEN** a category's visible Media all belong to one outermost container of a kind whose plugin declares a singular display name
- **THEN** its tile shows one with the singular display name, not the plural browse label

#### Scenario: Loose Media

- **WHEN** a category contains three visible Media without containers
- **THEN** its tile shows three items

## ADDED Requirements

### Requirement: Categories pivot cards have a designed fallback

Every featured Categories card SHALL keep one shape whatever its artwork. A card without a backdrop SHALL show a mosaic of up to four distinct container covers from the items it reads, and without covers SHALL show a surface tinted from the Library accent and a theme palette tone chosen deterministically from the value's name, with the value's name set large in the theme's primary text colour. The featured grid SHALL show at least two cards per row at phone widths without horizontal scrolling.

#### Scenario: A value without artwork

- **WHEN** a featured value's items carry no backdrop and no cover
- **THEN** its card shows the tinted surface with the value's name and count
- **AND** the same name gets the same tint on every visit

#### Scenario: A value with container covers only

- **WHEN** a featured value's items carry no backdrop and belong to four containers with covers
- **THEN** its card shows those four covers as a mosaic under the label
