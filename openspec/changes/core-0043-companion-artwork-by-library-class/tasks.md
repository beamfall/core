## 1. Scan

- [x] 1.1 Add `<stem>-banner` to the local artwork vocabulary as a recognised file-level name with no slot
- [x] 1.2 Add the optional `PluginMediaClassResolver` seam and implement it on the composition root's Identifier adapter
- [x] 1.3 Skip recognised sidecars in the walk, file count and missing-ratio guard only when the Library's plugin does not declare the `image` class
- [x] 1.4 Tests: companion fixture in a video Library imports one video and no image (all-items feed included); the same fixture in an image Library imports every image

## 2. Web player copy

- [x] 2.1 Reword `playbackUnsupportedCodec` in every locale so it names no Profile setting
- [x] 2.2 Tests: the decode failure with no HLS stream shows that message; the string registry baseline is updated
