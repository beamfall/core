- [x] Build the pinned FFmpeg and patch series with GPL/version3 enabled, without nonfree
      components or libfdk-aac, using native AAC encoding
- [x] Collect the corresponding upstream and exact Debian sources, build inputs and package
      manifest into one source tar per image architecture with an adjacent `SHA256SUMS`
- [x] Copy the source bundle into `ffmpeg-runtime`, reject nonfree/libfdk build configurations
      and verify the archive checksum during the image build
- [x] Document the archive contents, supported architectures and public endpoint in `NOTICE`
- [x] Register unauthenticated `GET /source/ffmpeg` and serve the named regular file with its
      attachment filename, tar content type and checksum header through `http.ServeContent`
- [x] Return 404 for unavailable bundles or invalid checksum entries; cover public delivery,
      headers, missing files, malformed digests and rejected filenames in server tests
