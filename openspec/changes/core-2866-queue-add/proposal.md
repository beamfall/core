# Guard Play Queue adds at commit

Ticket CORE-2866.

## Why

A client's eligibility check can become stale after Reveal or certification changes. A
response read can also fail after the add has committed, leaving the client unsure
whether the queue changed.

## What Changes

- Check the target's current eligibility inside the queue write transaction.
- Reuse the existing GraphQL error vocabulary for definitive refusals.
- Return the committed queue order even when optional card details cannot be read.
