## 1. Stall watchdog (CORE-1344)

- [ ] 1.1 `ffmpeg`: live HLS encoders write `-progress pipe:1`; `HLSProcess` parses `out_time_us` and records when it last advanced; `Kill` ends the process without marking it stopped
- [ ] 1.2 `Sessions`: count the requests waiting on each run; a watchdog kills a run whose output has not advanced for the stall interval while a request waits; the exit is classified as a stall and restarts like a crash
- [ ] 1.3 Tests: progress parsing; a fake encoder that stops writing is restarted, then the request fails with the encoder-failing error

## 2. Resume start (CORE-1345)

- [ ] 2.1 `Service.decide` records the resume position for the HLS session key
- [ ] 2.2 `Sessions`: the first encoder starts at the resume segment; `init.mp4` reuses a running encoder for its rendition
- [ ] 2.3 Tests: a resume at a non-zero offset spawns the encoder once; `init.mp4` after a seek does not restart the encoder
