# Player up\-next countdown and play queue navigation — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b5b269684b9427142beb259b4bb203d8 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/playQueue\.ts:38; symbol: activeQueue; span SHA256: 156d08e1411f0c5295a544e371ec2c766c57837a66aeabe9536310216fb87ab8.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow fa22fc31bb8d11236cda2f671f818c4a
    Note over S,U: Source contains call getIdentityEpochSignal in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 39 - order unresolved
    Note over S,U: Source contains call activeQueue in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 141 - order unresolved
    Note over S,U: Source contains call fetchLibraryItems in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 102 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 107 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 111 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 112 - order unresolved
    Note over S,U: Source contains call playable in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 112 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 112 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 114 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 115 - order unresolved
    Note over S,U: Source contains call activeQueue in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 128 - order unresolved
    Note over S,U: Source contains call activeQueue in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 133 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 133 - order unresolved
    Note over S,U: Source contains call activeQueue in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 161 - order unresolved
    Note over S,U: Source contains call positionOf in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 163 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 166 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 329 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 333 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/watchQueue.ts at line 40 - order unresolved
    Note over S,U: Source contains call isArray in internal/web/app/src/areas/viewer/lib/watchQueue.ts at line 85 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/watchQueue.ts at line 86 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 19 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 21 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 22 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 25 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 26 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 27 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 31 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/UpNextCard.tsx at line 34 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 31 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 32 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 33 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 34 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 36 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 38 - order unresolved
    Note over S,U: Source contains call getIdentityEpochSignal in internal/web/app/src/areas/viewer/lib/playQueue.ts at line 79 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 48 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 50 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 52 - order unresolved
    Note over S,U: Source contains call setVisible in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 55 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 91 - order unresolved
    Note over S,U: Source contains call setVisible in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 92 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 41 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 43 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 59 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 61 - order unresolved
    Note over S,U: Source contains call setVisible in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 62 - order unresolved
    Note over S,U: Source contains call inWindow in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 97 - order unresolved
    Note over S,U: Source contains call cancel in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 99 - order unresolved
    Note over S,U: Source contains call setCountdown in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 104 - order unresolved
    Note over S,U: Source contains call setVisible in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 105 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 106 - order unresolved
    Note over S,U: Source contains call inWindow in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 107 - order unresolved
    Note over S,U: Source contains call cancel in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 108 - order unresolved
    Note over S,U: Source contains call setCountdown in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 116 - order unresolved
    Note over S,U: Source contains call advance in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 118 - order unresolved
    Note over S,U: Source contains call maybeShow in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 124 - order unresolved
    Note over S,U: Source contains call maybeShow in internal/web/app/src/areas/viewer/player/useUpNext.ts at line 123 - order unresolved
```
