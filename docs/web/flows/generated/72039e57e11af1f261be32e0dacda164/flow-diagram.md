# Viewer notifications inbox — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:022de9e1c3870011e548cee6056eaee1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/NotificationsPage\.tsx:18; symbol: NOTIFICATIONS\_CHANGED\_EVENT; span SHA256: 7dde5112c416d78842d0a78b8b573bc6866f08d7ac535b0bcf9c82ce0969e72c.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 72039e57e11af1f261be32e0dacda164
    Note over S,U: Source contains call useT at line 185 - order unresolved
    Note over S,U: Source contains call useWatchPartyState at line 186 - order unresolved
    Note over S,U: Source contains call partyRequestRef at line 195 - order unresolved
    Note over S,U: Source contains call notificationKindLabel at line 214 - order unresolved
    Note over S,U: Source contains call t at line 215 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 217 - order unresolved
    Note over S,U: Source contains call onJoinParty at line 229 - order unresolved
    Note over S,U: Source contains call t at line 230 - order unresolved
    Note over S,U: Source contains call onDecideRequest at line 235 - order unresolved
    Note over S,U: Source contains call t at line 236 - order unresolved
    Note over S,U: Source contains call onDecideRequest at line 241 - order unresolved
    Note over S,U: Source contains call t at line 243 - order unresolved
    Note over S,U: Source contains call onMarkRead at line 248 - order unresolved
    Note over S,U: Source contains call t at line 249 - order unresolved
    Note over S,U: Source contains call useT at line 25 - order unresolved
    Note over S,U: Source contains call useNavigate at line 26 - order unresolved
    Note over S,U: Source contains call useState at line 28 - order unresolved
    Note over S,U: Source contains call useState at line 29 - order unresolved
    Note over S,U: Source contains call useCallback at line 32 - order unresolved
    Note over S,U: Source contains call async at line 33 - order unresolved
    Note over S,U: Source contains call setError at line 34 - order unresolved
    Note over S,U: Source contains call setLoading at line 35 - order unresolved
    Note over S,U: Source contains call setNotifications at line 37 - order unresolved
    Note over S,U: Source contains call fetchNotifications at line 37 - order unresolved
    Note over S,U: Source contains call setError at line 39 - order unresolved
    Note over S,U: Source contains call t at line 39 - order unresolved
    Note over S,U: Source contains call setLoading at line 41 - order unresolved
    Note over S,U: Source contains call useEffect at line 47 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 48 - order unresolved
    Note over S,U: Source contains call useEffect at line 51 - order unresolved
    Note over S,U: Source contains call Date at line 281 - order unresolved
    Note over S,U: Source contains call isNaN at line 282 - order unresolved
    Note over S,U: Source contains call getTime at line 282 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 283 - order unresolved
    Note over S,U: Source contains call t at line 260 - order unresolved
    Note over S,U: Source contains call t at line 262 - order unresolved
    Note over S,U: Source contains call t at line 264 - order unresolved
    Note over S,U: Source contains call t at line 266 - order unresolved
    Note over S,U: Source contains call t at line 268 - order unresolved
    Note over S,U: Source contains call setError at line 65 - order unresolved
    Note over S,U: Source contains call markNotificationRead at line 67 - order unresolved
    Note over S,U: Source contains call setNotifications at line 69 - order unresolved
    Note over S,U: Source contains call map at line 70 - order unresolved
    Note over S,U: Source contains call dispatchEvent at line 72 - order unresolved
    Note over S,U: Source contains call Event at line 72 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 75 - order unresolved
    Note over S,U: Source contains call setError at line 77 - order unresolved
    Note over S,U: Source contains call t at line 77 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 54 - order unresolved
```
