# Session spine: compat handshake, session renewal, identity epoch, request guard — technical-deep-dive

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:8221c408ad1cbf321930b1c1e7f73535 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/lib/api\.ts:20; symbol: getIdentityEpochSignal; span SHA256: fa6d3dc2d90a19d916a83e3cccb881a3ebdc0bee71bee7562a6646c9727f4bcf.

<!-- flowdocs:paragraph:74a94af40ee53de13dfe4e742d219c66 -->
Declared member: source declares getIdentityEpochSignal; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:20; symbol: getIdentityEpochSignal; span SHA256: fa6d3dc2d90a19d916a83e3cccb881a3ebdc0bee71bee7562a6646c9727f4bcf.

<!-- flowdocs:paragraph:3ea092d49685e67d83efa42fc3e9fcf0 -->
Declared member: source declares installAPIRequestGuard; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:54; symbol: installAPIRequestGuard; span SHA256: 43e48be80f8c69cb872478b529b41e855748fe9b337421ce3f8ac7610226ff93.

<!-- flowdocs:paragraph:ad64e762d3b67fde5257534e7851cfe9 -->
Declared member: source declares isAuthRejection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:85; symbol: isAuthRejection; span SHA256: 6cf09b5b822f3ad3b5efbd0535faf1ec8ca1106f0054b4d73af9ff2e33b8a6f2.

<!-- flowdocs:paragraph:160c0840eeb760fe9fe8bf71cc95f24a -->
Declared member: source declares requestSignal; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:45; symbol: requestSignal; span SHA256: 03b33a2874514e5dad6e508a307fe4350a7ad4fcf61ea8fbc6f30a27caf00245.

<!-- flowdocs:paragraph:2dd137060a2d8101b4a7bb017c6d598a -->
Declared member: source declares resetIdentityEpoch; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:41; symbol: resetIdentityEpoch; span SHA256: 860b2deeaf857279ca9787f555e7357c5f940435a9ce0068c27a540e78f8236e.

<!-- flowdocs:paragraph:cf84ba3b6f0fd991029f1140361e6174 -->
Declared member: source declares setIdentityEpochReset; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:32; symbol: setIdentityEpochReset; span SHA256: fbf2f721b2543cfa82938648ca18800d47fa78f88fd90527db04685be56fbb9e.

<!-- flowdocs:paragraph:ed0bd4267a72ef2e84709b374375e374 -->
Declared member: source declares setIdentityEpochSignal; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:16; symbol: setIdentityEpochSignal; span SHA256: 6a8a149863f52b28598fcd45557ae5e0ed9d4fdfa4f424137c82208c346ebb8c.

<!-- flowdocs:paragraph:f3e015752e05de2f5aaacc301e2cea54 -->
Declared member: source declares RegisterProvider; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/register\.tsx:21; symbol: RegisterProvider; span SHA256: 54f2ed45c9bb618137b663caa1ff55b27624bd096ca2ad93e5fdf3932bf880ab.

<!-- flowdocs:paragraph:64c2390ab61ee121a5305e0ae4d1b1e7 -->
Declared member: source declares useRegister; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/register\.tsx:44; symbol: useRegister; span SHA256: cef3ed2a60a7df31fd3a93597ba5cccf7ae31932654a1b8837b3116b31b4e22f.

<!-- flowdocs:paragraph:74de69d6a03f769f9d1c4c06eb25b5e8 -->
Declared member: source declares fetchSessionRenewingOnce; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:42; symbol: fetchSessionRenewingOnce; span SHA256: 18d99a7bb626186dfe824899cd69f7714c0769d5abb41c6838c96b7bd5eb7883.

<!-- flowdocs:paragraph:d8bad0b632ed13604ca7c8291f5aa9fb -->
Declared member: source declares clearViewerCache; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:69; symbol: clearViewerCache; span SHA256: 3ec8a80cd63539c1b115f99187d643e32d538bc1c1726ab88ea473097966d40f.

<!-- flowdocs:paragraph:c62197101e32d4e5df36ff1ad0974d22 -->
Declared member: source declares deleteViewerCache; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:52; symbol: deleteViewerCache; span SHA256: ccd9741c617849e93e0d12406d7e20a41483f462f9a4d3eb6c0903b79be84b3b.

<!-- flowdocs:paragraph:3d9f6f0863ab1fd8be460f2fe963649a -->
Source contains a lexical call to delete; dispatch and effects are unresolved\.

Source: internal/web/app/src/lib/viewerCache\.ts:54; symbol: ; span SHA256: 0c560f4171ba01d1f231e889211a80b858ca1b1aa634489a4ba1d15e23206d92.

<!-- flowdocs:paragraph:dcb9e55ec43bda36f488e519da60c33d -->
Declared member: source declares entryKey; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:25; symbol: entryKey; span SHA256: d844df192fd950e5f2e56782f50a4c371ba82975346ab97204df38491daa6dc3.

<!-- flowdocs:paragraph:390524adfc00dc97461392782f2ba100 -->
Declared member: source declares viewerCacheEntries; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:58; symbol: viewerCacheEntries; span SHA256: cfb9e329df82b385f8afaceae72709b9583b5197202e72558a28fe7832e37afa.

<!-- flowdocs:paragraph:b2be0871193ea2bc8db50b1efc1f86da -->
Declared member: source declares viewerCacheSize; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:74; symbol: viewerCacheSize; span SHA256: f352ebe62b15d9766b71930ebe245078e32b09a6f4030db5d4a12e00f3c878a0.

<!-- flowdocs:paragraph:a9d79a7e02911cb38e37666e03979bdf -->
Declared member: source declares viewerIdentityKey; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/viewerCache\.ts:20; symbol: viewerIdentityKey; span SHA256: 305139365b85d3dd39506c26b78c662e7aeb72f94c4ed088e15a4f29d74f1181.

<!-- flowdocs:paragraph:6ee9bad78e8e7bcdba806023e478cb2f -->
Declared member: source declares settleRebind; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:26; symbol: settleRebind; span SHA256: b13faca8956ab229de82053f9ca2976e5f0b4a1b28d78439f5d3ec44657139bb.

<!-- flowdocs:paragraph:6c45a943c44711ab99fffc8491ecc96d -->
Declared member: source declares fetchManifest; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:44; symbol: fetchManifest; span SHA256: f678944620d27a3653050722a184d55d9d99f3d92c8df932a8eb051fe578751e.

<!-- flowdocs:paragraph:748b0203374403baab9688851ccf6113 -->
Declared member: source declares send; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/api\.ts:345; symbol: send; span SHA256: 46fffe57ac0094e245d60cfdebc26d3f4d4834bc5e1c0e0246c6c4d0be1e97a9.

<!-- flowdocs:paragraph:481b1d20ee94230e0ccbd2aad97a4814 -->
Declared member: source declares beginEpoch; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:123; symbol: beginEpoch; span SHA256: 4c7e95df9a9a5e994593e6bee0151ae9d386eaf7fd480fe172c677b4f9255fd5.

<!-- flowdocs:paragraph:80a3d8969b11960455c39f1494dff87b -->
Declared member: source declares current; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:140; symbol: current; span SHA256: 51eba3e627a69d30df8ab4582cb4e27aba311688274355fa1a6110c1bcba7526.

<!-- flowdocs:paragraph:ce7ce55b52a101e83e449c96cb911eb3 -->
Declared member: source declares load; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:204; symbol: load; span SHA256: 6758a0d799bfe6f10904ae7bf4e286aafb0ec202044f7867af5edb3c86a3dccc.

<!-- flowdocs:paragraph:30c1cc367b456fe1023c7f140bb4bd03 -->
Declared member: source declares loadEpoch; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:139; symbol: loadEpoch; span SHA256: dac55ea4c71192baa4d461bb710c50b92f0e3ee22b6245eb9ae711f76e9b76c6.

<!-- flowdocs:paragraph:d0dddfc4f53d6dcb4ecc305347439f2e -->
Declared member: source declares refreshProfiles; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:299; symbol: refreshProfiles; span SHA256: 3c4001e7bbe4c6f72ce42571adc1227089f4fc021f5afa95bd25a4f4400b53b4.

<!-- flowdocs:paragraph:9a9672b18d7d3d3ad672bff99c8a3c71 -->
Declared member: source declares relock; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/session\.tsx:278; symbol: relock; span SHA256: 185f4e521d343e3093b92ff209bb76ff5c7428981420e7c858c688d2250ecbfe.

