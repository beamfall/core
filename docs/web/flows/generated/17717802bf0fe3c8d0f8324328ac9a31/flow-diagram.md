# Viewer media card: preview dwell, state overlay, card menu, artwork — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:7ed54513f84204eb1d819ccf1fe69a28 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/components/MediaItemCard\.tsx:283; symbol: CardStateOverlay; span SHA256: 26335e262852cff9dc288f54e524fdb41d5b7de94e8e3b75867f868698d359ae.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 17717802bf0fe3c8d0f8324328ac9a31
    Note over S,U: Source contains call useNavigate in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 57 - order unresolved
    Note over S,U: Source contains call useViewerIdentity in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 58 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 59 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 60 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 61 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 62 - order unresolved
    Note over S,U: Source contains call cardState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 64 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 68 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 69 - order unresolved
    Note over S,U: Source contains call artworkSlotURL in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 70 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 73 - order unresolved
    Note over S,U: Source contains call matchMedia in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 317 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useItemActions in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 50 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 52 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/cardState.ts at line 26 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/cardState.ts at line 32 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/lib/cardState.ts at line 40 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/cardState.ts at line 40 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/areas/viewer/lib/cardState.ts at line 40 - order unresolved
    Note over S,U: Source contains call timeLeftLabel in internal/web/app/src/areas/viewer/lib/cardState.ts at line 41 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/lib/cardState.ts at line 57 - order unresolved
    Note over S,U: Source contains call ceil in internal/web/app/src/areas/viewer/lib/cardState.ts at line 57 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/cardState.ts at line 59 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/lib/cardState.ts at line 59 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/lib/cardState.ts at line 61 - order unresolved
    Note over S,U: Source contains call play in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 146 - order unresolved
    Note over S,U: Source contains call mediaLinkState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 148 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 149 - order unresolved
    Note over S,U: Source contains call queueState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 149 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 151 - order unresolved
    Note over S,U: Source contains call entityLinkState in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 151 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 158 - order unresolved
    Note over S,U: Source contains call fetchPreviewInfo in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 160 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 161 - order unresolved
    Note over S,U: Source contains call setPreviewClip in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 163 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 166 - order unresolved
    Note over S,U: Source contains call setPreviewClip in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 168 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 75 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 77 - order unresolved
    Note over S,U: Source contains call preloadPlayer in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 175 - order unresolved
    Note over S,U: Source contains call clearPreviewTimer in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 176 - order unresolved
    Note over S,U: Source contains call canStartPreview in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 177 - order unresolved
    Note over S,U: Source contains call beginPreviewDwell in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 180 - order unresolved
    Note over S,U: Source contains call stopPreview in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 97 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 82 - order unresolved
    Note over S,U: Source contains call clearPreviewTimer in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 83 - order unresolved
    Note over S,U: Source contains call setPreviewClip in internal/web/app/src/areas/viewer/components/MediaItemCard.tsx at line 85 - order unresolved
    Note over S,U: Source contains call writeText in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 59 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 59 - order unresolved
    Note over S,U: Source contains call setCopied in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 61 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 62 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setCopied in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setCopied in internal/web/app/src/areas/viewer/components/MediaItemCardMenu.tsx at line 65 - order unresolved
```
