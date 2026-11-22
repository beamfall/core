## ADDED Requirements

### Requirement: RAW extensions are image Media

The scanner SHALL classify files with the extensions `cr2`, `cr3`, `nef`, `nrw`, `arw`, `dng`,
`raf`, `orf`, `rw2`, `pef`, `srw` and `3fr` (case-insensitive) as image-class Media, alongside the
existing still-image extensions.

#### Scenario: A RAF file is admitted

- **WHEN** a library scan finds `IMG_0001.RAF`
- **THEN** the file classifies as image-class Media and is never probed for streams

### Requirement: RAW derivatives come from the embedded preview

When the source of an image derivative is a camera RAW container (TIFF-based, RAF or CR3), the
derivative SHALL be produced from the largest valid embedded JPEG preview rather than from the
sensor data. When the container names no preview at least 1024 pixels on its long side, a scan of
at most the first 64 MiB SHALL look for a larger embedded JPEG. A RAW container with no usable
preview SHALL be decoded directly, as for any other image.

#### Scenario: Each RAW extension thumbnails from its preview

- **WHEN** a `cr3`, `raf`, `orf`, `rw2`, `pef`, `srw`, `3fr` or `nrw` file carries an embedded
  JPEG preview
- **THEN** a 320-pixel derivative is produced from that preview and shows its pixels

#### Scenario: Lossless sensor data is not a preview

- **WHEN** a CR2's IFDs name both a lossless-JPEG sensor strip and a baseline JPEG preview
- **THEN** the derivative comes from the baseline preview

### Requirement: Derivatives are upright

For JPEG, TIFF, RAW, PNG and WebP sources the derivative pipeline SHALL read the EXIF orientation
itself, disable the decoder's own autorotation, and apply the transform for orientations 2 to 8
before scaling, so the result does not depend on the FFmpeg version. For a RAW source the
container's orientation SHALL win over the preview's. The derivative SHALL carry no EXIF
orientation tag. HEIF and AVIF sources SHALL keep the decoder's container transforms.

#### Scenario: Orientation 6 JPEG

- **WHEN** a 40×20 JPEG whose left half is red and right half blue carries EXIF orientation 6
- **THEN** the derivative is 20×40 with red on top and blue at the bottom, and has no orientation
  tag

### Requirement: Wide-gamut derivatives are sRGB

When a JPEG derivative's source carries an RGB matrix/TRC ICC profile, or a HEIF `nclx` colour box,
whose primaries are BT.709/sRGB, Display P3 or BT.2020 and whose transfer curve is sRGB, BT.709,
gamma 2.2 or gamma 2.8, and the pair is not already sRGB, the pixels SHALL be converted to sRGB and
the derivative SHALL embed an sRGB ICC profile in place of the source profile. Any other profile
SHALL be left as the source profile, and a malformed profile SHALL be ignored.

#### Scenario: Display P3 JPEG

- **WHEN** a JPEG tagged with a Display P3 profile has the pixel value (180, 120, 60)
- **THEN** the derivative's pixel is within a small tolerance of (190, 117, 45) and its embedded
  profile is sRGB

#### Scenario: Display P3 HEIC

- **WHEN** a HEIC whose `colr` box carries a Display P3 profile is scaled to a JPEG derivative
- **THEN** the derivative embeds an sRGB profile and its pixels are converted

#### Scenario: Unsupported profile

- **WHEN** a JPEG carries an Adobe RGB (1998) profile
- **THEN** the derivative is produced without colour conversion and keeps the source profile
