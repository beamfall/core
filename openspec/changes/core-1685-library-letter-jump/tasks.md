## 1. Server

- [x] 1.1 Store: letter offsets and counts on `LibraryBrowseResult` for the title sort
- [x] 1.2 Graph: `letters` on `libraryItems`
- [x] 1.3 Publish wire contract 2.5.0 and regenerate wire goldens

## 2. Web

- [x] 2.1 `LibraryLetterRail` after board C: `#` and A-Z, disabled letters with counts, roving focus,
  typed letters, drag bubble, current letter, cramped "Jump to" sheet
- [x] 2.2 Browse page: shown on a title sort past 60 items; a jump scrolls the virtual grid, loads
  the pages in view and focuses the item when asked

## 3. Native

- [x] 3.1 Child tickets CORE-1767 (Apple: iPhone, tvOS focusable) and CORE-1769 (Android: phone, TV
  focusable), after CORE-1220
- [x] 3.2 Article-insensitive sort title follow-up: CORE-1768

## 4. Verification

- [x] 4.1 Focused Go and web tests, lint
- [x] 4.2 Web rail in a browser: jump, keyboard, drag, narrow viewport
