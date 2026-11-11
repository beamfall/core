# base-url-response-writer Specification

## Purpose
TBD - created by archiving change base-url-flush-error. Update Purpose after archive.

## Requirements

### Requirement: Flush results pass through the base-URL writer

The base-URL response writer SHALL forward a flush to the underlying writer and SHALL report the
underlying result to a caller flushing through `http.ResponseController`. It SHALL keep
implementing `http.Flusher`.

#### Scenario: Underlying writer flushes

- **WHEN** a handler flushes through `http.ResponseController` or `http.Flusher` and the underlying
  writer supports flushing
- **THEN** the underlying writer is flushed and the controller reports no error

#### Scenario: Underlying writer cannot flush

- **WHEN** a handler flushes through `http.ResponseController` and the underlying writer has no
  flush support
- **THEN** the controller reports `http.ErrNotSupported`

#### Scenario: Underlying flush fails

- **WHEN** a handler flushes through `http.ResponseController` and the underlying writer's flush
  returns an error
- **THEN** the controller reports that error
