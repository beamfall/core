# Show stored runtime on shelf cards

CORE-2320: the existing card subtitle requirement supplies runtime, but web SourceItems does not request it and poster/thumbnail cards only render runtime in list mode. Request the stored value and show it on non-row Media cards as well. Never invent a duration for a grouped container or an item without one.

The other ticket behaviors already have dedicated sources or landed fixes: Recently Added sorts by insertion order; recently-played and most-played use distinct engagement ordering; container grouping, names, duplicate-title years and Keep browsing are present. Add focused evidence for their ordering while fixing the missing shelf runtime.

Non-goals: change matching, metadata, grouping or playback order. Failure modes: missing values remain absent; row runtime must not be duplicated. Evidence: a failing poster/thumbnail card regression and wire selection test, existing title/grouping tests and focused source ordering tests. Rollback: revert the runtime display and selection.
