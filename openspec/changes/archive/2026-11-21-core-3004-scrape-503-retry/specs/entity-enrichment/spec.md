## MODIFIED Requirements

### Requirement: Entity enrichment pacing

The job SHALL record each entity check, skip matched entities thereafter and retry unmatched ones
after 30 days. A request answered with HTTP 429, or with HTTP 503 carrying `Retry-After`, SHALL be
retried in place up to three times when the requested wait is at most 10 seconds. When the provider
still reports rate limiting or a server error, asks for a longer wait, or cannot be reached, the job
SHALL end the pass and requeue it later.

#### Scenario: Provider rate-limits the pass

- **WHEN** the scraper returns HTTP 429 during a pass
- **THEN** the job records nothing for the current entity, keeps its checkpoint and requeues itself with a delay

#### Scenario: Provider is briefly busy

- **WHEN** the provider answers HTTP 503 with `Retry-After: 1` and the retry succeeds
- **THEN** the request is retried in place after one second and the pass continues
