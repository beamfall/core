## ADDED Requirements

### Requirement: The web poster size roams with the Profile

The web viewer SHALL save a poster size pick to the signed-in Profile as the per-Profile client
preference `web.poster-size`, and SHALL draw a Profile's saved size in any browser once that
Profile's preferences load. A Profile with no saved size SHALL keep the browser's own value, and
the web viewer SHALL NOT write a size to a Profile the viewer did not pick it under.

#### Scenario: A pick follows the Profile to another browser

- **WHEN** a viewer picks Small in one browser and signs in to the same Profile in another
- **THEN** the other browser draws Small once the Profile's preferences load

#### Scenario: No saved size keeps the browser's value

- **WHEN** a Profile with no saved size opens the viewer in a browser whose value is Medium
- **THEN** the viewer draws Medium and saves nothing to the Profile

#### Scenario: A Profile switch drops a queued save

- **WHEN** a save is still queued as the identity changes (Profile switch, reveal or relock)
- **THEN** the save is not sent under the new session
