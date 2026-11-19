# web-brand-splash Specification

## Purpose
Open the web app with the design system's Cut Beam logo intro on each document load, fitted to the viewport's orientation and replaced by the static lockup under reduced motion, then hand off to the first screen.

## Requirements

### Requirement: Cold-load logo intro

The web app SHALL play the design system's Cut Beam logo intro as a splash on every document load,
ending on the static lockup and then handing off to the first screen. In-app navigation SHALL NOT
replay it.

#### Scenario: Cold load

- **WHEN** the web app loads in a new document
- **THEN** the logo intro plays over the brand background and, once it has ended and the first
  screen has rendered, the splash fades out and is removed

### Requirement: Lockup follows the viewport

The splash SHALL show the horizontal lockup intro when the viewport is at least as wide as it is
tall and the stacked lockup intro otherwise. A rotation or resize during the intro SHALL switch
layout at the same point in the intro.

#### Scenario: Portrait

- **WHEN** the viewport is taller than it is wide
- **THEN** the stacked lockup intro shows

#### Scenario: Rotation mid-intro

- **WHEN** a landscape viewport turns portrait while the intro plays
- **THEN** the stacked intro shows at the same point in the intro, without restarting

### Requirement: Reduced motion shows the static lockup

Under `prefers-reduced-motion: reduce` the splash SHALL show the static lockup for the viewport's
layout, with no animation, until the first screen is ready.

#### Scenario: Reduced motion

- **WHEN** the user prefers reduced motion
- **THEN** the static lockup shows and then hands off to the first screen
