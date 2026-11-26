# Skip artwork derivatives at or above the source width

Ticket: CORE-3528 (`queue:core:main`).

## Why

Posters are stored at 1280px, yet the w1280 and w1920 buckets still wrote a re-encoded file per item,
because the scaler never upscales. On a production data directory that was 30,176 same-size copies
and 1.74 GB of 16.7 GB.

## What changes

A bucket whose width is not narrower than the source image serves the original and writes no file.
Copies written by earlier releases are removed when the bucket is next requested, which the artwork
sweep does for every item.
