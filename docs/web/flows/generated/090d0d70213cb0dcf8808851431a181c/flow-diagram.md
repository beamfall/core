# Promote an enrolled browser to a manager device — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:4e9dde93fcd9f338a9dc234b54dddd72 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/PromotePage\.tsx:22; symbol: PromotePage; span SHA256: f5a6481e3b322f72550819d0b73f61a52ac2e6cc5a95d9eaf4f34dba04c53f31.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 090d0d70213cb0dcf8808851431a181c
    Note over S,U: Source contains call useT at line 23 - order unresolved
    Note over S,U: Source contains call useNavigate at line 24 - order unresolved
    Note over S,U: Source contains call useSession at line 25 - order unresolved
    Note over S,U: Source contains call useId at line 26 - order unresolved
    Note over S,U: Source contains call useId at line 27 - order unresolved
    Note over S,U: Source contains call useState at line 29 - order unresolved
    Note over S,U: Source contains call useState at line 31 - order unresolved
    Note over S,U: Source contains call preventDefault at line 34 - order unresolved
    Note over S,U: Source contains call setBusy at line 36 - order unresolved
    Note over S,U: Source contains call setError at line 37 - order unresolved
    Note over S,U: Source contains call promote at line 39 - order unresolved
    Note over S,U: Source contains call trim at line 39 - order unresolved
    Note over S,U: Source contains call navigate at line 40 - order unresolved
    Note over S,U: Source contains call t at line 45 - order unresolved
    Note over S,U: Source contains call t at line 47 - order unresolved
    Note over S,U: Source contains call t at line 48 - order unresolved
    Note over S,U: Source contains call t at line 49 - order unresolved
    Note over S,U: Source contains call setError at line 50 - order unresolved
    Note over S,U: Source contains call setBusy at line 51 - order unresolved
    Note over S,U: Source contains call requestAnimationFrame at line 52 - order unresolved
    Note over S,U: Source contains call focus at line 52 - order unresolved
```
