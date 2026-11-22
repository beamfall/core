# entity-enrichment Specification

## Purpose
Let plugins classify entities (not just individual Media items) through a declared scraper, and fan each result out to the entity's member Media, so browse surfaces such as Categories reflect a fully identified library instead of sparse file tags.

## Requirements

### Requirement: Plugin entity enrichment

The server SHALL accept entity enrichments from plugin hooks. Each enrichment SHALL name an entity
kind, a relation from that kind to Media, an optional context relation, a target entity kind, a
target relation from the target kind to Media and a scraper declared by the same plugin. The server
SHALL refuse an enrichment whose kinds, relations or scraper are not declared by an active plugin,
and log the fault.

#### Scenario: Enrichment names an undeclared relation

- **WHEN** a plugin hook names a target relation its bundle does not declare
- **THEN** the server drops that enrichment with a warning and starts without it

### Requirement: Entity enrichment job

The server SHALL run a deduplicated background job that walks entities of each enrichment's kind in
the shared catalog, never in an encrypted Library. For each due entity it SHALL search the scraper,
pick a candidate, look it up and, when names result, resolve each target entity by folded title and
replace the relation rows of every member Media under the enrichment's relation only.

#### Scenario: Enrichment writes alongside tag classifications

- **WHEN** an enrichment writes target relations for a member Media that already has tag-derived classifications
- **THEN** the tag-derived relation rows are left unchanged

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
