# Conservative rescan retention

## Why
CORE-2364: a plugin declaration is not evidence that existing items may be retired.

## What Changes
Preserve catalogued items when the declaration no longer covers an existing media class. Do not infer permission to trash from scan exclusion.

## Impact
Core scan only; no plugin repin.
