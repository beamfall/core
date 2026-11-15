# Viewer notifications inbox — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:022de9e1c3870011e548cee6056eaee1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/NotificationsPage\.tsx:19; symbol: NOTIFICATIONS\_CHANGED\_EVENT; span SHA256: 7dde5112c416d78842d0a78b8b573bc6866f08d7ac535b0bcf9c82ce0969e72c.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 72039e57e11af1f261be32e0dacda164
    Note over S,U: Source contains call useT at line 183 - order unresolved
    Note over S,U: Source contains call useWatchPartyState at line 184 - order unresolved
    Note over S,U: Source contains call partyRequestRef at line 193 - order unresolved
    Note over S,U: Source contains call notificationKindLabel at line 212 - order unresolved
    Note over S,U: Source contains call t at line 213 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 215 - order unresolved
    Note over S,U: Source contains call onJoinParty at line 227 - order unresolved
    Note over S,U: Source contains call t at line 228 - order unresolved
    Note over S,U: Source contains call onDecideRequest at line 233 - order unresolved
    Note over S,U: Source contains call t at line 234 - order unresolved
    Note over S,U: Source contains call onDecideRequest at line 239 - order unresolved
    Note over S,U: Source contains call t at line 241 - order unresolved
    Note over S,U: Source contains call onMarkRead at line 246 - order unresolved
    Note over S,U: Source contains call t at line 247 - order unresolved
    Note over S,U: Source contains call useT at line 26 - order unresolved
    Note over S,U: Source contains call useNavigate at line 27 - order unresolved
    Note over S,U: Source contains call useState at line 29 - order unresolved
    Note over S,U: Source contains call useState at line 30 - order unresolved
    Note over S,U: Source contains call useCallback at line 33 - order unresolved
    Note over S,U: Source contains call async at line 34 - order unresolved
    Note over S,U: Source contains call setError at line 35 - order unresolved
    Note over S,U: Source contains call setLoading at line 36 - order unresolved
    Note over S,U: Source contains call setNotifications at line 38 - order unresolved
    Note over S,U: Source contains call fetchNotifications at line 38 - order unresolved
    Note over S,U: Source contains call setError at line 40 - order unresolved
    Note over S,U: Source contains call t at line 40 - order unresolved
    Note over S,U: Source contains call setLoading at line 42 - order unresolved
    Note over S,U: Source contains call useEffect at line 48 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 49 - order unresolved
    Note over S,U: Source contains call useEffect at line 52 - order unresolved
    Note over S,U: Source contains call Date at line 279 - order unresolved
    Note over S,U: Source contains call isNaN at line 280 - order unresolved
    Note over S,U: Source contains call getTime at line 280 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 281 - order unresolved
    Note over S,U: Source contains call t at line 258 - order unresolved
    Note over S,U: Source contains call t at line 260 - order unresolved
    Note over S,U: Source contains call t at line 262 - order unresolved
    Note over S,U: Source contains call t at line 264 - order unresolved
    Note over S,U: Source contains call t at line 266 - order unresolved
    Note over S,U: Source contains call setError at line 66 - order unresolved
    Note over S,U: Source contains call markNotificationRead at line 68 - order unresolved
    Note over S,U: Source contains call setNotifications at line 70 - order unresolved
    Note over S,U: Source contains call map at line 71 - order unresolved
    Note over S,U: Source contains call dispatchEvent at line 73 - order unresolved
    Note over S,U: Source contains call Event at line 73 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 76 - order unresolved
    Note over S,U: Source contains call setError at line 78 - order unresolved
    Note over S,U: Source contains call t at line 78 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 55 - order unresolved
```
