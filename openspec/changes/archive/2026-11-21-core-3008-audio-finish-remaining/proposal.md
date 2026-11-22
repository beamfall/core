# Resume-eligible audio finishes on remaining time

Ticket: CORE-3008 (`queue:core:main`). Design: `core-private/docs/design/audiobooks.md` §5.1;
ADR-0055 amendment A.

## Why

Core marks audio played once 80% of its duration has played, and a played item stores a resume
position of 0. For resume-eligible audio (audiobooks, podcasts) that fires with hours left in a
long book: it drops out of Continue Watching and reopens at the start.

## What Changes

- Resume-eligible audio is marked played only when it has crossed the audio threshold **and**
  30 s or less remain, or when the client reports it finished. Until then the position is kept.
- Audio that is not resume-eligible (music) keeps the 80% rule; video keeps 95%.
- No wire, schema or migration change.
