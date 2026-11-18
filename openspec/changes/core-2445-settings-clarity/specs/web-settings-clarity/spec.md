## ADDED Requirements

### Requirement: Settings explain themselves with a help button

A Console or Manage setting whose name alone does not tell a home user what it does or when to change it SHALL show a (?) help button beside its label. Activating the button SHALL open a popover that names the setting and explains, in plain words, what it does, its default and when to change it. The button SHALL be keyboard operable, SHALL have an accessible name that includes the setting's name, and SHALL close on Escape or an outside click. Help text SHALL be localized in every shipped locale.

#### Scenario: Opening help

- **WHEN** an operator activates the (?) beside "Low-power mode" on the Operations page
- **THEN** a popover titled "Low-power mode" explains that Beamfall then runs one background task at a time, that this is the default, and when to turn it off

#### Scenario: Keyboard and screen reader

- **WHEN** a keyboard user tabs to the (?) beside a setting
- **THEN** its accessible name is "Help: " followed by the setting's name, Enter opens the popover and Escape closes it

### Requirement: Settings with a fixed set of sensible values use a dropdown

A setting whose useful values form a small known set SHALL be chosen from a dropdown that names each choice in plain words, rather than typed as a free number. The current value SHALL always be selectable, including a saved value that is not one of the presets. The dropdown SHALL show the default as preselected for a new record.

#### Scenario: Background workers

- **WHEN** the Operations page shows the background workers setting
- **THEN** it is a dropdown offering 1, 2, 4 and 8 workers with a plain-words hint for each, and a saved value of 3 is shown as "3"

#### Scenario: Remote quality limit

- **WHEN** the Remote access page shows the server-wide remote limit
- **THEN** it is a dropdown of "No limit" and quality presets labelled with their bitrate, and choosing one saves that bitrate

#### Scenario: Access window times

- **WHEN** an operator adds an access window for a managed profile
- **THEN** its start and end are chosen as times of day (00:00 to 24:00 in 30-minute steps), not entered as minutes since midnight

### Requirement: Console and Manage state distinct purposes

The Console menu SHALL open with a line describing it as the place to run the server, and the Manage menu with a line describing it as the place to curate content. Each line SHALL link to the other area.

#### Scenario: Purpose lines

- **WHEN** the Console rail is shown
- **THEN** its top line says it runs the server and links to Manage, and the Manage menu's top line says it curates content and links to the Console

### Requirement: No destination is duplicated or ambiguously named across areas

A destination SHALL NOT appear in both the Console and Manage menus, and two destinations in either menu SHALL NOT share a name for different things. Server backups SHALL live only in the Console, and the old Manage address SHALL redirect there.

#### Scenario: Backups

- **WHEN** a person opens `/manage/backups`
- **THEN** they are taken to `/console/backups`, and the Manage menu has no Backups item

#### Scenario: Parental controls name

- **WHEN** the Console rail is shown
- **THEN** the destination for managed-profile limits is named "Parental controls", not "Schedules", and Manage's session profile page is named "Switch profile"

### Requirement: Settings search

The Console rail and the Manage menu SHALL each offer a settings search that lists matching settings and destinations from both areas, matched on their names and help text, each result labelled with its area and page. The search SHALL be operable by keyboard alone and SHALL announce how many results there are.

#### Scenario: Finding a setting by what it does

- **WHEN** an operator types "upload" into the Console settings search
- **THEN** "Remote quality limit" is listed under Console › Remote access

#### Scenario: Searching across areas

- **WHEN** a person types "trash" into the Manage settings search
- **THEN** results from Manage › Trash and from the Console are both listed, each labelled with its area

### Requirement: Choosing a search result reveals the setting

Choosing a settings search result SHALL open its page, reveal the setting (turning on the Advanced view when the setting is classified Advanced, and opening a collapsed section that holds it), scroll it into view and briefly highlight it.

#### Scenario: Revealing a setting

- **WHEN** an operator chooses "Remote quality limit" from the search
- **THEN** `/console/remote` opens with that setting scrolled into view and highlighted

#### Scenario: Advanced setting

- **WHEN** the Normal view is on and an operator chooses the "Events retention" result
- **THEN** the Advanced view turns on and the Operations page shows the field highlighted
