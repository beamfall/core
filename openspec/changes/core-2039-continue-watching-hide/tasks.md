- [x] Store: events migration 0025 `playback_progress.continue_hidden`, `hide-continue` engagement
      event, `SetContinueWatchingHidden`, continue-watching Source skips hidden rows, a
      position-moving report clears the flag
- [x] Graph: `setContinueWatchingHidden(mediaID, hidden)` mutation returning `ItemStateInfo`
- [x] Store and graph tests (hide keeps progress, same-position report keeps it hidden, play again
      returns it, undo restores order, rebuild reproduces it, hidden Media refused)
- [x] Regenerate wire goldens (`make regen-wire-goldens`)
- [ ] Lander: publish the next minor client-core-wire version
- [ ] CORE-2017: web card menu (this branch) and Android long-press use the mutation
