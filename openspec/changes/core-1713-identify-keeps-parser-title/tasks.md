## 1. Server

- [x] 1.1 Add the optional `MediaTitleSource` to `identify.Service`; `Apply` writes a non-blank parser title instead of the scraper's
- [x] 1.2 Keep the scraper's title for a blank title, a parser with no `MediaTitler`, or a failed read
- [x] 1.3 Wire the compiled filename parsers through an adapter over the Media's primary file

## 2. Tests

- [x] 2.1 Identify test: parser title wins, blank and failed reads keep the scraper's, a title-less lookup never asks
- [x] 2.2 App test: a series-shaped lookup through the real scrape engine and store keeps the parser's per-file title, and without the source the container name replaces it
