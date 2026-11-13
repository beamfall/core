# Viewer notifications inbox — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:022de9e1c3870011e548cee6056eaee1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/NotificationsPage\.tsx:119; symbol: NotificationRow; span SHA256: b24148d2db280286c5eac686af7ff3c720a22c3f543f7c474148b109b291bb61.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 72039e57e11af1f261be32e0dacda164
    Note over S,U: Source contains call useT at line 126 - order unresolved
    Note over S,U: Source contains call notificationKindLabel at line 136 - order unresolved
    Note over S,U: Source contains call t at line 137 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 139 - order unresolved
    Note over S,U: Source contains call onMarkRead at line 152 - order unresolved
    Note over S,U: Source contains call t at line 154 - order unresolved
    Note over S,U: Source contains call Date at line 175 - order unresolved
    Note over S,U: Source contains call isNaN at line 176 - order unresolved
    Note over S,U: Source contains call getTime at line 176 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 177 - order unresolved
    Note over S,U: Source contains call t at line 164 - order unresolved
    Note over S,U: Source contains call t at line 166 - order unresolved
    Note over S,U: Source contains call t at line 168 - order unresolved
    Note over S,U: Source contains call t at line 170 - order unresolved
    Note over S,U: Source contains call setError at line 57 - order unresolved
    Note over S,U: Source contains call markNotificationRead at line 59 - order unresolved
    Note over S,U: Source contains call setNotifications at line 61 - order unresolved
    Note over S,U: Source contains call map at line 62 - order unresolved
    Note over S,U: Source contains call dispatchEvent at line 64 - order unresolved
    Note over S,U: Source contains call Event at line 64 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 67 - order unresolved
    Note over S,U: Source contains call setError at line 69 - order unresolved
    Note over S,U: Source contains call t at line 69 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 46 - order unresolved
```
