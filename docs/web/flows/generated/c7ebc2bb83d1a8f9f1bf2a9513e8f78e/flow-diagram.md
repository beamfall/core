# Console plugins: standing, lifecycle, config editor, provider keys — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:5629adbba4881389c5b286a922a7c260 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Plugins\.tsx:190; symbol: credentialAction; span SHA256: 2ec0e1f09df30b70cd31ff7160b9849f00cfb715504c7571ae12fe761a0d7091.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c7ebc2bb83d1a8f9f1bf2a9513e8f78e
    Note over S,U: Source contains call setActionFailed at line 191 - order unresolved
    Note over S,U: Source contains call action at line 193 - order unresolved
    Note over S,U: Source contains call refresh at line 194 - order unresolved
    Note over S,U: Source contains call setActionFailed at line 197 - order unresolved
    Note over S,U: Source contains call setBusy at line 629 - order unresolved
    Note over S,U: Source contains call setFailed at line 630 - order unresolved
    Note over S,U: Source contains call onDeactivate at line 632 - order unresolved
    Note over S,U: Source contains call setBusy at line 634 - order unresolved
    Note over S,U: Source contains call useMemo at line 704 - order unresolved
    Note over S,U: Source contains call fieldValues at line 704 - order unresolved
    Note over S,U: Source contains call useMemo at line 308 - order unresolved
    Note over S,U: Source contains call groupProviderKeys at line 308 - order unresolved
    Note over S,U: Source contains call graphql at line 203 - order unresolved
    Note over S,U: Source contains call refresh at line 207 - order unresolved
    Note over S,U: Source contains call trim at line 725 - order unresolved
    Note over S,U: Source contains call trim at line 726 - order unresolved
    Note over S,U: Source contains call push at line 728 - order unresolved
    Note over S,U: Source contains call setBusy at line 731 - order unresolved
    Note over S,U: Source contains call setSaved at line 732 - order unresolved
    Note over S,U: Source contains call setFailed at line 733 - order unresolved
    Note over S,U: Source contains call onSave at line 735 - order unresolved
    Note over S,U: Source contains call setSaved at line 736 - order unresolved
    Note over S,U: Source contains call setFailed at line 738 - order unresolved
    Note over S,U: Source contains call setBusy at line 740 - order unresolved
    Note over S,U: Source contains call publishMutation at line 183 - order unresolved
    Note over S,U: Source contains call setPluginConfig at line 183 - order unresolved
    Note over S,U: Source contains call publishMutation at line 179 - order unresolved
    Note over S,U: Source contains call setPluginActive at line 179 - order unresolved
```
