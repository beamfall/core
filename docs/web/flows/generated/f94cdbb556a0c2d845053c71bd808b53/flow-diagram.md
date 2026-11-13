# Viewer item actions: mark watched, watchlist, rating, engagement sync — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:94cd4425ea529e3382576b74c56f578e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/actions/ItemActions\.tsx:14; symbol: ItemActions; span SHA256: 85ab1520079dbcd0e9f1b630a4fe1893bc09c86ff3fc09210cc70e03ae75cf84.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow f94cdbb556a0c2d845053c71bd808b53
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 21 - order unresolved
    Note over S,U: Source contains call useItemActions in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 23 - order unresolved
    Note over S,U: Source contains call ceil in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 27 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 30 - order unresolved
    Note over S,U: Source contains call setWatched in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 36 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 39 - order unresolved
    Note over S,U: Source contains call setWatchlisted in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 46 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 49 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 52 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 53 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 60 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setRating in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 63 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 71 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/actions/ItemActions.tsx at line 76 - order unresolved
    Note over S,U: Source contains call parse in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 43 - order unresolved
    Note over S,U: Source contains call listener in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 27 - order unresolved
    Note over S,U: Source contains call add in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 20 - order unresolved
    Note over S,U: Source contains call delete in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 21 - order unresolved
    Note over S,U: Source contains call useSyncExternalStore in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 32 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 52 - order unresolved
    Note over S,U: Source contains call subscribeEvents in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 54 - order unresolved
    Note over S,U: Source contains call isEngagementInvalidation in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 57 - order unresolved
    Note over S,U: Source contains call notifyEngagementChanged in internal/web/app/src/areas/viewer/actions/engagementSync.ts at line 59 - order unresolved
    Note over S,U: Source contains call targetKey in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 36 - order unresolved
    Note over S,U: Source contains call useEngagementEpoch in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 37 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 39 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 40 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 41 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 42 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 44 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 48 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 51 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 54 - order unresolved
    Note over S,U: Source contains call fetchItemState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 55 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 56 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 57 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 58 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 60 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 61 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 63 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 66 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 66 - order unresolved
    Note over S,U: Source contains call setPending in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 68 - order unresolved
    Note over S,U: Source contains call action in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 70 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 71 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 72 - order unresolved
    Note over S,U: Source contains call notifyEngagementChanged in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 73 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 75 - order unresolved
    Note over S,U: Source contains call setPending in internal/web/app/src/areas/viewer/actions/useItemActions.ts at line 77 - order unresolved
```
