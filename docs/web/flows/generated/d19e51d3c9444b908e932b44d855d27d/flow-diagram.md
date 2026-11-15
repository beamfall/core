# Console libraries: add a library, roots, preview clips, skip detection — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:ab09d2fb07cd933e1fc69d6d1e56da09 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/AddLibrary\.tsx:28; symbol: AddLibrary; span SHA256: ab9e95397d7ea3dbc166cab13fb5e5af59c6450dda53e1165bc373941260ee2f.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d19e51d3c9444b908e932b44d855d27d
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 39 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 40 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 41 - order unresolved
    Note over S,U: Source contains call libraryTypeOptions in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 47 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 48 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 49 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/AddLibrary.tsx at line 54 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Libraries.tsx at line 476 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Libraries.tsx at line 477 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Libraries.tsx at line 478 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Libraries.tsx at line 479 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Libraries.tsx at line 480 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Libraries.tsx at line 481 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 482 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/console/sections/Libraries.tsx at line 485 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 486 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/Libraries.tsx at line 489 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Libraries.tsx at line 491 - order unresolved
    Note over S,U: Source contains call queueMicrotask in internal/web/app/src/areas/console/sections/Libraries.tsx at line 495 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/console/sections/Libraries.tsx at line 495 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Libraries.tsx at line 126 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/Libraries.tsx at line 127 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 132 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/Libraries.tsx at line 133 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Libraries.tsx at line 135 - order unresolved
    Note over S,U: Source contains call setSavedRoots in internal/web/app/src/areas/console/sections/Libraries.tsx at line 136 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 138 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 139 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 140 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 142 - order unresolved
    Note over S,U: Source contains call useRescanState in internal/web/app/src/areas/console/sections/Libraries.tsx at line 143 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/Libraries.tsx at line 145 - order unresolved
    Note over S,U: Source contains call useLibraryIconSymbol in internal/web/app/src/areas/console/sections/Libraries.tsx at line 146 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/console/sections/Libraries.tsx at line 150 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Libraries.tsx at line 151 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/console/sections/Libraries.tsx at line 153 - order unresolved
    Note over S,U: Source contains call all in internal/web/app/src/areas/console/sections/Libraries.tsx at line 154 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Libraries.tsx at line 154 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/Libraries.tsx at line 154 - order unresolved
    Note over S,U: Source contains call probeRoot in internal/web/app/src/areas/console/sections/Libraries.tsx at line 154 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/console/sections/Libraries.tsx at line 154 - order unresolved
    Note over S,U: Source contains call setRootProblems in internal/web/app/src/areas/console/sections/Libraries.tsx at line 159 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/console/sections/Libraries.tsx at line 81 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/Libraries.tsx at line 81 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/sections/Libraries.tsx at line 167 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/Libraries.tsx at line 167 - order unresolved
    Note over S,U: Source contains call setScanFolders in internal/web/app/src/areas/console/sections/Libraries.tsx at line 169 - order unresolved
    Note over S,U: Source contains call setScanRoot in internal/web/app/src/areas/console/sections/Libraries.tsx at line 170 - order unresolved
    Note over S,U: Source contains call fetchFolderTree in internal/web/app/src/areas/console/sections/Libraries.tsx at line 174 - order unresolved
    Note over S,U: Source contains call all in internal/web/app/src/areas/console/sections/Libraries.tsx at line 505 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Libraries.tsx at line 505 - order unresolved
    Note over S,U: Source contains call setSavingRoots in internal/web/app/src/areas/console/sections/Libraries.tsx at line 200 - order unresolved
    Note over S,U: Source contains call setRootsError in internal/web/app/src/areas/console/sections/Libraries.tsx at line 201 - order unresolved
    Note over S,U: Source contains call setLibraryRoots in internal/web/app/src/areas/console/sections/Libraries.tsx at line 203 - order unresolved
    Note over S,U: Source contains call setSavedRoots in internal/web/app/src/areas/console/sections/Libraries.tsx at line 204 - order unresolved
    Note over S,U: Source contains call onChanged in internal/web/app/src/areas/console/sections/Libraries.tsx at line 205 - order unresolved
    Note over S,U: Source contains call setRootsError in internal/web/app/src/areas/console/sections/Libraries.tsx at line 208 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Libraries.tsx at line 208 - order unresolved
    Note over S,U: Source contains call setSavingRoots in internal/web/app/src/areas/console/sections/Libraries.tsx at line 212 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/console/sections/Libraries.tsx at line 177 - order unresolved
    Note over S,U: Source contains call forEach in internal/web/app/src/areas/console/sections/Libraries.tsx at line 178 - order unresolved
```
