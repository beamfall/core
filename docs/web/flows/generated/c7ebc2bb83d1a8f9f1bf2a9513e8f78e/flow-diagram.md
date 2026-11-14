# Console plugins: standing, lifecycle, config editor, provider keys — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:5629adbba4881389c5b286a922a7c260 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Plugins\.tsx:931; symbol: ActionRow; span SHA256: 2a20e7df7d8ac823ea677063b20d2017cc11d6c03e2b7774889185c0c031b315.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c7ebc2bb83d1a8f9f1bf2a9513e8f78e
    Note over S,U: Source contains call useT at line 944 - order unresolved
    Note over S,U: Source contains call useState at line 945 - order unresolved
    Note over S,U: Source contains call useState at line 946 - order unresolved
    Note over S,U: Source contains call displayString at line 947 - order unresolved
    Note over S,U: Source contains call Set at line 46 - order unresolved
    Note over S,U: Source contains call useT at line 805 - order unresolved
    Note over S,U: Source contains call displayString at line 806 - order unresolved
    Note over S,U: Source contains call t at line 807 - order unresolved
    Note over S,U: Source contains call onChange at line 815 - order unresolved
    Note over S,U: Source contains call onChange at line 821 - order unresolved
    Note over S,U: Source contains call t at line 823 - order unresolved
    Note over S,U: Source contains call map at line 826 - order unresolved
    Note over S,U: Source contains call displayString at line 828 - order unresolved
    Note over S,U: Source contains call onChange at line 843 - order unresolved
    Note over S,U: Source contains call useT at line 869 - order unresolved
    Note over S,U: Source contains call displayString at line 874 - order unresolved
    Note over S,U: Source contains call displayString at line 879 - order unresolved
    Note over S,U: Source contains call t at line 884 - order unresolved
    Note over S,U: Source contains call useT at line 304 - order unresolved
    Note over S,U: Source contains call useState at line 305 - order unresolved
    Note over S,U: Source contains call displayString at line 306 - order unresolved
    Note over S,U: Source contains call has at line 307 - order unresolved
    Note over S,U: Source contains call useT at line 703 - order unresolved
    Note over S,U: Source contains call useT at line 528 - order unresolved
    Note over S,U: Source contains call t at line 549 - order unresolved
    Note over S,U: Source contains call map at line 552 - order unresolved
    Note over S,U: Source contains call t at line 561 - order unresolved
    Note over S,U: Source contains call map at line 564 - order unresolved
    Note over S,U: Source contains call t at line 573 - order unresolved
    Note over S,U: Source contains call map at line 576 - order unresolved
    Note over S,U: Source contains call useT at line 611 - order unresolved
    Note over S,U: Source contains call useState at line 612 - order unresolved
    Note over S,U: Source contains call useState at line 613 - order unresolved
    Note over S,U: Source contains call displayString at line 614 - order unresolved
    Note over S,U: Source contains call useT at line 158 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 159 - order unresolved
    Note over S,U: Source contains call useAsync at line 161 - order unresolved
    Note over S,U: Source contains call fetchConsoleConfigWithStanding at line 162 - order unresolved
    Note over S,U: Source contains call useState at line 164 - order unresolved
    Note over S,U: Source contains call useEffect at line 166 - order unresolved
    Note over S,U: Source contains call some at line 170 - order unresolved
    Note over S,U: Source contains call useT at line 428 - order unresolved
    Note over S,U: Source contains call useState at line 429 - order unresolved
    Note over S,U: Source contains call useState at line 430 - order unresolved
    Note over S,U: Source contains call displayString at line 900 - order unresolved
    Note over S,U: Source contains call t at line 275 - order unresolved
    Note over S,U: Source contains call t at line 127 - order unresolved
    Note over S,U: Source contains call t at line 134 - order unresolved
    Note over S,U: Source contains call t at line 137 - order unresolved
    Note over S,U: Source contains call t at line 146 - order unresolved
    Note over S,U: Source contains call t at line 148 - order unresolved
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
