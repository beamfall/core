## ADDED Requirements

### Requirement: The image carries its FFmpeg corresponding source

Each Beamfall Core container image SHALL carry one corresponding-source tar for its architecture
(linux/amd64 or linux/arm64) under `/usr/share/beamfall/source/ffmpeg/`, with an adjacent
`SHA256SUMS` naming the archive and its SHA-256. Its contents SHALL fulfill the written offer
in `NOTICE`. The `ffmpeg-runtime` image build SHALL verify the archive against `SHA256SUMS`.

#### Scenario: Architecture-specific source travels with the image

- **WHEN** the linux/arm64 image is built for FFmpeg 7.1.4-3
- **THEN** it contains `beamfall-ffmpeg-7.1.4-3-linux-arm64-source.tar` and its `SHA256SUMS` in
  `/usr/share/beamfall/source/ffmpeg/`
- **AND** the image build fails if `sha256sum -c SHA256SUMS` fails

#### Scenario: Archive contains the build's corresponding source

- **WHEN** the image's source archive is unpacked
- **THEN** it contains the locked upstream tarballs (FFmpeg and its patch series,
  nv-codec-headers and AMD AMF headers), the exact Debian source packages for loaded libraries
  and bundled Intel VA-API, QSV and OpenCL runtimes, build scripts, lock and `PACKAGES.txt`
  mapping binary packages to source versions
- **AND** GNU C library and GCC runtime System Libraries are excluded from the source collection

### Requirement: The FFmpeg source archive is publicly downloadable

The server SHALL register `GET /source/ffmpeg` without requiring authentication. For an ordinary
GET with a usable checksum entry and named regular file, it SHALL return 200 with that file's
bytes, `Content-Type: application/x-tar` and `Content-Disposition: attachment; filename="<name>"`,
where `<name>` is the filename from the checksum entry.

#### Scenario: Download without credentials

- **WHEN** a client sends an unauthenticated GET to `/source/ffmpeg` with no range or conditional
  headers and a usable bundle exists
- **THEN** the response is 200 with the archive bytes and tar content type
- **AND** the attachment filename is the archive name from `SHA256SUMS`

#### Scenario: A compatibility-refused session can still download the source

- **WHEN** a client whose session has a refused compatibility decision sends GET or HEAD to
  `/source/ffmpeg` with its session cookies and a usable bundle exists
- **THEN** the response is 200, as for `GET /source`, because the route is exempt from
  compatibility enforcement
- **AND** protected routes remain refused for that session

### Requirement: Source delivery retains standard file response semantics

The GET route SHALL also accept HEAD through Go's method routing. Delivery SHALL use
`http.ServeContent` with the file's modification time, retaining its range and conditional-request
handling, `Accept-Ranges: bytes`, content length and applicable `Last-Modified` header.
The handler SHALL NOT set an ETag.

#### Scenario: Inspect the archive without downloading its body

- **WHEN** a client sends HEAD to `/source/ffmpeg` with a usable bundle and no conditional headers
- **THEN** the response is 200 with the archive headers and no response body

#### Scenario: Resume a source download

- **WHEN** a GET requests a satisfiable single byte range from a usable archive
- **THEN** `http.ServeContent` returns 206 with the selected bytes and the corresponding
  `Content-Range` and `Content-Length` headers

### Requirement: The checksum header reflects the bundled checksum entry

For a usable checksum entry and named regular file, the handler SHALL set `X-Checksum-SHA256`
to the recorded digest before calling `http.ServeContent`. It SHALL NOT recompute the archive's
checksum or inspect its tar structure on a request.

#### Scenario: Publish the recorded digest

- **WHEN** the first checksum line has a valid digest and names the available archive
- **THEN** an ordinary GET returns 200 with `X-Checksum-SHA256` equal to the recorded digest

#### Scenario: Recorded digest differs from the file bytes

- **WHEN** a syntactically valid first checksum line names an available regular file but its
  digest does not equal the SHA-256 of that file
- **THEN** an ordinary GET still returns 200 with the file bytes and the recorded digest header
- **AND** the handler does not reject the file for that mismatch or for invalid tar contents

### Requirement: The first checksum line selects the archive

The handler SHALL read at most 4096 bytes of `SHA256SUMS` and parse only the first scanned line,
after trimming surrounding whitespace. That line SHALL match
`^([0-9a-f]{64})  (beamfall-ffmpeg-[A-Za-z0-9._-]+\.tar)$`: a lowercase 64-digit hexadecimal
digest, exactly two spaces and an accepted archive filename. The handler SHALL ignore subsequent
checksum lines.

#### Scenario: Later checksum entries are ignored

- **WHEN** the first checksum line is valid and names an available regular file, and another
  checksum line follows it
- **THEN** an ordinary GET serves the first line's file and digest, ignoring the later entry

### Requirement: Unavailable or invalid source bundles return not found

The handler SHALL return 404 via `http.NotFound` for an unavailable source directory or
`SHA256SUMS`, an absent or invalid first checksum line, or an archive that cannot be opened
within the source root, statted or identified as a regular file. It SHALL do so before setting
the tar content type, attachment disposition or checksum header. It SHALL NOT search for a
different archive or fall back to later checksum lines.

#### Scenario: Bundle or checksum file is unavailable

- **WHEN** the source directory, `SHA256SUMS` or the archive named by a valid checksum line is
  missing or cannot be opened
- **THEN** GET `/source/ffmpeg` returns 404, including for a release without bundled FFmpeg source

#### Scenario: The checksum line is malformed

- **WHEN** `SHA256SUMS` is empty or its first line has `XYZ` as its digest, an uppercase digest,
  the wrong separator, `../beamfall-ffmpeg-x.tar` as its filename or the filename `ffmpeg.tar.gz`
- **THEN** GET `/source/ffmpeg` returns 404 even if a later line is valid

#### Scenario: The checksum filename does not match the available archive

- **WHEN** a valid checksum line names an absent file but a different source tar is present
- **THEN** GET `/source/ffmpeg` returns 404 without serving the other tar

#### Scenario: The named archive is not a regular file

- **WHEN** the checksum entry names a directory or the opened file cannot be statted
- **THEN** GET `/source/ffmpeg` returns 404

### Requirement: The shipped FFmpeg build excludes nonfree components

The Beamfall Core image SHALL ship its self-built FFmpeg under GPL-3.0-or-later, configured with
`--enable-gpl` and `--enable-version3`, without `--enable-nonfree`, libfdk-aac or other nonfree
components. AAC encoding SHALL use FFmpeg's native `aac` encoder. The `ffmpeg-runtime` stage
SHALL reject a build whose `ffmpeg -hide_banner -buildconf` output contains `--enable-nonfree`
or `libfdk`.

#### Scenario: Build the distributable runtime

- **WHEN** the pinned FFmpeg runtime image is built
- **THEN** it contains the self-built GPL-3.0-or-later FFmpeg with native AAC encoding and no
  libfdk-aac or nonfree components
- **AND** the build fails if the runtime configuration reports `--enable-nonfree` or `libfdk`
