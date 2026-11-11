## ADDED Requirements

### Requirement: Setup throttle is per source

The server SHALL throttle failed first-run setup attempts per request source only, with no lane
shared by all sources. Requests that carry no source address SHALL share one lane of their own.

#### Scenario: Wrong tokens from one source

- **WHEN** one source has presented enough wrong setup tokens to be throttled
- **THEN** a correct setup token presented from a different source completes setup without being
  throttled
