## ADDED Requirements

### Requirement: An ended access schedule is a distinct, readable denial

When a request fails only because the caller's managed Profile is outside its access schedule (and
holds no approved extension), the server SHALL refuse it with HTTP 403 and the closed reason
`ACCESS_SCHEDULE_ENDED`, after the credential itself has verified. The session SHALL stay valid.

#### Scenario: GraphQL request outside the schedule

- **WHEN** a managed Profile's session sends a GraphQL request outside its access schedule
- **THEN** the response is HTTP 403 with one error carrying code `FORBIDDEN`, reason
  `ACCESS_SCHEDULE_ENDED` and message "access schedule ended"

#### Scenario: GraphQL mutation that closes the schedule

- **WHEN** a mutation's after-mutation scope re-resolution finds the caller outside its schedule
- **THEN** the error carries code `FORBIDDEN` and reason `ACCESS_SCHEDULE_ENDED`

#### Scenario: Playback-info or preview-info outside the schedule

- **WHEN** a managed Profile's session requests `/api/playback-info` or `/api/preview-info`
  outside its schedule
- **THEN** the response is HTTP 403 with header `X-Beamfall-Denial-Reason: ACCESS_SCHEDULE_ENDED`
  and a JSON body whose `reason` is `ACCESS_SCHEDULE_ENDED`

#### Scenario: Media transport outside the schedule

- **WHEN** a Direct, HLS, Subtitles, Sidecars or Assets request's Stream Token re-verify fails only
  on the access schedule
- **THEN** the response is HTTP 403 with header `X-Beamfall-Denial-Reason: ACCESS_SCHEDULE_ENDED`

### Requirement: Other refusals and other Profiles are unchanged

Any other credential or Stream Token failure SHALL keep its existing status, code and message, with
no denial reason. The denial SHALL carry no schedule detail and SHALL NOT be emitted on a request
that concerns another Profile's session.

#### Scenario: Wrong device secret

- **WHEN** a managed Profile's session presents a wrong device secret, inside or outside its
  schedule
- **THEN** GraphQL answers 401 `UNAUTHENTICATED` "invalid session" and REST answers 401, with no
  reason

#### Scenario: Adult Profile

- **WHEN** an adult Profile's session sends a request
- **THEN** no access schedule applies and no denial reason is emitted

#### Scenario: Watch Party member outside their schedule

- **WHEN** a Watch Party operation checks another member whose schedule has ended
- **THEN** the refusal stays generic and does not carry `ACCESS_SCHEDULE_ENDED`
