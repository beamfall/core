# GraphQL errors: a machine-readable reason for reveal PIN refusals

Tickets: CORE-1551 (`queue:core:main`)

## Why

The My Data export (`myEngagement`) and delete (`deleteMyData`) refusals all share the FORBIDDEN
code, and only the English message said whether the profile has no reveal PIN (send the user to
set one) or the PIN was missing or wrong (show the keypad or the incorrect-PIN copy). tvOS matched
the message text, so rewording it would silently break the client.

## What changes

- GraphQL error extensions gain an optional `reason` from a closed vocabulary that refines `code`:
  `REVEAL_PIN_NOT_SET`, `REVEAL_PIN_REQUIRED` (no PIN sent) and `REVEAL_PIN_INCORRECT` (a PIN was
  sent and did not match). All three go with FORBIDDEN.
- The three refusals in `internal/graph/mydata.go` set it. Their messages are unchanged.
- The wire contract's GraphQL errors section lists the vocabulary under `reasons`, with a note that
  clients treat an unknown reason as absent. This ships in the client-core-wire 2.10.0 lockstep.
- Clients (BeamfallKit, the Apple and Android My Data screens, web) move from message matching to
  `reason` in their own follow-ups.

## Non-goals

- No new error codes, and no reasons for other refusals yet.
