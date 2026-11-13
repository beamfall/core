# Playback engine: playback\-info, capability probe, HLS selection and recovery, progress reports — technical-deep-dive

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:0dba614742002a4ca6c0bbcbdd7b4dda -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:103; symbol: fetchPlaybackInfo; span SHA256: 6c197262a7c51f224968ec7a4a67526f5d067c062e8683af5b58076f97b79db2.

<!-- flowdocs:paragraph:5710081c6de291f7e1dc01da385f04df -->
Declared member: source declares fetchPlaybackInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:103; symbol: fetchPlaybackInfo; span SHA256: 6c197262a7c51f224968ec7a4a67526f5d067c062e8683af5b58076f97b79db2.

<!-- flowdocs:paragraph:6df5973afa8f11138911ddd4c3b171e3 -->
Declared member: source declares hlsStreamOf; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:84; symbol: hlsStreamOf; span SHA256: 32425bbec1c0aa1d5ce0e6ca3077d93f9099823717c0a98e535315f805c847ab.

<!-- flowdocs:paragraph:8d755a6e5b7ff7444c8c253d3b2f0f6c -->
Declared member: source declares isRevealTerminalStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:89; symbol: isRevealTerminalStatus; span SHA256: c3996a71b4b0de77ebe373329f5cd6f30ca49ce5cf2e33f365dba6ac4a02b672.

<!-- flowdocs:paragraph:0d3c0a2d86c9d1d59cb078adc91f18df -->
Declared member: source declares playbackDecision; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:348; symbol: playbackDecision; span SHA256: 33a5a732a700cfdcedf0b8c7c56a626c21e564f75372d20fc0d7de516b29f993.

<!-- flowdocs:paragraph:85a5259cc453235768891f5c6c606cfd -->
Declared member: source declares resumePositionFromInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:342; symbol: resumePositionFromInfo; span SHA256: 0f4ee24b755d5bea0f94877f581655c0112522feb31910ef755e5222cfc4cbd5.

<!-- flowdocs:paragraph:179a2398f0bfeffb5f4c03d9b19b7b6e -->
Declared member: source declares freshRecoveryBudget; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:29; symbol: freshRecoveryBudget; span SHA256: 5e27b1ff40c53cc317f92399214a67c876cb724a343feed8bad2c76e09b1426f.

<!-- flowdocs:paragraph:6254ff62936f2ecfdabe91535d40f549 -->
Declared member: source declares nextMediaStep; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:52; symbol: nextMediaStep; span SHA256: 5867abad2524cec85214622de541e0281633d3c813ba6b76ce03b6718e932e3f.

<!-- flowdocs:paragraph:adceb157676d4e4151527666e9b1370c -->
Declared member: source declares nextNetworkStep; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:45; symbol: nextNetworkStep; span SHA256: d511ee5cbd9a32beb58c0abf9b680cdbbdb50b389c4e14e6fbbbb3dbf065c2d1.

<!-- flowdocs:paragraph:a7fb377274bd6261dc2628af06a9932a -->
Declared member: source declares nextRecoveryStep; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:39; symbol: nextRecoveryStep; span SHA256: 07292ec4801c00e484ba7be08d602ba2ff75485659cf57da030cb4853e95ffa1.

<!-- flowdocs:paragraph:cdeb0a2368b70d484f83ddb667a79ae0 -->
Declared member: source declares resetRecoveryBudget; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:33; symbol: resetRecoveryBudget; span SHA256: 51439d8947e28bd32ec60442a472fe63d5ac6e9cc0d090452886cd21d13054f9.

<!-- flowdocs:paragraph:0e9607c5a07e2d7cf35acba0a62a2e21 -->
Declared member: source declares useMediaLoading; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:13; symbol: useMediaLoading; span SHA256: 64540a9fd129f836662dd21193052f58c21ac044d975d6e1ab5fb69a2129f7c2.

<!-- flowdocs:paragraph:c276d3993dce8b2d27608e44dd708844 -->
Declared member: source declares createProgressQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:91; symbol: createProgressQueue; span SHA256: 4ae1bcf29664334aaae4699172b2b1cc2afdbae510b6ea0dd3b72b3c004f2dde.

<!-- flowdocs:paragraph:f17e8ca3ec4f1682365b09a6fcee6343 -->
Declared member: source declares enqueueProgress; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:102; symbol: enqueueProgress; span SHA256: a33e8e53cb3036c980d0a3471bbeffaca4b2b238b74aef143cc2ddc06f4850c4.

<!-- flowdocs:paragraph:561efedc6d94f3309a48124e070d9b51 -->
Declared member: source declares sendProgress; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:109; symbol: sendProgress; span SHA256: 25524918a6fbf65f0826f696042bbc34344769ea775b9e61e115c989a60c3b5e.

<!-- flowdocs:paragraph:4ee1940f7a84fd793281bc8849b6093b -->
Source contains a lexical call to delete; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:290; symbol: ; span SHA256: bf071c4b6fb6447d88a1d63abfa65841de12e92828b5e9e63a2ace24a8827a34.

<!-- flowdocs:paragraph:92c6489bcdccb45c1294720d1fc70d71 -->
Source contains a lexical call to fetch; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:322; symbol: ; span SHA256: f356ae58a3e67abc55f35a4b9b67d4ca4f70d6cb134d1595b126b810c3504a18.

<!-- flowdocs:paragraph:d8e50298ff81bf0c4ccec542dbd16134 -->
Declared member: source declares clearTimer; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:27; symbol: clearTimer; span SHA256: c575c569ef6b4569e29efb0f73990f63175c2c4b6cebe07a42aaa1524a2ae02d.

<!-- flowdocs:paragraph:612f98337e97f1038f1ad25223bea05c -->
Declared member: source declares onFirstFrame; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:37; symbol: onFirstFrame; span SHA256: 7fbc81afb4d6e7e870d74b3063c1852fb7cfca15222eb07799abe6b9b89415b2.

<!-- flowdocs:paragraph:cad9a2a25e1fb3200b5acb6836a8e294 -->
Declared member: source declares onPlaying; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:38; symbol: onPlaying; span SHA256: 96a6659604e0175607233aad70e45acb176ab6aa22c6f19be49cd053c01d20af.

<!-- flowdocs:paragraph:978abb6c2f6f6f0aa4725048c7860b2a -->
Declared member: source declares onTimeUpdate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:51; symbol: onTimeUpdate; span SHA256: 63bd392f0bd3690a3e7a69326f2e638638fd8d27b4b50cc2a220f1bfb26954b1.

<!-- flowdocs:paragraph:f51fa3c57048e87514dd452d8f0942b8 -->
Declared member: source declares onWaiting; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:42; symbol: onWaiting; span SHA256: 0a641814e0ab1998356eeb3ba64bb19ea2226e3b839d4565dbbddbd7b4c84fca.

<!-- flowdocs:paragraph:be145e7c6b70a6d50c1269ccfc68eea2 -->
Declared member: source declares settle; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:33; symbol: settle; span SHA256: 02b81edc37cdffe79eb50974834c4e00eb9bda3fa839375fa82740b9e0f2deea.

<!-- flowdocs:paragraph:7c8735532f8bb328d511b59bafc3b387 -->
Declared member: source declares applyPreferencesThenBump; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:726; symbol: applyPreferencesThenBump; span SHA256: d187da2f6325236ab079cb20179051416ed90329667309954bc22805482f74ea.

<!-- flowdocs:paragraph:ff2ea91816ea5a95356de0f165c7e622 -->
Declared member: source declares atSourceEnd; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:839; symbol: atSourceEnd; span SHA256: 8674b536787ea52e44142f106cba63b968db249b78a3a0965bc6b3b32bcd59c6.

<!-- flowdocs:paragraph:ba6da69360e47397f53c45e249c621bb -->
Declared member: source declares bumpTracks; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:721; symbol: bumpTracks; span SHA256: cd688cca0de40abd29755d80dd1057bba031fb66db192b932e0e1753b13e532b.

<!-- flowdocs:paragraph:b324e2edd81ec4052388dd166d59c186 -->
Declared member: source declares destroyHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:391; symbol: destroyHls; span SHA256: 697b62bf069b6af4195e118771065f85df7f0187e7fa3de1d617230193051dd2.

<!-- flowdocs:paragraph:a3d2ef11da8cbe79f39476aa69156f64 -->
Source contains a lexical call to destroy; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:393; symbol: ; span SHA256: 0e059a072dd156d9519052bd52f618ff8ec22cf4e35ab305a667bdd8818d3fed.

<!-- flowdocs:paragraph:f8d909e726c371e0e5bef7bb5550fcd2 -->
Declared member: source declares detachEngine; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:612; symbol: detachEngine; span SHA256: 8580a995d1cbd1fdc100934fb0572f573774ce4b18813a4f372826821e9e59f5.

<!-- flowdocs:paragraph:1b6b34afae94fdc185661a65795ac3c5 -->
Declared member: source declares enterRevealTerminal; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:514; symbol: enterRevealTerminal; span SHA256: 09ba3225d3833f99de86b5cdaf130be0396b4478ec5d491917e438e388490ef4.

<!-- flowdocs:paragraph:0bf543c2c8f98b97dbac96ec64c74516 -->
Declared member: source declares failPlayback; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:740; symbol: failPlayback; span SHA256: 3ff24e1b45055cbb676101a4224a52d8c7f5889713995890e9b0ed858a57e119.

<!-- flowdocs:paragraph:bedb0445918c6e3c3eba8e268db29053 -->
Declared member: source declares onEnded; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1111; symbol: onEnded; span SHA256: 8100981419b13a4f1a079b20be7fc3d2642ead5fef56406a59da03c2ee440f83.

<!-- flowdocs:paragraph:0501fadd349aee1e8b108ae0be4bc3e4 -->
Declared member: source declares onOffline; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:492; symbol: onOffline; span SHA256: faadbc49978d76466c625634c744b228611908b55f9955048131be5f73ed5a3c.

<!-- flowdocs:paragraph:ffaebe6c0fbe2cb32aa7bc190bc813a7 -->
Declared member: source declares onOnline; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:496; symbol: onOnline; span SHA256: ec4d19749ac4b326f07f5d247ca4300abe9a5f8162f8c5abfda758ed3d75a40d.

<!-- flowdocs:paragraph:ef715f71372fe052e0d2377d31eb5e91 -->
Declared member: source declares onPause; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1109; symbol: onPause; span SHA256: 5399166540ecb713435d3520d2c5f15ffc3f5cc5985fe882a735bb84ffdff3a6.

<!-- flowdocs:paragraph:923bc59cf0655ea5449ddab52e24ca01 -->
Declared member: source declares onPlaying; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:585; symbol: onPlaying; span SHA256: e47d6a89a8d15ffb4d9999fa94141f70c4c7cd6ceeca6f4822ddf1e59eaaafce.

<!-- flowdocs:paragraph:626fdad2f97f12ff0454f785c80a88be -->
Declared member: source declares onSeeked; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1110; symbol: onSeeked; span SHA256: 9dc84a9492be6fefc4ee449754d18325ae26d5e4d55936931e3e7d7c0043a143.

<!-- flowdocs:paragraph:55a4c420978728a2169cbd2fd2aa52d7 -->
Declared member: source declares onSourceEndSeeking; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:855; symbol: onSourceEndSeeking; span SHA256: 0a84640f045942e37ccd0a61d047ff2d1c213ef30187a6462c959a5a46d1b512.

<!-- flowdocs:paragraph:2d2a7bd4218a95de63188be1bc1d10aa -->
Declared member: source declares onSourceEndWaiting; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:852; symbol: onSourceEndWaiting; span SHA256: 0220c83f55174c8dde00fa2f981fbc033abb7ce7d382d8a0327d13400579d585.

<!-- flowdocs:paragraph:70ce98c5f02c169dc2e8c3ee75739311 -->
Declared member: source declares onTimeUpdate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:591; symbol: onTimeUpdate; span SHA256: 467678dd77b7af9c27da138ef504e1607ee259aebba6672545cb11651530d0f4.

<!-- flowdocs:paragraph:2322261a69a760e1f3dee42b3f2da510 -->
Declared member: source declares playheadPosition; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:574; symbol: playheadPosition; span SHA256: b0323211cdafb42403f03982fd4e04cc697f206e44fcc4ce66f06cd55a6ba838.

<!-- flowdocs:paragraph:7b579796ddc6f67b9a5cd3f5d9da88d2 -->
Declared member: source declares reloadAtPlayhead; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:914; symbol: reloadAtPlayhead; span SHA256: a179d44d6b19ad1c61610e6ce7d50c78cacd43486b4cff57e936a7f40247f5f1.

<!-- flowdocs:paragraph:566190f2dc9bcbd411c746db9d0fb274 -->
Declared member: source declares reloadHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:754; symbol: reloadHls; span SHA256: accda529e6b616312b9cbeb609593aeb42bf3d244ea300485f25c20d06cad09a.

<!-- flowdocs:paragraph:73ea2a6fea271cf5b253a3efb2c17683 -->
Declared member: source declares reloadNative; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:970; symbol: reloadNative; span SHA256: 32726f32b3664188d900441550014f48e03995f0a9795ca5253c92a01e09d86c.

<!-- flowdocs:paragraph:d399b7e7c3ecc71435334047e18c3ecb -->
Declared member: source declares renegotiate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:644; symbol: renegotiate; span SHA256: bddcc9c3b3de65f2664848e5c346a1cecd4b484774954b32f5f6abf4118587d4.

<!-- flowdocs:paragraph:b5a24b5dc084c3dc543fff8783aa25c7 -->
Declared member: source declares resumeLoad; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:745; symbol: resumeLoad; span SHA256: 26be51862b786a22e9ddb81c2cac944678165501f91fed6a1099067f4ef20dd7.

<!-- flowdocs:paragraph:828b6772d64db8b04532f5c206e6e15d -->
Declared member: source declares retry; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:386; symbol: retry; span SHA256: a67d4fb6730d24442355f4b595ad67c50d34f6a92f09acb1714c98b66e459ea2.

<!-- flowdocs:paragraph:0cf73c5afe5d9471a7af9a5b34e239e2 -->
Declared member: source declares setRecovering; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:460; symbol: setRecovering; span SHA256: 3f6f0ba473ab4d9abb0f9d53adb57137345ffa4fadf79f0ed62fa36e80de3cc7.

<!-- flowdocs:paragraph:a4da4a422f072b13c0a492283c50fd7e -->
Declared member: source declares setWaitingForNetwork; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:486; symbol: setWaitingForNetwork; span SHA256: b6033aff81708e64ec61d1a01ab674beebda8169d8e0534f0b6f091cba6db63c.

<!-- flowdocs:paragraph:8dded376690a68911c224631646da1a7 -->
Declared member: source declares showPlaybackInfoError; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:525; symbol: showPlaybackInfoError; span SHA256: a29165bc45aee5685f5749c6ef4a5bc84285b80c108bac104c6a2dc6aebc6ecc.

<!-- flowdocs:paragraph:6a25f85a000f6e484a01af81f09a4b44 -->
Declared member: source declares showSourceEndsEarly; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:833; symbol: showSourceEndsEarly; span SHA256: 9469db691ab294775b716560941f2179971eb2a06ea2172f8bc67aada5a2804a.

<!-- flowdocs:paragraph:fbf4c1d768bb339d442e2dcd4eda6e6b -->
Declared member: source declares showUnavailable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:475; symbol: showUnavailable; span SHA256: 43cdcf5957cbd77195906162d67022d24c74e5313a2189fea704d7c1e5877d08.

<!-- flowdocs:paragraph:cbc653a581543d156dae414a4e7bce71 -->
Declared member: source declares start; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:538; symbol: start; span SHA256: 8fd9326b952587a2007dcceeef139cfdab1e4649b872ddf8b3b02d0f03e4b1c1.

<!-- flowdocs:paragraph:6d80de4cf3088fb5c6242a5283f1d4fc -->
Declared member: source declares stopAtSourceEnd; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:843; symbol: stopAtSourceEnd; span SHA256: 5d967be982b7b5cc06ce1aedad1d2eb197c0492806e1d0204024ec4d6a569681.

<!-- flowdocs:paragraph:07e58b47035a9ab19335a4f185cdd1c5 -->
Declared member: source declares stopForIdentityChange; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:445; symbol: stopForIdentityChange; span SHA256: 95215ab3f4ccbd48d560183d5ed47565ac94e29ba081b38b43aa6c8905b5ad0f.

<!-- flowdocs:paragraph:9c078449ac35444fb02a36bfa29395a9 -->
Declared member: source declares switchToHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:980; symbol: switchToHls; span SHA256: 8b480640780b9eb0031075b63d09e376e9e48e3c24daeb47793f4fe7bb67027b.

<!-- flowdocs:paragraph:14532e6df223eedd86909877b4fc8bc9 -->
Declared member: source declares waitForNetwork; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:488; symbol: waitForNetwork; span SHA256: 70b69f94099d660fe55c2f435ae7b68161d67e1fb906e04c41c5f9e2ee0107d3.

