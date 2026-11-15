# Player audio and subtitle tracks, subtitle styling — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:1500616c2ec3a004d37072fdc22a79f9 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:450; symbol: TrackMenuLabels; span SHA256: 254b8c899b00bab907d731ac06734822eab5d6277240a1a9c5867ae572c8b104.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 76fc5c2449ad0d7ef758f4e866e02adc
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/SubtitleStylePanel.tsx at line 87 - order unresolved
    Note over S,U: Source contains call NumberFormat in internal/web/app/src/areas/viewer/player/SubtitleStylePanel.tsx at line 88 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/viewer/player/SubtitleStylePanel.tsx at line 88 - order unresolved
    Note over S,U: Source contains call audioTrackList in internal/web/app/src/areas/viewer/player/media-tracks.ts at line 29 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/player/media-tracks.ts at line 33 - order unresolved
    Note over S,U: Source contains call audioTracksArray in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 54 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 54 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 63 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 63 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 114 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 37 - order unresolved
    Note over S,U: Source contains call readSync in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 37 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 39 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 40 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 41 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 43 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 115 - order unresolved
    Note over S,U: Source contains call setNudge in internal/web/app/src/areas/viewer/player/useLiveTracks.ts at line 115 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 46 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 50 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 51 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 59 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 60 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 139 - order unresolved
    Note over S,U: Source contains call commitOffset in internal/web/app/src/areas/viewer/player/useSubtitleStyling.ts at line 140 - order unresolved
```
