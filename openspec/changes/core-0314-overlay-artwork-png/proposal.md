# Overlay artwork slots keep their transparency

Ticket: CORE-0314 (`queue:core:main`)

## Why

`logo`, `clearart` and `disc` are drawn over other artwork, so their transparent pixels are the
point. Every slot is normalised to JPEG on import and every bucket derivative is a JPEG, so the
alpha channel is lost and a logo arrives as a picture on a solid rectangle.

## What changes

- `logo`, `clearart` and `disc` are stored as `<slot>.png` and their bucket derivatives as
  `<slot>.<bucket>.png`, encoded as RGBA PNG. The image scaler picks PNG when the destination
  ends in `.png`; every other destination stays the same JPEG.
- The artwork routes answer `Content-Type: image/png` for a served PNG file and `image/jpeg`
  otherwise.
- An overlay stored as `<slot>.jpg` before this change keeps being served (as JPEG, with its JPEG
  buckets and its lock) until the slot is next imported. That import writes the PNG and removes
  the old JPEG, its buckets and its markers. There is no migration and no bulk rewrite.

## Non-goals

- `poster`, `backdrop`, `thumb`, `banner` and `season-poster` are unchanged: same files, same
  bytes, same content type.
- No wire contract change: slot names, URLs and the bucket vocabulary are the same.
- No re-fetch of overlays that were already flattened to JPEG; they regain alpha when the slot is
  imported again.

## Failure modes

- With no image scaler configured, imported bytes are stored unchanged, as they are today, so an
  overlay slot then holds whatever format the source was.
- A bucket that fails to scale still falls back to the full-size file, which for an overlay is
  the PNG.
