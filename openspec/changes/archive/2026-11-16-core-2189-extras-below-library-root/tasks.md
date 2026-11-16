## 1. Scan

- [x] 1.1 Bound the Extras folder check in `isExtraPath` and `extraContainerDir` by the Library root
- [x] 1.2 Pass the root from the scan walk, version grouping, Extra linking and estimate
- [x] 1.3 Tests: unit cases for ancestors above and folders below the root; a scan of a Library under `Trailers/` imports main Media and still links an inner Extras clip; the estimate counts a Library under `Extras/`
- [x] 1.4 Bound the Extras conventions by the configured Library root holding the scanned folder, so a subfolder scan classifies files as a Library-root scan would
- [x] 1.5 Let a scan import set the stored Extra flag both ways so a rescan repairs a stale flag; uploads and managed imports keep the stored flag
- [x] 1.6 Tests: a subfolder scan of `<root>/<title>/Extras` imports an Extra; a rescan clears an Extra flag set by a folder above the root; a path-decided store import clears the flag while an undecided one keeps it
