# Page keyword search results

CORE-2321: keyword search stops after fifty rows. Entity results and container labels already exist; add bounded pages and a Load more action to reach the remaining visible matches.

The graph accepts a nonnegative offset and a page size up to 200 (default 50), returns hasMore, and selects disjoint pages before ordering each page's container members. Existing first-page ranking stays intact. The web app retains accumulated rows, groups siblings in ordinal order, and cancels superseded requests. Failed additional reads preserve the previous rows and permit retry.

Non-goals: semantic/recommend paging, snapshot cursors, ranking changes or new entity types. A changing catalog can change offsets; the client deduplicates by kind and ID. Hidden results never contribute to paging. Evidence: graph tests across more than fifty results and web tests for append, retry, and a query change during loading; existing container/search tests. Rollback: revert the additive fields and Load more control.
