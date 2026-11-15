# Tasks (CORE-1657)

- [x] 1. Grant field `composedPicture`; live composed decision with reason `composed-picture`
- [x] 2. HLS session with a single composed rung; shared FFmpeg composition recipe with a start offset
- [x] 3. Queue the composed rendition at raised priority after a live play
- [x] 4. Tests: live start for a graphics and a timed-text Media, Ready rendition preferred,
      profile fallback, precompute queued
- [x] 5. Wire goldens regenerated for the additive `streamToken.composedPicture`
- [x] 6. Live composed shape under client and quirk height, bitrate and codec limits; audio when
      none fits
- [x] 7. Offer live composition only when the FFmpeg build has the recipe's filters and encoders
      (cached per binary); audio otherwise
- [x] 8. Read the capability requirements off the emitted argv, audio encoder and pitch and tempo
      filters included; decline an adjustment the build cannot run; budget each live shape as its
      video maxrate plus the ladder's 256 kbps audio and mux allowance
