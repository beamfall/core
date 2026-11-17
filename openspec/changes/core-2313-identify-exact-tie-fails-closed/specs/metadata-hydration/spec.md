## ADDED Requirements

### Requirement: An exact top-score identification tie is queued for review

When automatic identification scores two or more candidates with different provider IDs at the
same highest score, the server SHALL NOT apply any of them, whatever their popularity, and SHALL
queue the item for review with the first candidate at that score, as it queues a result below the
plugin's threshold. Candidates that share one provider ID SHALL count as one item.

#### Scenario: Two different items tie at the top

- **WHEN** a file titled `The Office` is identified and two different series named `The Office`
  score the same and above the threshold
- **THEN** neither is applied and the item is queued for review with its top candidate

#### Scenario: Duplicates of one item

- **WHEN** the search returns the same provider ID twice at the highest score
- **THEN** that item is applied

#### Scenario: Distinct scores

- **WHEN** the highest-scoring candidate scores above every other and clears the threshold
- **THEN** it is applied, even when a lower-scoring candidate is more popular
