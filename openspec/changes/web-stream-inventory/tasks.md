## 1. Implementation

- [x] 1.1 Keep the full `streams` inventory in `PlayerState` (CORE-0048)
- [x] 1.2 Build the quality menu from the HLS sibling on direct play and switch on selection
- [x] 1.3 Fail over once from direct play to the HLS sibling on `MediaError` 3 or 4, keeping play intent
- [x] 1.4 Probe the stream on a native `MEDIA_ERR_NETWORK` with a timeout; 401/403/410 is reveal-terminal
- [x] 1.5 Use one canonical rendition key for the menu id, selection and `hls.levels` lookup

## 2. Verification

- [x] 2.1 `usePlayer.test.tsx` and `transport.test.ts` cases for each requirement
- [x] 2.2 `npm run check`, `npm run typecheck`, vitest, `openspec validate --all --strict`
