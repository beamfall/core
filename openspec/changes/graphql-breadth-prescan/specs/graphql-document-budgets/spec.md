## ADDED Requirements

### Requirement: Breadth is bounded before parsing

The server SHALL count the lexical tokens of a GraphQL document (names, numbers, strings, block
strings, punctuators and comments) in its pre-parse scan and SHALL reject, without invoking the
parser, a document of more than 8192 tokens with the existing `invalid GraphQL document` error. The
count SHALL be at least the number of tokens the parser reads, and SHALL be computed in time linear
in the document size without allocating.

#### Scenario: Token count over the limit

- **WHEN** a document contains 8193 tokens
- **THEN** the request fails with `invalid GraphQL document` before the parser runs

#### Scenario: Token count at the limit

- **WHEN** a document contains exactly 8192 tokens
- **THEN** the scan accepts it and later validation proceeds as before

#### Scenario: Pathological maximal-size shallow document

- **WHEN** a 256 KiB document of depth one repeats fields, aliases, arguments, list values,
  input-object fields, directives, variables, fragment spreads, comments or operations
- **THEN** it is refused before the parser runs and preparing it allocates less than 4 MiB of heap
  and stack combined

#### Scenario: Largest accepted shallow document

- **WHEN** a document of any of those shapes has as many tokens as the limit allows
- **THEN** preparing it allocates less than 4 MiB of heap and stack combined
