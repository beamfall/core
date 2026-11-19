# Count category browse units

CORE-2552: Categories currently count every episode although viewers browse their outermost containers.

Only requests with `categoryCounts: true` resolve the extra membership so ordinary library pages do not traverse the whole library’s containers. Add parallel browse counts and plugin-supplied labels to category facets. Preserve raw Media counts and loose-Media behavior. Resolve containers with the existing declared-relation traversal; count distinct IDs, not titles, and retain visibility filtering. Categories whose members mix loose Media or container kinds retain item counts rather than claim a homogeneous unit.

Non-goals: change category navigation, matching, or library membership. Failure modes: hidden members must not influence counts; unavailable labels fall back to item counts. Evidence: graph fixtures for shared containers and loose items, web category rendering tests, lint/unit tests and the viewer Playwright spec. Rollback: revert this additive change; existing counts remain usable.
