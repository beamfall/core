## 1. Server

- [x] 1.1 `graphError` gains `extensions.code`; one mapping function and table in `internal/graph`
- [x] 1.2 Every error site (HTTP gate, role gate, audit failures, field failures) sets a code
- [x] 1.3 Tests: error JSON carries the code; denial-of-existence answers `NOT_FOUND`

## 2. Contract

- [x] 2.1 Conformance test enumerates error sites and fails on an unmapped one
- [x] 2.2 Wire contract gains the error shape and vocabulary; publish 2.1.0 and repoint the registry
- [x] 2.3 ARCHITECTURE §7 states the shipped shape
