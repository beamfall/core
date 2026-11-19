# Durable observed source ends

## Why
CORE-2380 extends CORE-1343 EOF refusal to resume and engagement projections. Process-local observations can advertise known unplayable resumes after eviction or restart, and exclude encrypted sources.

## What Changes
Persist observed premature source ends by source generation in the owning catalog plane. Refuse playback/resume and omit affected items from Continue Watching and the in-progress projection until the source generation changes.

## Impact
Append-only catalog and encrypted-library migrations; playback, engagement and encrypted HTTP source wiring. No wire contract changes.
