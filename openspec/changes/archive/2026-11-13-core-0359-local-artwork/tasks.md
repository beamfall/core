## 1. Artwork

- [x] 1.1 File-backed lock marker beside the slot file for Media and Entity artwork (source, path, size, modification time)
- [x] 1.2 `ImportLocalArtwork` / `ImportEntityLocalArtwork`: regular files only, size cap, format sniff, no-op when unchanged, nothing for Sealed or Encrypted Libraries
- [x] 1.3 `ImportArtwork` and `ImportEntityPoster` refuse a locked slot with `SlotLockedError`; `ImportManualArtwork` bypasses it
- [x] 1.4 `GeneratePoster` and the embedded-cover import leave a locked poster alone
- [x] 1.5 Releasing a local lock removes the marker and keeps the art

## 2. Scan

- [x] 2.1 One local artwork vocabulary table (file, folder and container scopes)
- [x] 2.2 Discover sidecar images per Media at walk completion, on both the fast and full paths
- [x] 2.3 Container art through the optional resolver seam, bounded to the library root
- [x] 2.4 Tests: folder poster and fanart, file-level thumb precedence, scrape refused, edited sidecar re-imports, vanished sidecar releases the lock, Sealed Library imports nothing, container art lands on the Entity

## 3. Follow-ups

- [x] 3.1 Implement the container Entity resolver in identification — deferred to CORE-1488
- [x] 3.2 Wire the manual artwork picker to `ImportManualArtwork` — deferred to CORE-1489
- [x] 3.3 Exclude sidecar images from Media admission and the estimate walk — deferred to CORE-0043
