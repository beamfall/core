# Derive Entity years for Section Sources

## Why

CORE-2532. Browse catalog shelves already rank by year within an engagement tier, but Entity cards arrive without a year and retain title order within the tier.

## What Changes

- Populate the existing `SourceItem.year` field on Entity cards from their earliest directly linked Media year visible to the Source request.
- Look up the years in batches across readable catalog planes. Keep the existing client ranking and wire shape.

## Impact

- Store evaluation of Section Sources and web Browse shelf ordering.
