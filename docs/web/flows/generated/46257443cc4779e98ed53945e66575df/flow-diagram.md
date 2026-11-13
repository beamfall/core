# Playback engine: playback\-info, capability probe, HLS selection and recovery, progress reports — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9b7c9e7f9f309297afc68988b7ea478a -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:103; symbol: fetchPlaybackInfo; span SHA256: 6c197262a7c51f224968ec7a4a67526f5d067c062e8683af5b58076f97b79db2.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 46257443cc4779e98ed53945e66575df
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/lib/playback.ts at line 85 - order unresolved
    Note over S,U: Source contains call finiteNumber in internal/web/app/src/areas/viewer/lib/transport.ts at line 344 - order unresolved
    Note over S,U: Source contains call nextNetworkStep in internal/web/app/src/areas/viewer/player/hlsRecovery.ts at line 40 - order unresolved
    Note over S,U: Source contains call nextMediaStep in internal/web/app/src/areas/viewer/player/hlsRecovery.ts at line 41 - order unresolved
    Note over S,U: Source contains call sendProgress in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 106 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 115 - order unresolved
    Note over S,U: Source contains call sendProgress in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 133 - order unresolved
    Note over S,U: Source contains call stopProgressQueue in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 136 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 142 - order unresolved
    Note over S,U: Source contains call stopProgressQueue in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 160 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 168 - order unresolved
    Note over S,U: Source contains call sendProgress in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 170 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 175 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 175 - order unresolved
    Note over S,U: Source contains call emptyState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 205 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 214 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 216 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 224 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 225 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 235 - order unresolved
    Note over S,U: Source contains call end in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 235 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 257 - order unresolved
    Note over S,U: Source contains call canPlayType in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 265 - order unresolved
    Note over S,U: Source contains call isSupported in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 266 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 288 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 289 - order unresolved
    Note over S,U: Source contains call delete in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 290 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 303 - order unresolved
    Note over S,U: Source contains call hlsLevelIndex in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 306 - order unresolved
    Note over S,U: Source contains call selectHlsLevel in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 308 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 318 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 320 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 320 - order unresolved
    Note over S,U: Source contains call fetch in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 322 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 331 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 332 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 368 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 369 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 373 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 377 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 384 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 29 - order unresolved
    Note over S,U: Source contains call setFirstFrame in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 37 - order unresolved
    Note over S,U: Source contains call setFirstFrame in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 39 - order unresolved
    Note over S,U: Source contains call settle in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 40 - order unresolved
    Note over S,U: Source contains call settle in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 52 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 45 - order unresolved
    Note over S,U: Source contains call setBuffering in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 47 - order unresolved
    Note over S,U: Source contains call clearTimer in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 34 - order unresolved
    Note over S,U: Source contains call setBuffering in internal/web/app/src/areas/viewer/player/useMediaLoading.ts at line 35 - order unresolved
    Note over S,U: Source contains call applyTrackPreferences in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 727 - order unresolved
    Note over S,U: Source contains call bumpTracks in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 728 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 722 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 391 - order unresolved
    Note over S,U: Source contains call destroy in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 393 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 614 - order unresolved
    Note over S,U: Source contains call destroyHls in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 617 - order unresolved
    Note over S,U: Source contains call removeEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 618 - order unresolved
    Note over S,U: Source contains call removeEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 619 - order unresolved
    Note over S,U: Source contains call removeEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 621 - order unresolved
    Note over S,U: Source contains call removeEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 623 - order unresolved
    Note over S,U: Source contains call removeEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 625 - order unresolved
    Note over S,U: Source contains call removeAttribute in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 632 - order unresolved
    Note over S,U: Source contains call load in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 633 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 515 - order unresolved
    Note over S,U: Source contains call terminalState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 515 - order unresolved
    Note over S,U: Source contains call resetIdentityEpoch in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 518 - order unresolved
    Note over S,U: Source contains call destroyHls in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 741 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 742 - order unresolved
    Note over S,U: Source contains call stopProgressReporting in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 1111 - order unresolved
    Note over S,U: Source contains call setWaitingForNetwork in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 494 - order unresolved
    Note over S,U: Source contains call setWaitingForNetwork in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 498 - order unresolved
    Note over S,U: Source contains call reportProgress in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 1109 - order unresolved
    Note over S,U: Source contains call setRecovering in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 586 - order unresolved
    Note over S,U: Source contains call resetRecoveryBudget in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 588 - order unresolved
    Note over S,U: Source contains call reportProgress in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 1110 - order unresolved
    Note over S,U: Source contains call showSourceEndsEarly in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 859 - order unresolved
    Note over S,U: Source contains call startLoad in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 862 - order unresolved
    Note over S,U: Source contains call atSourceEnd in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 853 - order unresolved
    Note over S,U: Source contains call showSourceEndsEarly in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 853 - order unresolved
    Note over S,U: Source contains call reloadNative in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 915 - order unresolved
    Note over S,U: Source contains call addEventListener in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 759 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 766 - order unresolved
    Note over S,U: Source contains call loadSource in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 767 - order unresolved
    Note over S,U: Source contains call detachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 971 - order unresolved
    Note over S,U: Source contains call attachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 973 - order unresolved
    Note over S,U: Source contains call detachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 647 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 648 - order unresolved
    Note over S,U: Source contains call playheadPosition in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 654 - order unresolved
    Note over S,U: Source contains call setRecovering in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 655 - order unresolved
    Note over S,U: Source contains call fetchPlaybackInfo in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 658 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 659 - order unresolved
    Note over S,U: Source contains call streamOfKind in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 661 - order unresolved
    Note over S,U: Source contains call detachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 664 - order unresolved
    Note over S,U: Source contains call showUnavailable in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 665 - order unresolved
    Note over S,U: Source contains call reload in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 670 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 674 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 681 - order unresolved
    Note over S,U: Source contains call detachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 683 - order unresolved
    Note over S,U: Source contains call showPlaybackInfoError in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 684 - order unresolved
    Note over S,U: Source contains call finally in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 686 - order unresolved
    Note over S,U: Source contains call resetRecoveryBudget in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 747 - order unresolved
    Note over S,U: Source contains call setRecovering in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 748 - order unresolved
    Note over S,U: Source contains call startLoad in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 749 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 386 - order unresolved
    Note over S,U: Source contains call setRetryKey in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 388 - order unresolved
    Note over S,U: Source contains call isRevealTerminalStatus in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 527 - order unresolved
    Note over S,U: Source contains call enterRevealTerminal in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 528 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 530 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 532 - order unresolved
    Note over S,U: Source contains call serverErrorReason in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 532 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 534 - order unresolved
    Note over S,U: Source contains call destroyHls in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 834 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 835 - order unresolved
    Note over S,U: Source contains call unavailableReason in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 476 - order unresolved
    Note over S,U: Source contains call showMessage in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 477 - order unresolved
    Note over S,U: Source contains call fetchPlaybackMarkers in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 542 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 542 - order unresolved
    Note over S,U: Source contains call fetchPlaybackInfo in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 545 - order unresolved
    Note over S,U: Source contains call waitForNetwork in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 549 - order unresolved
    Note over S,U: Source contains call start in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 549 - order unresolved
    Note over S,U: Source contains call showPlaybackInfoError in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 552 - order unresolved
    Note over S,U: Source contains call choosePlaybackStream in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 557 - order unresolved
    Note over S,U: Source contains call playbackDecision in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 558 - order unresolved
    Note over S,U: Source contains call showUnavailable in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 564 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 844 - order unresolved
    Note over S,U: Source contains call stopLoad in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 846 - order unresolved
    Note over S,U: Source contains call setRecovering in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 847 - order unresolved
    Note over S,U: Source contains call atSourceEnd in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 848 - order unresolved
    Note over S,U: Source contains call showSourceEndsEarly in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 849 - order unresolved
    Note over S,U: Source contains call stopProgressQueue in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 445 - order unresolved
    Note over S,U: Source contains call hlsStreamOf in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 982 - order unresolved
    Note over S,U: Source contains call engineFor in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 984 - order unresolved
    Note over S,U: Source contains call renditionIndex in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 988 - order unresolved
    Note over S,U: Source contains call playheadPosition in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 992 - order unresolved
    Note over S,U: Source contains call detachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 997 - order unresolved
    Note over S,U: Source contains call attachEngine in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 999 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 1000 - order unresolved
    Note over S,U: Source contains call setState in internal/web/app/src/areas/viewer/player/usePlayer.ts at line 490 - order unresolved
```
