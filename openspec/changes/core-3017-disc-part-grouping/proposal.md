# Disc and CD subfolders join one audiobook (CORE-3017)

## Why
plugin-audiobook v0.1.17 keys every `Disc N`, `Disk N` and `CD N` subfolder to its book folder
and numbers its tracks disc×10000 + track, but Core still split or failed such books:

- Scan flushed a pending part group at the first file that was not one of its parts, so a loose
  file in the book folder that sorts between two disc folders (`Novel/Credits.mp3` between
  `Novel/CD1/` and `Novel/Disc 2/`) split the book into two Media.
- Two folders naming the same disc (`CD1/` and `Disc 01/`) gave their tracks the same part
  number, and the store refused the group, which failed the whole scan.

## What changes
- A file inside the pending group's folder (its `PartGroup`, when that is an absolute path
  above the file) no longer flushes the group, as a still image already did not (CORE-1837).
  Such a file is still its own Media; its completion is held below the group until the group
  flushes. A file outside the folder, or a part of another group, flushes as before.
- A group orders its Parts folder by folder: each folder takes the place of its lowest part
  number, a tie going to the folder walked first, and Parts within a folder go by part number,
  a tie going to the file walked first. Storage gets that rank as the part ordinal, so equal
  part numbers no longer fail the scan. A group in one folder orders exactly as before.

## Impact
Scan part grouping for every plugin that sets `PartGroup` (audiobooks, Film & TV numbered
parts). No wire, schema or plugin change.
