## ADDED Requirements

### Requirement: Keyword search can load beyond fifty results

Keyword search SHALL accept bounded pages with a nonnegative offset and a limit from 1 to 200 (default 50), preserving existing first-page selection and container ordering. Its hasMore flag SHALL describe only visible matches. The web search page SHALL offer Load more while another page exists, append distinct rows, keep accumulated container children in ordinal order, and stop offering the action after the last page.

#### Scenario: More than fifty matches

- **WHEN** a query has sixty visible matches and the viewer loads more after the first fifty
- **THEN** all sixty distinct matches are available and Load more disappears

#### Scenario: Additional page fails

- **WHEN** loading another page fails
- **THEN** previous results remain visible and the action can be retried

#### Scenario: Query changes during paging

- **WHEN** a query or mode changes while a page is pending
- **THEN** the pending page is aborted and cannot append to the new result set
