## ADDED Requirements

### Requirement: Web token CSS is generated from the design-system release

The web app's token CSS and its shipped preset role values SHALL be generated from the latest
published beamfall-design-system token release and preset catalog, and SHALL NOT be edited by
hand. A check SHALL fail when either generated file differs from a fresh generation. When no
current design-system checkout is available the check SHALL report that it was skipped and why,
and SHALL NOT report a pass.

#### Scenario: Release moves ahead of the committed tokens

- **WHEN** the design system publishes a token release the committed files were not generated from
- **THEN** the drift check names the stale files and fails

### Requirement: App styles colour only through theme roles

Web app CSS and TSX SHALL NOT contain a raw colour (hex, colour function, CSS named colour or
Tailwind palette class) outside the generated token file. A lint SHALL fail on any such literal.

#### Scenario: A component adds a fixed colour

- **WHEN** a TSX file adds the class `text-white`
- **THEN** the colour lint reports the file and line and fails

### Requirement: Themes apply at run time without a reload

The web app SHALL apply a shipped preset or a theme document by resolving it with its resolver
and setting the resolved roles as CSS custom properties on the document root, so every surface
re-skins without a reload. A theme document the resolver rejects SHALL leave the current theme on
screen. The app's resolver SHALL produce the design system's golden vector results.

#### Scenario: Viewer picks a preset

- **WHEN** a viewer chooses the Moss preset in Profile Settings
- **THEN** the app re-skins in place and `theme.preset` is saved as `moss` for the Profile

#### Scenario: Rejected theme document

- **WHEN** a theme document names an unknown role
- **THEN** the resolver rejects it and the previous theme stays applied

### Requirement: The colour scheme is chosen per device

The web app SHALL offer a dark, light or system colour scheme, stored only on the device and
applied in every register, with system following the device's colour-scheme preference as it
changes. The theme preset SHALL roam with the Profile through `profile_client_prefs`; the scheme
SHALL NOT be written there. Video and artwork stages SHALL stay on the dark scheme.

#### Scenario: Light on one device only

- **WHEN** a viewer sets the scheme to Light on a laptop
- **THEN** the laptop shows the light scheme and no client preference is written
