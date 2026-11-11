# graphql-document-budgets Specification

## Purpose
Bounds the server applies to a GraphQL document before parsing it, so a hostile document cannot exhaust the parser.

## Requirements

### Requirement: Nesting is bounded before parsing

The server SHALL scan a GraphQL document lexically before parsing it and SHALL reject, without
invoking the parser, a document whose selection-set nesting exceeds 64 levels or whose value
nesting (lists, input objects and list types inside arguments and variable definitions) exceeds 64
levels. The scan SHALL ignore braces and brackets inside strings, block strings and comments, and
SHALL run in time linear in the document size without allocating per byte.

#### Scenario: Selection nesting over the limit

- **WHEN** a document nests selection sets 65 levels deep
- **THEN** the request fails with `GraphQL selection depth exceeds 64` before the parser runs

#### Scenario: Selection nesting at the limit

- **WHEN** a document nests selection sets exactly 64 levels deep
- **THEN** the scan accepts it and later validation proceeds as before

#### Scenario: Value nesting over the limit

- **WHEN** an argument value or variable type nests lists or input objects 65 levels deep
- **THEN** the request fails with `invalid GraphQL document` before the parser runs

#### Scenario: Braces inside strings and comments

- **WHEN** a string, block string or comment contains more than 64 opening braces
- **THEN** those braces do not count towards either nesting limit

#### Scenario: Pathological maximal-size document

- **WHEN** a 256 KiB document consists of nested selection sets, lists or input objects
- **THEN** preparing it allocates less than 4 MiB of heap and stack combined
