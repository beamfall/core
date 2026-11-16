## 1. Server

- [x] 1.1 Set reason `transcode-disallowed` on a `direct-play` decision whose HLS stream was withheld because the Profile disallows transcoding
- [x] 1.2 Tests: transcodeAllowed false gives direct play with that reason and no HLS stream; transcodeAllowed true gives no reason

## 2. Web player

- [x] 2.1 Show `playbackTranscodeDisallowed` on a direct-play decode failure with no HLS stream when the reason is `transcode-disallowed`; otherwise the unsupported-codec message
- [x] 2.2 Label the reason in the playback details panel in every shipped locale
- [x] 2.3 Tests: usePlayer covers both branches; the details model labels the reason
