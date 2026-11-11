# integration-host-generation Specification

## Purpose
When the Integration Host may retire a plugin generation.

## Requirements

### Requirement: Retiring a generation waits for every guarded call

Retiring an Integration Host worker generation lease SHALL wait until every guarded Host API call
admitted under that lease has returned, regardless of how many earlier guarded calls already
completed. The wait SHALL be bounded by the caller's context, and a guarded call started after
retirement SHALL be rejected with `ErrWorkerGenerationRetired`.

#### Scenario: A later guarded call is still running

- **WHEN** one guarded call has completed and a second guarded call is still running when the
  lease is retired
- **THEN** retirement does not report success until the second call returns

#### Scenario: Retirement bound expires

- **WHEN** a guarded call is still running when the caller's retirement bound expires
- **THEN** retirement returns the bound's cause instead of reporting a drained generation

#### Scenario: Calls after retirement

- **WHEN** a guarded call is attempted on a retired lease
- **THEN** it is rejected with `ErrWorkerGenerationRetired` and its work does not run
