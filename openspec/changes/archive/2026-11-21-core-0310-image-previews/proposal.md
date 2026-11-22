# Image previews: RAW embedded previews, EXIF orientation, ICC to sRGB, more RAW extensions

Tickets: CORE-0310, CORE-0652 (`queue:core:main`).

## Why

Every image-class poster and derivative goes through `ffmpeg.ScaleImage`, a plain
`-i src -vf scale` run. Three things go wrong for real image libraries:

- RAW files almost never thumbnail. FFmpeg does not demosaic camera RAW, and at best decodes a
  tiny thumbnail IFD, so most RAW Media show `preview-unavailable`.
- EXIF orientation is left to the FFmpeg build. FFmpeg 8 and later rotate a JPEG by its EXIF tag;
  the pinned Linux build (7.1) does not. The same file is upright on one host and sideways on
  another.
- ICC profiles are ignored. FFmpeg copies the source profile into the derivative, so a Display P3
  file's poster is only right in colour-managed viewers.

The scanner also admits only `cr2`, `nef`, `arw` and `dng`, so `cr3`, `raf`, `orf`, `rw2`, `pef`,
`srw`, `3fr` and `nrw` files never become Media, even though the image plugin's EXIF reader handles
RAF and CR3 (CORE-0652's dependency on this change).

## What changes

- **Extensions.** `imageExtensions` adds `cr3`, `raf`, `orf`, `rw2`, `pef`, `srw`, `3fr` and `nrw`.
  Classification stays by extension.
- **RAW embedded preview.** Before decoding, `ScaleImage` sniffs the source by content (the
  artwork importer passes files with no image extension):
  - TIFF-based RAW (`cr2`, `nef`, `nrw`, `arw`, `dng`, `pef`, `srw`, `3fr`, `orf`, `rw2`): the
    JPEG previews named by the IFD chain, SubIFDs and the RW2 `JpgFromRaw` tag;
  - RAF: the JPEG named by the RAF header;
  - CR3: the JPEG in the `PRVW` box.
  If none of those is at least 1024 pixels on its long side, a bounded byte scan looks for a
  larger embedded JPEG (maker-note previews in ORF, PEF and SRW). The largest valid baseline or
  progressive JPEG wins. It is written to a temp file beside the output and decoded instead of
  the RAW. A RAW with no embedded preview is decoded directly, as before.
- **Orientation.** For JPEG, TIFF and RAW, PNG and WebP sources, `ScaleImage` reads the EXIF
  orientation itself (for RAW, the container's IFD0, else the preview's own EXIF), turns off
  FFmpeg's autorotation and applies the matching `transpose`/`hflip`/`vflip` filter. The
  derivative carries no orientation tag. HEIF and AVIF keep FFmpeg's handling, since their
  rotation lives in the `irot` and `imir` item properties, not EXIF.
- **ICC to sRGB.** `ScaleImage` reads the source profile (JPEG `APP2`, PNG `iCCP`, WebP `ICCP`,
  TIFF tag 34675, HEIF `colr`). When a matrix/TRC profile's colorants are sRGB/BT.709, Display P3
  or BT.2020 and its curve is sRGB, BT.709, gamma 2.2 or gamma 2.8, a JPEG derivative is converted
  with FFmpeg's `colorspace` filter and carries an embedded sRGB profile instead of the source's.
  HEIF `nclx` colour boxes map the same way.

## Non-goals

- A libraw-class demosaic for RAW files with no embedded preview (would add a native dependency
  and need an ADR-0043 amendment; design photos.md §4.3).
- Profiles the native `colorspace` filter cannot express (Adobe RGB, ProPhoto, LUT-based
  profiles). Their derivatives keep the source profile, as today, so colour-managed clients still
  show them correctly.
- ICC conversion for PNG derivatives (alpha-preserving logos); they keep the source profile.
- The HEIF/AVIF decoder health line from CORE-0310's ticket text. It needs library-health wiring
  in `internal/app`, `internal/store` and `internal/graph`, outside this change; follow-up ticket.

## Failure modes

- An unreadable source fails as before and sets `preview-unavailable`.
- A malformed RAW container, EXIF block or ICC profile is ignored: the file is decoded directly
  with no explicit orientation or colour conversion, never worse than before this change.
- A preview larger than 64 MiB, or a scan past the first 64 MiB, is not used.
