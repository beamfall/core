# Player audio and subtitle tracks, subtitle styling — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:1500616c2ec3a004d37072fdc22a79f9 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:411; symbol: buildTrackMenuModel; span SHA256: 08ad1dc2bca1f40d9cee935165267b7cd02908df2a1f69fc208938be66ebbd1a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 76fc5c2449ad0d7ef758f4e866e02adc
    Note over S,U: Source contains call audioTrackList in internal/web/app/src/areas/viewer/player/media-tracks.ts at line 29 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/player/media-tracks.ts at line 33 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 32 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 34 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 36 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 37 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 38 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 42 - order unresolved
    Note over S,U: Source contains call setProperty in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 45 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 45 - order unresolved
    Note over S,U: Source contains call remove in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 46 - order unresolved
    Note over S,U: Source contains call add in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 47 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 83 - order unresolved
    Note over S,U: Source contains call setNudge in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 83 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 50 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 54 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 55 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 63 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 64 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 96 - order unresolved
    Note over S,U: Source contains call setSizeScale in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 97 - order unresolved
    Note over S,U: Source contains call setBackground in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 98 - order unresolved
    Note over S,U: Source contains call setSyncOffset in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 99 - order unresolved
    Note over S,U: Source contains call applySync in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 101 - order unresolved
```
