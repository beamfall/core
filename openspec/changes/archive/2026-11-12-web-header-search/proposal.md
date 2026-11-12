# Search from the viewer header, with results as you type

Ticket: CORE-1191 (`queue:core:main`). Absorbs CORE-1168 (cancel stale search requests) and the
search-page part of CORE-0289 (query mirrored in the URL).

## Why

Search in the web viewer is a separate `/search` page behind a header link. Nothing happens until
the form is submitted, a slow earlier reply is only ignored rather than cancelled, and a result is a
bare card: no type, year or library, and activating a Media card starts playback at once. A search
that has to be navigated to and then submitted is slower than the catalogue browsing it is meant to
shortcut, and the query is lost on reload because it is not in the URL.

## What Changes

- The viewer header carries a search field on every viewer page. `/` (outside a text field) and
  Cmd/Ctrl-K focus it.
- Typing searches after a short debounce. A newer query cancels the request for the older one.
- Results open in a list under the field, grouped by type. Each row shows artwork, title, year,
  type and library. Up/Down move through rows, Enter opens the highlighted row's page, and Media
  rows carry a separate Play control. Escape closes the list.
- A polite live region announces the number of results, or that there are none, for the query.
- With no query the list is closed. With a query and no results the list names the query and offers
  the full search page. The full search page (`/search?q=…`) reads and writes the query in the URL,
  searches as the user types with the same cancellation, shows no results heading until a query
  exists, and its no-result state names the query and offers a Clear search action.
- `searchResult` rows gain optional fields the rows need: `libraryID`, `libraryTitle`, `year`,
  `mediaClass` (Media rows) and `entityKind` with `entityKindDisplayName` (Entity rows). They are
  filled only for rows in Libraries visible to the caller's scope.

## Non-goals

- Search suggestions, recent searches or spelling correction.
- A year on Entity rows: the Entity table has no year column. Filed as a follow-up.
- Restyling the header. The field replaces the Search link in the viewer action cluster only.
- Native clients. They read the new optional row fields when they adopt them.

## Failure modes

- A search request fails: the list shows the existing "search unavailable" message; the field keeps
  the typed query. A cancelled request shows nothing.
- A row whose Library cannot be resolved (sealed encrypted Library, or hidden from the scope) is
  still returned, without library, year or type detail; the web row then falls back to the
  `kind` discriminant for grouping.
- The shortcut never fires while focus is in an input, textarea, select or editable element, so
  typing `/` in a form is unaffected.

## Dependencies

- The web viewer has no Media detail page yet (CORE-1144). Until it lands, a Media row's own page is
  the watch page, so Enter and Play lead to the same place for Media rows. The row keeps the two
  actions separate so CORE-1144 only changes the open target.
