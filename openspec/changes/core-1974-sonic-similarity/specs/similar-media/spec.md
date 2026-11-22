## ADDED Requirements

### Requirement: Similar-Media read

The store SHALL answer `SimilarMedia(filter, kind, seeds, limit)` with Media whose embeddings of
`kind` are nearest by cosine similarity to the sum of the seeds' embeddings of that kind, each
scaled to unit length, best first, ties in the scan order. A seed SHALL NOT be listed. Scores SHALL
be cosine similarities in [-1, 1], and a candidate scoring exactly -1 SHALL be eligible.

#### Scenario: Nearest first, seed excluded

- **WHEN** one seed is given and two other Media have vectors at decreasing similarity to it
- **THEN** both are listed, nearest first, and the seed is not listed

#### Scenario: Several seeds

- **WHEN** two seeds are given
- **THEN** candidates are ranked against the centroid of the two unit vectors

#### Scenario: Exact opposite kept

- **WHEN** a candidate's vector is the negation of the only seed's vector
- **THEN** it is listed last with a score of -1

#### Scenario: Nothing usable

- **WHEN** no seed has an embedding of `kind`, or the seeds' unit vectors cancel out
- **THEN** the result is empty and no error is returned

### Requirement: Similar-Media seeds and limits

Seeds SHALL be used in the order given. Seeds without an embedding of `kind`, seeds whose
dimensionality differs from the first usable seed, and zero or non-finite vectors SHALL be ignored.
`kind` SHALL be required, at most 50 seeds SHALL be accepted, and the limit SHALL default to 50 and
be capped at 200.

#### Scenario: Mismatched seed ignored

- **WHEN** the second seed's vector is wider than the first seed's
- **THEN** the query is built from the first seed alone

#### Scenario: Too many seeds

- **WHEN** 51 seeds are given
- **THEN** the read fails with a validation error

### Requirement: Similar-Media scoping

Seeds and candidates SHALL be read through the same Reveal, Library-access and certification
scoping as `SemanticSearch`. A seed the caller cannot see SHALL contribute nothing to the query,
and a candidate the caller cannot see SHALL NOT be scored.

#### Scenario: Hidden seed

- **WHEN** the only seed is in a Library the caller cannot reveal
- **THEN** the result is empty

#### Scenario: Hidden candidate

- **WHEN** a candidate is in a Library outside the caller's scope
- **THEN** it is not listed, and it is listed once the caller's scope includes that Library

### Requirement: Similar-Media scan budget

One `SimilarMedia` read SHALL score at most 4,000,000 vector values (candidate rows times
dimensions) and never fewer than 1,000 candidate rows, taken in the same deterministic order as
`SemanticSearch`'s window, so the same inputs give the same result.

#### Scenario: Small vectors cover a large library

- **WHEN** the seeds' vectors have 32 dimensions
- **THEN** up to 125,000 candidate Media are scored
