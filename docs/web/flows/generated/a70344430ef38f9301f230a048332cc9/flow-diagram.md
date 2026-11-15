# Player transport: seek bar, trickplay, markers, chapters, renditions, touch gestures — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e56c4e24895f7bfec80b11f456e2848e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:10; symbol: DEFAULT\_TRICKPLAY\_COLUMNS; span SHA256: 73d34cd672bd70bad3928a6a449f541fe4a0719dd92af9ecfb2069497d12c8de.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow a70344430ef38f9301f230a048332cc9
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 593 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 688 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 690 - order unresolved
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 700 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/lib/transport.ts at line 702 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 703 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 706 - order unresolved
    Note over S,U: Source contains call renditionLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 706 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 708 - order unresolved
    Note over S,U: Source contains call formatBitrate in internal/web/app/src/areas/viewer/lib/transport.ts at line 710 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 714 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 717 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 724 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 732 - order unresolved
    Note over S,U: Source contains call reasonLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 732 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/lib/transport.ts at line 300 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/transport.ts at line 301 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/lib/transport.ts at line 303 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 303 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/transport.ts at line 304 - order unresolved
    Note over S,U: Source contains call sort in internal/web/app/src/areas/viewer/lib/transport.ts at line 306 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/viewer/lib/transport.ts at line 25 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/lib/transport.ts at line 26 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 511 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 110 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 110 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 110 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 112 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 122 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 122 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 188 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/lib/transport.ts at line 188 - order unresolved
    Note over S,U: Source contains call findIndex in internal/web/app/src/areas/viewer/lib/transport.ts at line 533 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 533 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 525 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 127 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 127 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 127 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 128 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 129 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 133 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 136 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 139 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/viewer/lib/transport.ts at line 141 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 174 - order unresolved
    Note over S,U: Source contains call songSecondsPerStreamSecond in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 177 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 178 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 183 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1209 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1209 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1210 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1245 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1246 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1247 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1221 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1224 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 40 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 46 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 49 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 52 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 56 - order unresolved
    Note over S,U: Source contains call onSelect in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 61 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 61 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/player/TransportMenu.tsx at line 86 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 22 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 24 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 114 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/viewer/lib/transport.ts at line 114 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 239 - order unresolved
    Note over S,U: Source contains call setSettingsOpen in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 240 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 241 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 242 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 184 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 616 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 617 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 382 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 383 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 385 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 466 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 468 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 469 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 471 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 472 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 472 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 472 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 592 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 594 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 623 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 624 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 624 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 624 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 604 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 608 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 609 - order unresolved
    Note over S,U: Source contains call setHover in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 629 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 631 - order unresolved
    Note over S,U: Source contains call resolveTrickplayTile in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 634 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 636 - order unresolved
    Note over S,U: Source contains call resolveTrickplaySheetURL in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 639 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 640 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 668 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 668 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 266 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 267 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 581 - order unresolved
    Note over S,U: Source contains call markViewerIntent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 584 - order unresolved
    Note over S,U: Source contains call setCurrent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 586 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 419 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 420 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 430 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 431 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 431 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 521 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 525 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 527 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 531 - order unresolved
    Note over S,U: Source contains call setMuted in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 535 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 564 - order unresolved
    Note over S,U: Source contains call exitPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 567 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 568 - order unresolved
    Note over S,U: Source contains call requestPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 570 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 571 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 574 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 575 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 511 - order unresolved
    Note over S,U: Source contains call play in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 515 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 515 - order unresolved
    Note over S,U: Source contains call pause in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 517 - order unresolved
    Note over S,U: Source contains call hypot in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 479 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 485 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 488 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 489 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 489 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 27 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 29 - order unresolved
    Note over S,U: Source contains call reveal in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 67 - order unresolved
    Note over S,U: Source contains call setChromeHidden in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 69 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 70 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 34 - order unresolved
    Note over S,U: Source contains call setChromeHidden in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 35 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 36 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 40 - order unresolved
    Note over S,U: Source contains call setChromeHidden in internal/web/app/src/areas/viewer/player/useChromeAutoHide.ts at line 42 - order unresolved
```
