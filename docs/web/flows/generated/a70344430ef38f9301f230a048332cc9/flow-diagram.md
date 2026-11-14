# Player transport: seek bar, trickplay, markers, chapters, renditions, touch gestures — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e56c4e24895f7bfec80b11f456e2848e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:10; symbol: DEFAULT\_TRICKPLAY\_COLUMNS; span SHA256: 73d34cd672bd70bad3928a6a449f541fe4a0719dd92af9ecfb2069497d12c8de.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow a70344430ef38f9301f230a048332cc9
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 538 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 633 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 635 - order unresolved
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/transport.ts at line 645 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/lib/transport.ts at line 647 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 648 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 651 - order unresolved
    Note over S,U: Source contains call renditionLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 651 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 653 - order unresolved
    Note over S,U: Source contains call formatBitrate in internal/web/app/src/areas/viewer/lib/transport.ts at line 655 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 659 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 662 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 669 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 677 - order unresolved
    Note over S,U: Source contains call reasonLabel in internal/web/app/src/areas/viewer/lib/transport.ts at line 677 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/lib/transport.ts at line 289 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/transport.ts at line 290 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/lib/transport.ts at line 292 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 292 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/transport.ts at line 293 - order unresolved
    Note over S,U: Source contains call sort in internal/web/app/src/areas/viewer/lib/transport.ts at line 295 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/viewer/lib/transport.ts at line 25 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/lib/transport.ts at line 26 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 456 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 99 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 99 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 99 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 100 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 101 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 111 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 177 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/lib/transport.ts at line 177 - order unresolved
    Note over S,U: Source contains call findIndex in internal/web/app/src/areas/viewer/lib/transport.ts at line 478 - order unresolved
    Note over S,U: Source contains call renditionKey in internal/web/app/src/areas/viewer/lib/transport.ts at line 478 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 470 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/transport.ts at line 116 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 116 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 116 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 117 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/transport.ts at line 118 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 122 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 125 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/transport.ts at line 128 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/viewer/lib/transport.ts at line 130 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 117 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 118 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 123 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1079 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1079 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1080 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1115 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1116 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1117 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1091 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1094 - order unresolved
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
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/transport.ts at line 103 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/viewer/lib/transport.ts at line 103 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 178 - order unresolved
    Note over S,U: Source contains call setSettingsOpen in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 179 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 180 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 181 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 124 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 528 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 529 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 278 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 279 - order unresolved
    Note over S,U: Source contains call Boolean in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 439 - order unresolved
    Note over S,U: Source contains call closest in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 440 - order unresolved
    Note over S,U: Source contains call interactive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 451 - order unresolved
    Note over S,U: Source contains call toLowerCase in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 455 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 461 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 462 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 463 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 464 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 468 - order unresolved
    Note over S,U: Source contains call action in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 469 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 281 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 362 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 364 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 365 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 367 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 368 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 368 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 368 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 504 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 506 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 535 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 536 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 536 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 536 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 516 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 520 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 521 - order unresolved
    Note over S,U: Source contains call setHover in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 541 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 543 - order unresolved
    Note over S,U: Source contains call resolveTrickplayTile in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 546 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 548 - order unresolved
    Note over S,U: Source contains call URL in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 553 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 557 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 581 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 581 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 205 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 206 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 496 - order unresolved
    Note over S,U: Source contains call setCurrent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 498 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 315 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 316 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 326 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 327 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 327 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 417 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 421 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 423 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 427 - order unresolved
    Note over S,U: Source contains call setMuted in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 431 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 479 - order unresolved
    Note over S,U: Source contains call exitPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 482 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 483 - order unresolved
    Note over S,U: Source contains call requestPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 485 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 486 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 489 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 490 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 407 - order unresolved
    Note over S,U: Source contains call play in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 411 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 411 - order unresolved
    Note over S,U: Source contains call pause in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 413 - order unresolved
    Note over S,U: Source contains call hypot in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 375 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 381 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 384 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 385 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 385 - order unresolved
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
