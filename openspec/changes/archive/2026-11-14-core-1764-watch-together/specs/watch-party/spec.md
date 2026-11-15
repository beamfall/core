## ADDED Requirements

### Requirement: Web viewer starts a Watch Party from the media detail page

The web media detail page SHALL offer a "Watch together" secondary action only when the server has accepted the tab's Watch Party clock calibration (the home network) and no party is running. Choosing it SHALL start the party through the same create path as the player, held at the viewer's start position, and SHALL open the player with the Watch Party invite panel open. A failed start SHALL leave the viewer on the page with a notice naming the cause.

#### Scenario: Off the home network
- **WHEN** the server refuses the clock calibration
- **THEN** the detail page does not show Watch together

#### Scenario: Starting from the detail page
- **GIVEN** the viewer is on the home network with a saved position of 42.5 seconds
- **WHEN** the viewer chooses Watch together
- **THEN** a party starts for the item at 42500 ms
- **AND** the player opens past pre-play with the Watch Party invite panel open

#### Scenario: The start fails
- **WHEN** the server refuses to create the party
- **THEN** the viewer stays on the detail page and sees a notice saying why
