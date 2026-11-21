# Render styled subtitles in the served web player

CORE-1719

## Why

Converting ASS and SSA to WebVTT discards authored placement and styling, including the fonts supplied by the source container.

## What changes

- Load the styled subtitle renderer on selection and retain the existing WebVTT presentation as a fallback.
- Serve styled muxed subtitles and their embedded fonts using the existing file- and track-bound subtitle grant.
