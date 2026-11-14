# Playback engine: playback\-info, capability probe, HLS selection and recovery, progress reports — technical-deep-dive

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:0dba614742002a4ca6c0bbcbdd7b4dda -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:85; symbol: AUDIO\_CANDIDATES; span SHA256: 94955eba6053462d874d6ccbf0bcdc13dbf76138e1a44d5d97fb0555b32e1339.

<!-- flowdocs:paragraph:cfaca83f27487dbf24651d0dba12daa1 -->
Declared member: source declares AUDIO\_CANDIDATES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:85; symbol: AUDIO\_CANDIDATES; span SHA256: 94955eba6053462d874d6ccbf0bcdc13dbf76138e1a44d5d97fb0555b32e1339.

<!-- flowdocs:paragraph:8ea0ea830f6e2d79300a24f04d4941c5 -->
Declared member: source declares AudioCodecCandidate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:8; symbol: AudioCodecCandidate; span SHA256: 97d88525069ee4c04c14fe0cbc446fb9f86cafca6d42f0f4679e2c4b5f71e964.

<!-- flowdocs:paragraph:42f5a0fc072d202b8cadf23ebcff7c08 -->
Declared member: source declares AudioCodecCap; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:33; symbol: AudioCodecCap; span SHA256: 9610bcb6df39d6bf1398d9de92eba496fab80048247e5c60a05fc062d08d9101.

<!-- flowdocs:paragraph:572c72b3b323cbbb1f3c23fa0b3c2945 -->
Declared member: source declares CONTAINER\_CANDIDATES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:95; symbol: CONTAINER\_CANDIDATES; span SHA256: 07222478ded2f7612291813b0daf65e15856455a46a951333e6b1e4c51442488.

<!-- flowdocs:paragraph:0ecb19ff3d7eb8920625167403d21209 -->
Declared member: source declares CapabilityProfile; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:38; symbol: CapabilityProfile; span SHA256: a9dc252017504a14e4c97afa5c34723ee7b5f4958ab897d1c3b66c8963af8b8f.

<!-- flowdocs:paragraph:7d26608113b16a177c9df0a025e45f38 -->
Declared member: source declares ContainerCandidate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:20; symbol: ContainerCandidate; span SHA256: 80ea854294d522472c4819d572e788dd844e884815a8ec2969ee0e7d503e38f7.

<!-- flowdocs:paragraph:b13f8066d0f1a0d331ed98747ad9c69c -->
Declared member: source declares PROBE\_HEIGHT\_1080; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:107; symbol: PROBE\_HEIGHT\_1080; span SHA256: 9a8465fcde5fd67ffe9d367168c63bb1c25f4dd60aebcb9b094931ba1c2989c7.

<!-- flowdocs:paragraph:43a5403c75b4ac18cd8875ce2088d77d -->
Declared member: source declares PROBE\_HEIGHT\_4K; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:108; symbol: PROBE\_HEIGHT\_4K; span SHA256: 0df3855b617890a68898f9acbac084a3795b6af649c74a2aa2fa5b415a0ce3b6.

<!-- flowdocs:paragraph:901af49a8d590a5a2216e501d27c2f64 -->
Declared member: source declares ProbeDeps; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:49; symbol: ProbeDeps; span SHA256: bacf4bb4b9a5b52a5d898aac99c60ef5f24c8661083eb441852e06ade46d21c9.

<!-- flowdocs:paragraph:e9708c45661dc7108b636cbc8d224cc2 -->
Declared member: source declares VIDEO\_CANDIDATES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:58; symbol: VIDEO\_CANDIDATES; span SHA256: 19dc088e40297a559c59e5ddf36536d8b409300c84228611397b837462624504.

<!-- flowdocs:paragraph:10b818b7212085ddf50d11010cba6265 -->
Declared member: source declares VideoCodecCandidate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:1; symbol: VideoCodecCandidate; span SHA256: bc7095682a59357b28082d5faf0a0cdc0fa63c8e0cba9c54caf184876278c3eb.

<!-- flowdocs:paragraph:7b7bfcffa2cd1afa3c620fde5ef92d02 -->
Declared member: source declares VideoCodecCap; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:25; symbol: VideoCodecCap; span SHA256: c3cfc20a6eef1e1e238a331b66a3e9c5808781d0ec2129396c1d82c2b584b563.

<!-- flowdocs:paragraph:3e68ddab5a808dad262e9448653ed0ab -->
Declared member: source declares canPlayTypeSupported; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:110; symbol: canPlayTypeSupported; span SHA256: 8cc499193d7ce10a5d319863ac112b07794c475a5b67a2fc9dd47f55e22b99a6.

<!-- flowdocs:paragraph:30562d965afb231a476c9ef7c907fc57 -->
Declared member: source declares downstreamAudioHint; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:222; symbol: downstreamAudioHint; span SHA256: bf3d03b7c3a04387bb8b3b74e8a597efd984ba6b8a47d3ea0eb941ff32d02759.

<!-- flowdocs:paragraph:6a4320a30d7a1d849d710e946eb9f829 -->
Declared member: source declares mediaCapabilitiesSupports; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:117; symbol: mediaCapabilitiesSupports; span SHA256: f40fdebb06850b60a2a85c196af49c5056d1f91a8485b271c819903c88857ab8.

<!-- flowdocs:paragraph:5b21e56e01fe2d781453c6fad5489dd1 -->
Declared member: source declares probeAudioCodec; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:179; symbol: probeAudioCodec; span SHA256: bca3bbabd94c5131c32dd6c20588c521e16e578f6b0adcc006f46f1119120717.

<!-- flowdocs:paragraph:3e9cfc924e52314ed5ebeaa7657dc4c8 -->
Declared member: source declares probeCapabilityProfile; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:234; symbol: probeCapabilityProfile; span SHA256: 160b3143e937ae407d8b58bc55c2dae4cdd4de3df24d1e7efc15516e9ae28cd7.

<!-- flowdocs:paragraph:a0f5dde7adabbc06e31b6ca04221aaf1 -->
Declared member: source declares probeHDR; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:197; symbol: probeHDR; span SHA256: 3f38fff906b4bde6615667c22aa59cc7b8b6a3bac6ff7649fe9380218bf5d4b1.

<!-- flowdocs:paragraph:96b311957c6a757a6cb93d75d1dbd244 -->
Declared member: source declares probeVideoCodec; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/capabilityProfile\.ts:132; symbol: probeVideoCodec; span SHA256: 1928e7bb16e918897dc6e76af77a15485a21ac7224ffd80122d124d392a766c0.

<!-- flowdocs:paragraph:efc5971b95703ca19f55b7e3365d8b4f -->
Declared member: source declares PlaybackAssets; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:36; symbol: PlaybackAssets; span SHA256: f25baefb77a9c98139bb8d1b413cf6f0320bd5f0851e1d05d7bdf63e4a690728.

<!-- flowdocs:paragraph:353e0445b8574151fc0d9ff6c0766b61 -->
Declared member: source declares PlaybackInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:60; symbol: PlaybackInfo; span SHA256: c06b9ea647f864fb4f0d50d8b6b9b6151b9bd6a018cac54bf8fe3d87b83cd3ab.

<!-- flowdocs:paragraph:fc06b9051c868c23503186331a4dc178 -->
Declared member: source declares PlaybackMessageAction; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:135; symbol: PlaybackMessageAction; span SHA256: 82635a6868b5d9244c634a6105d8c100b12460395bf2fad60875f77cafed3ee9.

<!-- flowdocs:paragraph:ac841ce0c85dc287d18d6850cc9b471b -->
Declared member: source declares PlaybackNext; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:48; symbol: PlaybackNext; span SHA256: aaf8366f2f5c6be61af7a28e52372fcdefef1f3f9a7d004e6567f013ff99bd3f.

<!-- flowdocs:paragraph:25262fec29b3f924068fe72b19d4adb5 -->
Declared member: source declares PlaybackRendition; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:15; symbol: PlaybackRendition; span SHA256: 2c8bac02e8eb42ecda34da9c64710eb88027e0e945dcc213953d3576456a7eac.

<!-- flowdocs:paragraph:e0d14e77f4823437371c7a372faa5033 -->
Declared member: source declares PlaybackStream; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:22; symbol: PlaybackStream; span SHA256: 8f3596531649579c6d0e53cb7c92d1a68291afc2c94b01d0d48aadbadfa3e2da.

<!-- flowdocs:paragraph:5306323bccfc02d988175f81b16f1623 -->
Declared member: source declares PlaybackTimeline; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:31; symbol: PlaybackTimeline; span SHA256: ea3de31ac5674c4b17f7db90b1de936ee468846b9a27e9f773f31249da4a5415.

<!-- flowdocs:paragraph:acc7390640a16bc47afabe200aa77768 -->
Declared member: source declares PreviewInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:78; symbol: PreviewInfo; span SHA256: 9c01787786610d4f7d609a4de17864b079e9762c0960f2ed15d5ecddc905d2d1.

<!-- flowdocs:paragraph:784da6a45ec99353c1f5f38933686272 -->
Declared member: source declares UNAVAILABLE\_REASONS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:152; symbol: UNAVAILABLE\_REASONS; span SHA256: f8a9b6aae94314387dbc2fc5e8ccb9db4c8b8a829cf54c70f320c2f1e5129c80.

<!-- flowdocs:paragraph:1e71e2c086c009732769493e34290c41 -->
Declared member: source declares UnavailableReasonMessages; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:138; symbol: UnavailableReasonMessages; span SHA256: 75e231f60645f2795eacbcc51fb37d6d908ab5be4aa77e70cbcb38a8e5cc0fbf.

<!-- flowdocs:paragraph:5710081c6de291f7e1dc01da385f04df -->
Declared member: source declares fetchPlaybackInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:103; symbol: fetchPlaybackInfo; span SHA256: 6c197262a7c51f224968ec7a4a67526f5d067c062e8683af5b58076f97b79db2.

<!-- flowdocs:paragraph:241e45fe5ca952b033ca9ce22af4e9ec -->
Declared member: source declares fetchPreviewInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:203; symbol: fetchPreviewInfo; span SHA256: 5aa44a0568d8f96574af98fb2de8cf99a25c0bbce319896cae82dd4e725f794e.

<!-- flowdocs:paragraph:6df5973afa8f11138911ddd4c3b171e3 -->
Declared member: source declares hlsStreamOf; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:84; symbol: hlsStreamOf; span SHA256: 32425bbec1c0aa1d5ce0e6ca3077d93f9099823717c0a98e535315f805c847ab.

<!-- flowdocs:paragraph:8d755a6e5b7ff7444c8c253d3b2f0f6c -->
Declared member: source declares isRevealTerminalStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:89; symbol: isRevealTerminalStatus; span SHA256: c3996a71b4b0de77ebe373329f5cd6f30ca49ce5cf2e33f365dba6ac4a02b672.

<!-- flowdocs:paragraph:d24a248165adb41e6634d7f08b44cde1 -->
Declared member: source declares serverErrorReason; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:193; symbol: serverErrorReason; span SHA256: bd31be6a1600e66f3d7f23a6a844d4df85626644dbcb66ed7bbe4b83958611eb.

<!-- flowdocs:paragraph:18e1f2102a575077fa6b56f0363439b4 -->
Declared member: source declares unavailableReason; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:170; symbol: unavailableReason; span SHA256: 32c5f269dfa1e2feeb7137087defdc7a2d993600a0f98868f6bb1620677399b3.

<!-- flowdocs:paragraph:ed6be69663bb0ef8e0a57dddc8c02f81 -->
Declared member: source declares unavailableReasonMessage; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/playback\.ts:181; symbol: unavailableReasonMessage; span SHA256: ddba225ff0746418a72a65150b0a718f786ddb598a7a9899d43df2e6788331d6.

<!-- flowdocs:paragraph:d9a8019de224eab8406935789675cd8b -->
Declared member: source declares PlaybackStreamLike; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:358; symbol: PlaybackStreamLike; span SHA256: f4565c3c232d59c2c59ffe6e8bbdd32db1f61d0ea6d217711e3bab0c4a6b27fd.

<!-- flowdocs:paragraph:0d3c0a2d86c9d1d59cb078adc91f18df -->
Declared member: source declares playbackDecision; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:348; symbol: playbackDecision; span SHA256: 33a5a732a700cfdcedf0b8c7c56a626c21e564f75372d20fc0d7de516b29f993.

<!-- flowdocs:paragraph:85a5259cc453235768891f5c6c606cfd -->
Declared member: source declares resumePositionFromInfo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:342; symbol: resumePositionFromInfo; span SHA256: 0f4ee24b755d5bea0f94877f581655c0112522feb31910ef755e5222cfc4cbd5.

<!-- flowdocs:paragraph:57117faf501afb34ea5ffa1fcc027d99 -->
Declared member: source declares HLS\_FRAG\_LOAD\_ERROR; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:13; symbol: HLS\_FRAG\_LOAD\_ERROR; span SHA256: 392453a587ba1e84cf3598f77fd57b3fac05d0fa0cc59a76bbe0e8f94598790e.

<!-- flowdocs:paragraph:5fc82e4373af52fae92160a02e66e74c -->
Declared member: source declares HLS\_MEDIA\_ERROR; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:14; symbol: HLS\_MEDIA\_ERROR; span SHA256: 7652bb895246b6986c423c8640d7bf7faf4861363035abf834a6958c2f6db6cc.

<!-- flowdocs:paragraph:f5dc6310c47ca75356a68bf2d11f2a6c -->
Declared member: source declares HLS\_NETWORK\_ERROR; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:11; symbol: HLS\_NETWORK\_ERROR; span SHA256: 6431ea4ad6bcc136a78dc393ea232f9f339c2820e522aa2775d59fd844bc1553.

<!-- flowdocs:paragraph:8e40c375b33e349c4241c8b91320338d -->
Declared member: source declares MEDIA\_RECOVERY\_ATTEMPTS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:17; symbol: MEDIA\_RECOVERY\_ATTEMPTS; span SHA256: c82ee7fb6aa0835b0a8529138672745b80861a490dcc226dc32c3766975df2d9.

<!-- flowdocs:paragraph:61760d53e1152280ad697c43a2e404db -->
Declared member: source declares NETWORK\_RETRY\_DELAYS\_MS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:16; symbol: NETWORK\_RETRY\_DELAYS\_MS; span SHA256: 2a18aef998bdc2f36ba8534979f76cb0533830ce2f46690cbd4a69aa03707c61.

<!-- flowdocs:paragraph:59680c52fd2604f22e310ce6b84231ba -->
Declared member: source declares RecoveryBudget; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:19; symbol: RecoveryBudget; span SHA256: 0fd6db7583ff5bdad9ae61240268ce15352f91f2dd728873a3c49f844d9e954a.

<!-- flowdocs:paragraph:88981aba43fa92f6ba994d5861e8721b -->
Declared member: source declares RecoveryStep; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/hlsRecovery\.ts:24; symbol: RecoveryStep; span SHA256: 1ce9475937df383d5fa740a67051dac8465253d027eb0eeb640f584436aa14ee.

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

<!-- flowdocs:paragraph:d5a8e2e84a1355a16256549fd22e6047 -->
Declared member: source declares BUFFERING\_SPINNER\_DELAY\_MS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:4; symbol: BUFFERING\_SPINNER\_DELAY\_MS; span SHA256: 8c389abfd2b3e9cb724282e26819f7dd55dd245a58ee32c4f94f863a6c4e2c42.

<!-- flowdocs:paragraph:0e9607c5a07e2d7cf35acba0a62a2e21 -->
Declared member: source declares useMediaLoading; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/useMediaLoading\.ts:13; symbol: useMediaLoading; span SHA256: 64540a9fd129f836662dd21193052f58c21ac044d975d6e1ab5fb69a2129f7c2.

<!-- flowdocs:paragraph:27a676425d546e2f4418a8a9ef749e97 -->
Declared member: source declares Engine; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:260; symbol: Engine; span SHA256: d1e7e0736df74cbf86c093011933833e43872d2af6cc6129f8570d7993a5a13e.

<!-- flowdocs:paragraph:3dedd48f6fb6d975b26b941ead0e0b74 -->
Declared member: source declares PlayerPhase; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:38; symbol: PlayerPhase; span SHA256: 13ee46111bd7bc7d5277136b46be13d1c0d736b398820c7a7c30e9a6392a32cd.

<!-- flowdocs:paragraph:ba353253a31eff0a39f4df4f50406cab -->
Declared member: source declares PlayerState; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:40; symbol: PlayerState; span SHA256: 700900b86e6ea355ba6bd49a0d8b6d45c2d8daca7bfc80b90eaa69f35aa1a559.

<!-- flowdocs:paragraph:a8095fcb116e14726bc113407d0a0db2 -->
Declared member: source declares ProgressQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:80; symbol: ProgressQueue; span SHA256: ea24d1f3c6c5344c2152b53a4967e27e44d27251cc05937ae0cc2864e4535f93.

<!-- flowdocs:paragraph:8d334bf658123613208208e6de94d3b0 -->
Declared member: source declares REPORT\_INTERVAL\_MS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:75; symbol: REPORT\_INTERVAL\_MS; span SHA256: a147b371295597c2dc2d5464a6ee9f7d41029f8ce2edaff67e4b358ae894728e.

<!-- flowdocs:paragraph:c791fac69aac9679ab7dc8ad2eb07512 -->
Declared member: source declares REPORT\_PLAYBACK\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:76; symbol: REPORT\_PLAYBACK\_MUTATION; span SHA256: 442b7c5d44f1b4b3f266947bfc95c4114561f2824722edfab6ba204331cd5461.

<!-- flowdocs:paragraph:e2976ce53e27e3ccf04a72b8503ce629 -->
Declared member: source declares STREAM\_PROBE\_TIMEOUT\_MS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:315; symbol: STREAM\_PROBE\_TIMEOUT\_MS; span SHA256: cacf84aa21ece0cf0a48e00f3c210dbfb7e50f54efb141b309fb95b449e52c07.

<!-- flowdocs:paragraph:264592d2b4d218b833ae0f40cf676891 -->
Declared member: source declares SwitchResult; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:273; symbol: SwitchResult; span SHA256: 40ef3d399a9e06726abc902dea0dadcada6cdab821142e5cb586353cb559cf55.

<!-- flowdocs:paragraph:303aa3fee69c3fb82e21b9ed4c0999f8 -->
Declared member: source declares TrackPreferences; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1143; symbol: TrackPreferences; span SHA256: 564559c548bafa01ce56f035dd576f1d0c4c115ca09c72c041c6e0769855349b.

<!-- flowdocs:paragraph:daa19b8fe82d10db60838e9f1a615c71 -->
Declared member: source declares applyTrackPreferences; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1155; symbol: applyTrackPreferences; span SHA256: a342b6c9cf04ec3e33f9f90c24e742d11bd51b9625106f1cd154c75f426b0f79.

<!-- flowdocs:paragraph:6e61d58164dc57ff6f68cbc7ee333302 -->
Declared member: source declares assetsExpired; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1214; symbol: assetsExpired; span SHA256: e2e6e9cfc6f2a88c9453386e05e5167245c795d7dde58eee452028053ba2cfcf.

<!-- flowdocs:paragraph:676b82d95fdee1850e6d6573ae30f5ac -->
Declared member: source declares bufferedAhead; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:231; symbol: bufferedAhead; span SHA256: 83c3d4157d91835eb5c128a31d717a488aa2b6db9b0c6e9c83431e1b8f4ae53f.

<!-- flowdocs:paragraph:ef17041d9d44e0408ba3b4d603b1011e -->
Declared member: source declares choosePlaybackStream; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:248; symbol: choosePlaybackStream; span SHA256: adfa47117c9c04c0da7de39557d39c3fefed56767870b526b1494d5967fc26fd.

<!-- flowdocs:paragraph:cbd932213a85a370bc2d9281658aca7f -->
Declared member: source declares completeProgress; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:126; symbol: completeProgress; span SHA256: 2c0587d5b3b9f0022a19623ecc2602b191d124bf95b229cf1c2fa78b2fb09e3b.

<!-- flowdocs:paragraph:c276d3993dce8b2d27608e44dd708844 -->
Declared member: source declares createProgressQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:91; symbol: createProgressQueue; span SHA256: 4ae1bcf29664334aaae4699172b2b1cc2afdbae510b6ea0dd3b72b3c004f2dde.

<!-- flowdocs:paragraph:43f33a3c2937a7abe4999a81865ee6df -->
Declared member: source declares emptyState; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:180; symbol: emptyState; span SHA256: e92dd11b17a5752444894e0dc3430aad5d3030b52de131ff23e07b9cde211011.

<!-- flowdocs:paragraph:265a6bed261caba32b747bd91e767dad -->
Declared member: source declares engineFor; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:263; symbol: engineFor; span SHA256: 8749cd9b4402937c29afe62f1f575d15c6819f2b06715a3516ae29fab2e106ce.

<!-- flowdocs:paragraph:f17e8ca3ec4f1682365b09a6fcee6343 -->
Declared member: source declares enqueueProgress; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:102; symbol: enqueueProgress; span SHA256: a33e8e53cb3036c980d0a3471bbeffaca4b2b238b74aef143cc2ddc06f4850c4.

<!-- flowdocs:paragraph:301ccf28f72b15b2465364fba6f307a1 -->
Declared member: source declares hlsErrorStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:212; symbol: hlsErrorStatus; span SHA256: 51c9a8e88750065e4e18fcd0fd811ddef0f5e657732d42a8f4472c74f8c3f304.

<!-- flowdocs:paragraph:28247c5635320debb1b405dacd0779b0 -->
Declared member: source declares hlsFragSpan; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:222; symbol: hlsFragSpan; span SHA256: 36ac2955ef06b0d8e27e1ac347c70b209109f578d1cab213e801f73d65b253ef.

<!-- flowdocs:paragraph:6b77132dda7c2479fc0d2f0c272f339e -->
Declared member: source declares isStreamRefusalStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:244; symbol: isStreamRefusalStatus; span SHA256: 73fc413929ce0e73606e3e4dcbd140d5ea4dc98973c045789616e2a4e2729b29.

<!-- flowdocs:paragraph:aff3a3431662a1064bf2a3476fd6ab06 -->
Declared member: source declares lastPlaybackEventTimeMS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:78; symbol: lastPlaybackEventTimeMS; span SHA256: 27dceddc20cefb7839ec9e74d539a8173a7fdfcd17dda135d866eefd8bbc845d.

<!-- flowdocs:paragraph:a8a1cdcca825a81055893d045996497d -->
Declared member: source declares loadTrickplay; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1192; symbol: loadTrickplay; span SHA256: 7748910900e363b131f4abd5a5dffcec67825dfa49e1f56641c7291ba67656b9.

<!-- flowdocs:paragraph:b075172b7ca6b9c54f86ac8234a15279 -->
Declared member: source declares nextPlaybackEventTimeMS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:174; symbol: nextPlaybackEventTimeMS; span SHA256: 226beefa34eabbfd85575d927babd702e6c2756ae0a6d98a1a1d7e257bdc32c5.

<!-- flowdocs:paragraph:78cf0b86f1a128f06415da9b9f1ef986 -->
Declared member: source declares pendingLevelPins; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:280; symbol: pendingLevelPins; span SHA256: 542d2a89f9ea91e5b2bd08bd045caedf2fc97511c25657a47a22d550aa3ae451.

<!-- flowdocs:paragraph:0cacd35f941d2c95c1e74f561233737e -->
Declared member: source declares playbackDecision; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:336; symbol: playbackDecision; span SHA256: 43d030bb9def050507befaf2bf5ccc7d86d96df8f0c140b4387c04facb96f7c4.

<!-- flowdocs:paragraph:21ea50b81bd9893db92d27ff52252f28 -->
Declared member: source declares probeStreamStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:317; symbol: probeStreamStatus; span SHA256: 540759e2fa4ad7bd5aabc1a1072bd7951629b4a634ab6690f4679b159196d20f.

<!-- flowdocs:paragraph:d378f8afc245e42b6a46ae41b6e25457 -->
Source contains a lexical call to fetch; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:322; symbol: ; span SHA256: f356ae58a3e67abc55f35a4b9b67d4ca4f70d6cb134d1595b126b810c3504a18.

<!-- flowdocs:paragraph:3c414818bd20d47fc6b778a23df399a1 -->
Declared member: source declares retireProgressQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:153; symbol: retireProgressQueue; span SHA256: f2cebbe9e388f0d55b8c300dde109d834141008febe0870aae23122e0c136020.

<!-- flowdocs:paragraph:51e3ad0bf105edd72a6f4f991a0475c3 -->
Declared member: source declares selectHlsLevel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:287; symbol: selectHlsLevel; span SHA256: 1a5224d128bcc251b8dc433fa4680bcb81cadea1edf82d05854674efe4f99892.

<!-- flowdocs:paragraph:d377aafdfb8fa2f8ae1827f48816e1b8 -->
Source contains a lexical call to delete; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:290; symbol: ; span SHA256: bf071c4b6fb6447d88a1d63abfa65841de12e92828b5e9e63a2ace24a8827a34.

<!-- flowdocs:paragraph:047ef31203ab0a8d6f0ff99a0dbe0e7d -->
Declared member: source declares selectHlsRendition; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:300; symbol: selectHlsRendition; span SHA256: fd64e27154e8b7756045908d7a4d17b32b5b8312094a28fbff52081408e06790.

<!-- flowdocs:paragraph:561efedc6d94f3309a48124e070d9b51 -->
Declared member: source declares sendProgress; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:109; symbol: sendProgress; span SHA256: 25524918a6fbf65f0826f696042bbc34344769ea775b9e61e115c989a60c3b5e.

<!-- flowdocs:paragraph:ed7b3eaf026c5f4123198c6b903dcf2d -->
Declared member: source declares stopProgressQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:139; symbol: stopProgressQueue; span SHA256: 1792583180ccb676c46fe659c482ebfec1b80d269fb4312529fff9470d968794.

<!-- flowdocs:paragraph:5f9856876807da7ff7cad89ceec0a4af -->
Declared member: source declares streamOfKind; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:256; symbol: streamOfKind; span SHA256: 48cf24adb33a4ab732385df152633b56d849a1e35f18d3c5e0183fdc7e59a10a.

<!-- flowdocs:paragraph:c895546ef12a28ccc7b2683d471c945f -->
Declared member: source declares terminalState; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:204; symbol: terminalState; span SHA256: d03a0feadd35a782fe8aa84b02caeeab15288a0b17f17251663be25e0414bae6.

<!-- flowdocs:paragraph:20d2efd9344ba822cc53ffeee492450b -->
Declared member: source declares CLIENT\_PREFS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:362; symbol: CLIENT\_PREFS\_QUERY; span SHA256: 9bacd1650b0a3e712344a1d217c82eeb1787add2a7fe4dac99cd39090e64fe99.

<!-- flowdocs:paragraph:058d7a97b937af79c41662c85c2c1b20 -->
Declared member: source declares PLAYBACK\_MARKERS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:269; symbol: PLAYBACK\_MARKERS\_QUERY; span SHA256: 544d5358f08fd6ae9a76cf90c961836ab9b1c6a79b27a7727786601bd8e027de.

<!-- flowdocs:paragraph:746ce06aa8bd3bd3837c7c6e460b4e02 -->
Declared member: source declares fetchClientPrefs; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:365; symbol: fetchClientPrefs; span SHA256: b1c6c568bae96333d435a5619a12f1cb5955c51390dbafc6e6a29c4e8e42bd5a.

<!-- flowdocs:paragraph:96bb1249b851799f390b67d9d0421d04 -->
Declared member: source declares fetchPlaybackMarkers; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:273; symbol: fetchPlaybackMarkers; span SHA256: 42907473fcded61af03c4d6d88719493d434aba6a290f8f9ba73f01db77e2aa0.

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

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:741; symbol: applyPreferencesThenBump; span SHA256: d187da2f6325236ab079cb20179051416ed90329667309954bc22805482f74ea.

<!-- flowdocs:paragraph:ff2ea91816ea5a95356de0f165c7e622 -->
Declared member: source declares atSourceEnd; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:854; symbol: atSourceEnd; span SHA256: 8674b536787ea52e44142f106cba63b968db249b78a3a0965bc6b3b32bcd59c6.

<!-- flowdocs:paragraph:ba6da69360e47397f53c45e249c621bb -->
Declared member: source declares bumpTracks; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:736; symbol: bumpTracks; span SHA256: cd688cca0de40abd29755d80dd1057bba031fb66db192b932e0e1753b13e532b.

<!-- flowdocs:paragraph:b324e2edd81ec4052388dd166d59c186 -->
Declared member: source declares destroyHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:391; symbol: destroyHls; span SHA256: 697b62bf069b6af4195e118771065f85df7f0187e7fa3de1d617230193051dd2.

<!-- flowdocs:paragraph:a3d2ef11da8cbe79f39476aa69156f64 -->
Source contains a lexical call to destroy; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:393; symbol: ; span SHA256: 0e059a072dd156d9519052bd52f618ff8ec22cf4e35ab305a667bdd8818d3fed.

<!-- flowdocs:paragraph:f8d909e726c371e0e5bef7bb5550fcd2 -->
Declared member: source declares detachEngine; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:627; symbol: detachEngine; span SHA256: 8580a995d1cbd1fdc100934fb0572f573774ce4b18813a4f372826821e9e59f5.

<!-- flowdocs:paragraph:1b6b34afae94fdc185661a65795ac3c5 -->
Declared member: source declares enterRevealTerminal; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:529; symbol: enterRevealTerminal; span SHA256: 09ba3225d3833f99de86b5cdaf130be0396b4478ec5d491917e438e388490ef4.

<!-- flowdocs:paragraph:0bf543c2c8f98b97dbac96ec64c74516 -->
Declared member: source declares failPlayback; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:755; symbol: failPlayback; span SHA256: 3ff24e1b45055cbb676101a4224a52d8c7f5889713995890e9b0ed858a57e119.

<!-- flowdocs:paragraph:bedb0445918c6e3c3eba8e268db29053 -->
Declared member: source declares onEnded; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1126; symbol: onEnded; span SHA256: 8100981419b13a4f1a079b20be7fc3d2642ead5fef56406a59da03c2ee440f83.

<!-- flowdocs:paragraph:0501fadd349aee1e8b108ae0be4bc3e4 -->
Declared member: source declares onOffline; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:507; symbol: onOffline; span SHA256: faadbc49978d76466c625634c744b228611908b55f9955048131be5f73ed5a3c.

<!-- flowdocs:paragraph:ffaebe6c0fbe2cb32aa7bc190bc813a7 -->
Declared member: source declares onOnline; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:511; symbol: onOnline; span SHA256: ec4d19749ac4b326f07f5d247ca4300abe9a5f8162f8c5abfda758ed3d75a40d.

<!-- flowdocs:paragraph:ef715f71372fe052e0d2377d31eb5e91 -->
Declared member: source declares onPause; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1124; symbol: onPause; span SHA256: 5399166540ecb713435d3520d2c5f15ffc3f5cc5985fe882a735bb84ffdff3a6.

<!-- flowdocs:paragraph:923bc59cf0655ea5449ddab52e24ca01 -->
Declared member: source declares onPlaying; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:600; symbol: onPlaying; span SHA256: e47d6a89a8d15ffb4d9999fa94141f70c4c7cd6ceeca6f4822ddf1e59eaaafce.

<!-- flowdocs:paragraph:626fdad2f97f12ff0454f785c80a88be -->
Declared member: source declares onSeeked; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:1125; symbol: onSeeked; span SHA256: 9dc84a9492be6fefc4ee449754d18325ae26d5e4d55936931e3e7d7c0043a143.

<!-- flowdocs:paragraph:55a4c420978728a2169cbd2fd2aa52d7 -->
Declared member: source declares onSourceEndSeeking; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:870; symbol: onSourceEndSeeking; span SHA256: 0a84640f045942e37ccd0a61d047ff2d1c213ef30187a6462c959a5a46d1b512.

<!-- flowdocs:paragraph:2d2a7bd4218a95de63188be1bc1d10aa -->
Declared member: source declares onSourceEndWaiting; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:867; symbol: onSourceEndWaiting; span SHA256: 0220c83f55174c8dde00fa2f981fbc033abb7ce7d382d8a0327d13400579d585.

<!-- flowdocs:paragraph:2322261a69a760e1f3dee42b3f2da510 -->
Declared member: source declares playheadPosition; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:589; symbol: playheadPosition; span SHA256: b0323211cdafb42403f03982fd4e04cc697f206e44fcc4ce66f06cd55a6ba838.

<!-- flowdocs:paragraph:7b579796ddc6f67b9a5cd3f5d9da88d2 -->
Declared member: source declares reloadAtPlayhead; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:929; symbol: reloadAtPlayhead; span SHA256: a179d44d6b19ad1c61610e6ce7d50c78cacd43486b4cff57e936a7f40247f5f1.

<!-- flowdocs:paragraph:566190f2dc9bcbd411c746db9d0fb274 -->
Declared member: source declares reloadHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:769; symbol: reloadHls; span SHA256: accda529e6b616312b9cbeb609593aeb42bf3d244ea300485f25c20d06cad09a.

<!-- flowdocs:paragraph:73ea2a6fea271cf5b253a3efb2c17683 -->
Declared member: source declares reloadNative; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:985; symbol: reloadNative; span SHA256: 32726f32b3664188d900441550014f48e03995f0a9795ca5253c92a01e09d86c.

<!-- flowdocs:paragraph:d399b7e7c3ecc71435334047e18c3ecb -->
Declared member: source declares renegotiate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:659; symbol: renegotiate; span SHA256: bddcc9c3b3de65f2664848e5c346a1cecd4b484774954b32f5f6abf4118587d4.

<!-- flowdocs:paragraph:b5a24b5dc084c3dc543fff8783aa25c7 -->
Declared member: source declares resumeLoad; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:760; symbol: resumeLoad; span SHA256: 26be51862b786a22e9ddb81c2cac944678165501f91fed6a1099067f4ef20dd7.

<!-- flowdocs:paragraph:828b6772d64db8b04532f5c206e6e15d -->
Declared member: source declares retry; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:386; symbol: retry; span SHA256: a67d4fb6730d24442355f4b595ad67c50d34f6a92f09acb1714c98b66e459ea2.

<!-- flowdocs:paragraph:0cf73c5afe5d9471a7af9a5b34e239e2 -->
Declared member: source declares setRecovering; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:475; symbol: setRecovering; span SHA256: 3f6f0ba473ab4d9abb0f9d53adb57137345ffa4fadf79f0ed62fa36e80de3cc7.

<!-- flowdocs:paragraph:a4da4a422f072b13c0a492283c50fd7e -->
Declared member: source declares setWaitingForNetwork; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:501; symbol: setWaitingForNetwork; span SHA256: b6033aff81708e64ec61d1a01ab674beebda8169d8e0534f0b6f091cba6db63c.

<!-- flowdocs:paragraph:8dded376690a68911c224631646da1a7 -->
Declared member: source declares showPlaybackInfoError; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:540; symbol: showPlaybackInfoError; span SHA256: a29165bc45aee5685f5749c6ef4a5bc84285b80c108bac104c6a2dc6aebc6ecc.

<!-- flowdocs:paragraph:6a25f85a000f6e484a01af81f09a4b44 -->
Declared member: source declares showSourceEndsEarly; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:848; symbol: showSourceEndsEarly; span SHA256: 9469db691ab294775b716560941f2179971eb2a06ea2172f8bc67aada5a2804a.

<!-- flowdocs:paragraph:fbf4c1d768bb339d442e2dcd4eda6e6b -->
Declared member: source declares showUnavailable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:490; symbol: showUnavailable; span SHA256: 43cdcf5957cbd77195906162d67022d24c74e5313a2189fea704d7c1e5877d08.

<!-- flowdocs:paragraph:cbc653a581543d156dae414a4e7bce71 -->
Declared member: source declares start; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:553; symbol: start; span SHA256: 8fd9326b952587a2007dcceeef139cfdab1e4649b872ddf8b3b02d0f03e4b1c1.

<!-- flowdocs:paragraph:6d80de4cf3088fb5c6242a5283f1d4fc -->
Declared member: source declares stopAtSourceEnd; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:858; symbol: stopAtSourceEnd; span SHA256: 5d967be982b7b5cc06ce1aedad1d2eb197c0492806e1d0204024ec4d6a569681.

<!-- flowdocs:paragraph:07e58b47035a9ab19335a4f185cdd1c5 -->
Declared member: source declares stopForIdentityChange; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:459; symbol: stopForIdentityChange; span SHA256: 95215ab3f4ccbd48d560183d5ed47565ac94e29ba081b38b43aa6c8905b5ad0f.

<!-- flowdocs:paragraph:9c078449ac35444fb02a36bfa29395a9 -->
Declared member: source declares switchToHls; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:995; symbol: switchToHls; span SHA256: 8b480640780b9eb0031075b63d09e376e9e48e3c24daeb47793f4fe7bb67027b.

<!-- flowdocs:paragraph:14532e6df223eedd86909877b4fc8bc9 -->
Declared member: source declares waitForNetwork; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/usePlayer\.ts:503; symbol: waitForNetwork; span SHA256: 70b69f94099d660fe55c2f435ae7b68161d67e1fb906e04c41c5f9e2ee0107d3.

