# Scan preview and estimate follow the Library's sidecar rule

Ticket: CORE-2180 (`queue:core:main`).

## Why

CORE-0043 keeps recognised sidecar-named images as items in a Library whose bound plugin declares
the `image` Media class. The rescan preview and the first-scan estimate have no plugin context, so
they still skip every recognised sidecar. In an image Library they leave out `IMG_1-thumb.jpg`
beside `IMG_1.mov`, or `poster.jpg` beside one video, although the scan imports them.

## What Changes

- `PreviewRoots` and `EstimateRoots` take whether images are content for the target plugin and skip
  recognised sidecars only when they are not, matching the scan walk, its file count and its
  missing-ratio guard.
- The `scanPreview` field resolves this from the Library's bound plugin. The `scanEstimate` field
  resolves it from the Library's bound plugin when given `libraryID`, and from an optional
  `pluginID` variable when given a raw `root` before the Library exists. Without a plugin, or with
  an unknown one, both keep skipping recognised sidecars.

## Impact

- `internal/scan` (preview, estimate) and `internal/graph` (scan preview, wizard estimate). One
  optional, additive `pluginID` variable on `scanEstimate`. No schema, migration or web change.
