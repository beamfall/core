## ADDED Requirements

### Requirement: Healthy subscription polls adapt to feed cadence and dormancy

Healthy Subscription polls SHALL schedule the next poll within 15 minutes to 24 hours using explicit feed update hints, HTTP cache lifetimes, observed publication cadence, or dormancy, and SHALL retain poll state across `304 Not Modified` and rolling feeds.

#### Scenario: Feed hints, HTTP cache-control, and dormancy set interval

- **WHEN** a healthy feed declares `updateFrequency`, `<ttl>`, `sy:updatePeriod`, or HTTP `Cache-Control: max-age`
- **THEN** dormancy (`complete="true"` or 180 days without entries) schedules 24 hours, explicit feed hints take precedence over `max-age`, `max-age` bounds observed cadence, and `304 Not Modified` preserves the same schedule

#### Scenario: Rolling feed preserves observed cadence

- **WHEN** a rolling feed retains only its latest item or omits explicit GUIDs
- **THEN** healthy `200 OK` and `304 Not Modified` polls retain prior publication timestamps and converge on the feed's release cadence
