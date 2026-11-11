# Retire an Integration Host generation only after its guarded calls drain

Ticket: CORE-0746 (`queue:core:main`).

## Why

A worker generation lease closes its drain signal whenever its count of in-flight guarded Host API
calls reaches zero, even while the lease is still current. After one guarded call completes, the
signal is already closed, so a later guarded call that is still running cannot make `Retire` wait:
the lifecycle believes the generation drained and may start a replacement worker while the old
generation is still inside a Host API call.

## What Changes

- The drain signal closes only once the lease is retired and no guarded call is in flight.
- `Retire` keeps its caller-supplied bound: when the bound expires first it returns the bound's
  cause, which the worker lifecycle already reports as an incomplete stop.
- Guarded calls started after retirement are still rejected with `ErrWorkerGenerationRetired`.

## Non-goals

- Fence ownership, generation numbering and the worker restart policy are unchanged.
- The stop bound value is unchanged.

## Failure modes

- A guarded call wedged past the stop bound: `Retire` returns the bound's cause and the lifecycle
  reports `errIntegrationWorkerStop`, as before.

## Acceptance evidence

- `internal/integrationhost/generation_test.go` completes one guarded call, holds a second one
  blocked, and shows `Retire` waits, reports the bound on timeout, succeeds once the call is
  released, and rejects new calls.

## Rollback

Revert the change. No stored state or wire contract depends on it.

## Capabilities

### New Capabilities

- `integration-host-generation`: how a worker generation lease fences and drains Host API calls.

### Modified Capabilities

None.
