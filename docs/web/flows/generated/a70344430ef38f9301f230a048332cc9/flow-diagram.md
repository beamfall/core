# Player transport: seek bar, trickplay, markers, chapters, renditions, touch gestures — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e56c4e24895f7bfec80b11f456e2848e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:11; symbol: DEFAULT\_TRICKPLAY\_COLUMNS; span SHA256: 73d34cd672bd70bad3928a6a449f541fe4a0719dd92af9ecfb2069497d12c8de.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow a70344430ef38f9301f230a048332cc9
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 603 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 711 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 713 - order unresolved
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 723 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/lib/transport.ts at line 725 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 726 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 729 - order unresolved
    Note over S,U: Source contains call renditionLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 729 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 731 - order unresolved
    Note over S,U: Source contains call formatBitrate in internal/web/app/src/areas/viewer/lib/transport.ts at line 733 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 737 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 740 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 747 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 755 - order unresolved
    Note over S,U: Source contains call reasonLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 755 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/lib/transport.ts at line 310 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/transport.ts at line 311 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/lib/transport.ts at line 313 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 313 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/transport.ts at line 314 - order unresolved
    Note over S,U: Source contains call sort in internal/web/app/src/areas/viewer/lib/transport.ts at line 316 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/viewer/lib/transport.ts at line 26 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/lib/transport.ts at line 27 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 521 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 112 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 113 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 123 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 123 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 198 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/lib/transport.ts at line 198 - order unresolved
    Note over S,U: Source contains call findIndex in internal/web/app/src/areas/viewer/lib/transport.ts at line 543 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 543 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 535 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 134 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 134 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 134 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 135 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 136 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 139 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/transport.ts at line 139 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 140 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/transport.ts at line 140 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 142 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/transport.ts at line 142 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/viewer/lib/transport.ts at line 144 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 173 - order unresolved
    Note over S,U: Source contains call songSecondsPerStreamSecond in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 176 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 177 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 182 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1203 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1203 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1204 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1215 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1218 - order unresolved
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
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 115 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/viewer/lib/transport.ts at line 115 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 238 - order unresolved
    Note over S,U: Source contains call setSettingsOpen in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 239 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 240 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 241 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 183 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 615 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 616 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 381 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 382 - order unresolved
    Note over S,U: Source contains call Boolean in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 45 - order unresolved
    Note over S,U: Source contains call closest in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 45 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 49 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 53 - order unresolved
    Note over S,U: Source contains call useFrameClock in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 55 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/usePlayerShortcuts.ts at line 57 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 384 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 465 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 467 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 468 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 470 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 471 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 471 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 471 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 591 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 593 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 622 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 623 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 623 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 623 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 603 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 607 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 608 - order unresolved
    Note over S,U: Source contains call setHover in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 628 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 630 - order unresolved
    Note over S,U: Source contains call resolveTrickplayTile in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 633 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 635 - order unresolved
    Note over S,U: Source contains call resolveTrickplaySheetURL in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 638 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 639 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 667 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 667 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 265 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 266 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 580 - order unresolved
    Note over S,U: Source contains call markViewerIntent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 583 - order unresolved
    Note over S,U: Source contains call setCurrent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 585 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 418 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 419 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 429 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 430 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 430 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 520 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 524 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 526 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 530 - order unresolved
    Note over S,U: Source contains call setMuted in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 534 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 563 - order unresolved
    Note over S,U: Source contains call exitPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 566 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 567 - order unresolved
    Note over S,U: Source contains call requestPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 569 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 570 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 573 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 574 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 510 - order unresolved
    Note over S,U: Source contains call play in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 514 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 514 - order unresolved
    Note over S,U: Source contains call pause in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 516 - order unresolved
    Note over S,U: Source contains call hypot in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 478 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 484 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 487 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 488 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 488 - order unresolved
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
