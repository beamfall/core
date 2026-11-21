# Persist the runtime found during playback

Ticket CORE-2336.

## Why

Playback can discover the duration of a file with a missing runtime. Catalog
reads continue to show a zero duration until the next scan.

## What Changes

Record a successful probe from the Media row's primary single-Part Version
when the stored duration is zero. Subsequent catalog reads use the saved
runtime. Playback can use the probe result even if the write fails.
