# Console libraries: add a library, roots, preview clips, skip detection — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:ab09d2fb07cd933e1fc69d6d1e56da09 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Libraries\.tsx:395; symbol: AddLibrary; span SHA256: 5d39f22b21a3c38babdca6c6f64008f3d42514a2b018b17ae8722462992a990f.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d19e51d3c9444b908e932b44d855d27d
    Note over S,U: Source contains call useT at line 404 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 405 - order unresolved
    Note over S,U: Source contains call useState at line 406 - order unresolved
    Note over S,U: Source contains call useState at line 407 - order unresolved
    Note over S,U: Source contains call useT at line 473 - order unresolved
    Note over S,U: Source contains call useAsync at line 474 - order unresolved
    Note over S,U: Source contains call useAsync at line 475 - order unresolved
    Note over S,U: Source contains call useAsync at line 476 - order unresolved
    Note over S,U: Source contains call useAsync at line 477 - order unresolved
    Note over S,U: Source contains call useState at line 478 - order unresolved
    Note over S,U: Source contains call useT at line 112 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 113 - order unresolved
    Note over S,U: Source contains call useState at line 118 - order unresolved
    Note over S,U: Source contains call useRef at line 119 - order unresolved
    Note over S,U: Source contains call useEffect at line 121 - order unresolved
    Note over S,U: Source contains call setSavedRoots at line 122 - order unresolved
    Note over S,U: Source contains call useState at line 124 - order unresolved
    Note over S,U: Source contains call useState at line 125 - order unresolved
    Note over S,U: Source contains call useState at line 126 - order unresolved
    Note over S,U: Source contains call useState at line 128 - order unresolved
    Note over S,U: Source contains call find at line 129 - order unresolved
    Note over S,U: Source contains call split at line 69 - order unresolved
    Note over S,U: Source contains call filter at line 69 - order unresolved
    Note over S,U: Source contains call useCallback at line 131 - order unresolved
    Note over S,U: Source contains call async at line 131 - order unresolved
    Note over S,U: Source contains call setScanFolders at line 133 - order unresolved
    Note over S,U: Source contains call setScanRoot at line 134 - order unresolved
    Note over S,U: Source contains call fetchFolderTree at line 138 - order unresolved
    Note over S,U: Source contains call preventDefault at line 410 - order unresolved
    Note over S,U: Source contains call trim at line 411 - order unresolved
    Note over S,U: Source contains call createLibrary at line 412 - order unresolved
    Note over S,U: Source contains call trim at line 412 - order unresolved
    Note over S,U: Source contains call setTitle at line 413 - order unresolved
    Note over S,U: Source contains call setPluginID at line 414 - order unresolved
    Note over S,U: Source contains call onCreated at line 415 - order unresolved
    Note over S,U: Source contains call all at line 482 - order unresolved
    Note over S,U: Source contains call refresh at line 482 - order unresolved
    Note over S,U: Source contains call setSavingRoots at line 164 - order unresolved
    Note over S,U: Source contains call setRootsError at line 165 - order unresolved
    Note over S,U: Source contains call setLibraryRoots at line 167 - order unresolved
    Note over S,U: Source contains call setSavedRoots at line 168 - order unresolved
    Note over S,U: Source contains call onChanged at line 169 - order unresolved
    Note over S,U: Source contains call setRootsError at line 172 - order unresolved
    Note over S,U: Source contains call t at line 172 - order unresolved
    Note over S,U: Source contains call setSavingRoots at line 176 - order unresolved
    Note over S,U: Source contains call push at line 141 - order unresolved
    Note over S,U: Source contains call forEach at line 142 - order unresolved
```
