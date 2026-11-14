# Player transport: seek bar, trickplay, markers, chapters, renditions, touch gestures — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

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
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 121 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 122 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 127 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1084 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1084 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1085 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1120 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1121 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1122 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1096 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 1099 - order unresolved
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
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 182 - order unresolved
    Note over S,U: Source contains call setSettingsOpen in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 183 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 184 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 185 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 128 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 532 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 533 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 282 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 283 - order unresolved
    Note over S,U: Source contains call Boolean in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 443 - order unresolved
    Note over S,U: Source contains call closest in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 444 - order unresolved
    Note over S,U: Source contains call interactive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 455 - order unresolved
    Note over S,U: Source contains call toLowerCase in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 459 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 465 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 466 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 467 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 468 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 472 - order unresolved
    Note over S,U: Source contains call action in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 473 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 285 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 366 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 368 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 369 - order unresolved
    Note over S,U: Source contains call setVolume in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 371 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 372 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 372 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 372 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 508 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 510 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 539 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 540 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 540 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 540 - order unresolved
    Note over S,U: Source contains call setPlaybackPosition in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 520 - order unresolved
    Note over S,U: Source contains call setScrubValue in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 524 - order unresolved
    Note over S,U: Source contains call previewAt in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 525 - order unresolved
    Note over S,U: Source contains call setHover in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 545 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 547 - order unresolved
    Note over S,U: Source contains call resolveTrickplayTile in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 550 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 552 - order unresolved
    Note over S,U: Source contains call URL in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 557 - order unresolved
    Note over S,U: Source contains call setTrickplay in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 561 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 585 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 585 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 209 - order unresolved
    Note over S,U: Source contains call setOpenKey in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 210 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 500 - order unresolved
    Note over S,U: Source contains call setCurrent in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 502 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 319 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 320 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 330 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 331 - order unresolved
    Note over S,U: Source contains call setGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 331 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 421 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 425 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 427 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 431 - order unresolved
    Note over S,U: Source contains call setMuted in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 435 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 483 - order unresolved
    Note over S,U: Source contains call exitPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 486 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 487 - order unresolved
    Note over S,U: Source contains call requestPictureInPicture in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 489 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 490 - order unresolved
    Note over S,U: Source contains call setPipFailure in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 493 - order unresolved
    Note over S,U: Source contains call setPipActive in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 494 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 411 - order unresolved
    Note over S,U: Source contains call play in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 415 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 415 - order unresolved
    Note over S,U: Source contains call pause in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 417 - order unresolved
    Note over S,U: Source contains call hypot in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 379 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 385 - order unresolved
    Note over S,U: Source contains call seekBy in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 388 - order unresolved
    Note over S,U: Source contains call showGestureFeedback in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 389 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/viewer/player/PlayerTransport.tsx at line 389 - order unresolved
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
