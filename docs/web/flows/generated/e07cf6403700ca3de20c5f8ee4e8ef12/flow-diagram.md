# Manage subscriptions and retention kinds — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:3812876bea2833d22454b7951b6e7f21 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/SubscriptionsPage\.tsx:43; symbol: RETENTION\_KINDS; span SHA256: 5dd4f70d180d5a5bf45e288263059770089c15c92e9c9ebac0706e8f2e0321a6.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow e07cf6403700ca3de20c5f8ee4e8ef12
    Note over S,U: Source contains call useT at line 255 - order unresolved
    Note over S,U: Source contains call useState at line 256 - order unresolved
    Note over S,U: Source contains call useState at line 257 - order unresolved
    Note over S,U: Source contains call String at line 258 - order unresolved
    Note over S,U: Source contains call t at line 272 - order unresolved
    Note over S,U: Source contains call t at line 273 - order unresolved
    Note over S,U: Source contains call map at line 278 - order unresolved
    Note over S,U: Source contains call t at line 280 - order unresolved
    Note over S,U: Source contains call t at line 291 - order unresolved
    Note over S,U: Source contains call t at line 292 - order unresolved
    Note over S,U: Source contains call setN at line 294 - order unresolved
    Note over S,U: Source contains call onSaveRetention at line 302 - order unresolved
    Note over S,U: Source contains call Number at line 302 - order unresolved
    Note over S,U: Source contains call t at line 304 - order unresolved
    Note over S,U: Source contains call t at line 312 - order unresolved
    Note over S,U: Source contains call t at line 317 - order unresolved
    Note over S,U: Source contains call t at line 323 - order unresolved
    Note over S,U: Source contains call onRemove at line 324 - order unresolved
    Note over S,U: Source contains call t at line 325 - order unresolved
    Note over S,U: Source contains call useT at line 55 - order unresolved
    Note over S,U: Source contains call useSession at line 56 - order unresolved
    Note over S,U: Source contains call useState at line 59 - order unresolved
    Note over S,U: Source contains call useState at line 61 - order unresolved
    Note over S,U: Source contains call useState at line 62 - order unresolved
    Note over S,U: Source contains call useState at line 63 - order unresolved
    Note over S,U: Source contains call useState at line 64 - order unresolved
    Note over S,U: Source contains call useRef at line 65 - order unresolved
    Note over S,U: Source contains call useRef at line 66 - order unresolved
    Note over S,U: Source contains call useEffect at line 70 - order unresolved
    Note over S,U: Source contains call setLibraryID at line 71 - order unresolved
    Note over S,U: Source contains call useCallback at line 74 - order unresolved
    Note over S,U: Source contains call async at line 75 - order unresolved
    Note over S,U: Source contains call setError at line 77 - order unresolved
    Note over S,U: Source contains call setSubscriptions at line 78 - order unresolved
    Note over S,U: Source contains call fetchSubscriptions at line 81 - order unresolved
    Note over S,U: Source contains call setSubscriptions at line 82 - order unresolved
    Note over S,U: Source contains call setError at line 84 - order unresolved
    Note over S,U: Source contains call t at line 84 - order unresolved
    Note over S,U: Source contains call useEffect at line 90 - order unresolved
    Note over S,U: Source contains call load at line 91 - order unresolved
    Note over S,U: Source contains call useCallback at line 97 - order unresolved
    Note over S,U: Source contains call async at line 98 - order unresolved
    Note over S,U: Source contains call load at line 100 - order unresolved
    Note over S,U: Source contains call async at line 106 - order unresolved
    Note over S,U: Source contains call setError at line 107 - order unresolved
    Note over S,U: Source contains call trim at line 108 - order unresolved
    Note over S,U: Source contains call setError at line 111 - order unresolved
    Note over S,U: Source contains call t at line 111 - order unresolved
    Note over S,U: Source contains call addSubscription at line 115 - order unresolved
    Note over S,U: Source contains call trim at line 118 - order unresolved
    Note over S,U: Source contains call trim at line 119 - order unresolved
    Note over S,U: Source contains call setFeedURL at line 122 - order unresolved
    Note over S,U: Source contains call setRoot at line 123 - order unresolved
    Note over S,U: Source contains call setAuthHeader at line 124 - order unresolved
    Note over S,U: Source contains call refreshMutationLibrary at line 125 - order unresolved
    Note over S,U: Source contains call setError at line 129 - order unresolved
    Note over S,U: Source contains call t at line 129 - order unresolved
    Note over S,U: Source contains call async at line 147 - order unresolved
    Note over S,U: Source contains call setError at line 149 - order unresolved
    Note over S,U: Source contains call removeSubscription at line 151 - order unresolved
    Note over S,U: Source contains call refreshMutationLibrary at line 152 - order unresolved
    Note over S,U: Source contains call setError at line 154 - order unresolved
    Note over S,U: Source contains call t at line 154 - order unresolved
    Note over S,U: Source contains call async at line 134 - order unresolved
    Note over S,U: Source contains call setError at line 136 - order unresolved
    Note over S,U: Source contains call setSubscriptionRetention at line 138 - order unresolved
    Note over S,U: Source contains call refreshMutationLibrary at line 139 - order unresolved
    Note over S,U: Source contains call setError at line 142 - order unresolved
    Note over S,U: Source contains call t at line 142 - order unresolved
```
