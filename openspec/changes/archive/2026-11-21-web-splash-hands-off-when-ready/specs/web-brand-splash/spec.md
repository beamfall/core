## MODIFIED Requirements

### Requirement: Cold-load logo intro

The web app SHALL play the design system's Cut Beam logo intro as a splash on every document load
while the first screen loads, and SHALL hand off to the first screen as soon as it has rendered,
cutting the intro short if it is still playing. In-app navigation SHALL NOT replay it.

#### Scenario: Cold load

- **WHEN** the web app loads in a new document
- **THEN** the logo intro plays over the brand background and, once the first screen has
  rendered, the splash fades out and is removed

#### Scenario: App ready before the intro ends

- **WHEN** the first screen renders while the intro is still playing
- **THEN** the splash fades out at once, without waiting for the intro to end
