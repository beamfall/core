# Player route: lazy load, timestamp links, info overlay, audio stage — technical-deep-dive

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:41b41964c0c4920e543ab8d1becb7dd1 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:41; symbol: PlayerPage; span SHA256: 23cf3dbe101367b8eb37216ba93bd2f3395527a8ecb2a14a2b116775aff2b593.

<!-- flowdocs:paragraph:8af732ea464b9e36ad8409fcdd36dfd9 -->
Declared member: source declares buildTimestampedLink; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:403; symbol: buildTimestampedLink; span SHA256: c3fb37a335af45dc7c5cae087c62afcf46a82252a78c0690201022ad10dcf83e.

<!-- flowdocs:paragraph:66a214617dbb1bcb690e947a2e72999d -->
Declared member: source declares parseTimestampParam; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:397; symbol: parseTimestampParam; span SHA256: 80b1680c0056c1b51d64cca950ba7f60d20c43c0c7ce243fdca04e37fca9f109.

<!-- flowdocs:paragraph:1b75ba00eec3c8847e68b95cee156b8a -->
Declared member: source declares AudioStage; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/AudioStage\.tsx:11; symbol: AudioStage; span SHA256: b0ddbe700e9792a73aad4a344b29bbe74e7f5dd0c9cc74556ebc21ccf59f8158.

<!-- flowdocs:paragraph:d791b106c1a900537568f774dbb9ac40 -->
Declared member: source declares creditLine; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/AudioStage\.tsx:58; symbol: creditLine; span SHA256: f9b172ee9974751f80193f816427c52f425f64bd57a96ba167ce2e8430157e10.

<!-- flowdocs:paragraph:861f3c494050b4db5cc65d753f436a51 -->
Declared member: source declares cssURL; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/AudioStage\.tsx:71; symbol: cssURL; span SHA256: 0176f9d1f2bd4b04e22b85c2931fb447d277b80ad2cdd4d327443fa37ee79973.

<!-- flowdocs:paragraph:ddf72211e9a53eca5378ddd8f1d7d97b -->
Declared member: source declares InfoOverlay; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/InfoOverlay\.tsx:15; symbol: InfoOverlay; span SHA256: f069d849e318dad4ee82e4099ff512c575af034a6f312f6debd591012dd794c7.

<!-- flowdocs:paragraph:471bba825384c5b461ddaabc9a348bda -->
Declared member: source declares LazyPlayerPage; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/LazyPlayerPage\.ts:6; symbol: LazyPlayerPage; span SHA256: 152a4e6aa48116f25161381259a16bbf1818d53d4804e93fed314569f0fc0152.

<!-- flowdocs:paragraph:6accbf256f9f7173b2b4964eab95ba89 -->
Declared member: source declares preloadPlayer; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/player/preload\.ts:8; symbol: preloadPlayer; span SHA256: 2ce87c54b1ceb4362c2a94938072100f35dd8f1c448b7a102a5a95cfdfd25707.

<!-- flowdocs:paragraph:ab0e12be061df825a5ee8e42651f58dd -->
Declared member: source declares CLIENT\_PREFS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:362; symbol: CLIENT\_PREFS\_QUERY; span SHA256: 9bacd1650b0a3e712344a1d217c82eeb1787add2a7fe4dac99cd39090e64fe99.

<!-- flowdocs:paragraph:34a420e35dd60e836115a5a3a61a72e8 -->
Declared member: source declares MEDIA\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:170; symbol: MEDIA\_QUERY; span SHA256: be6e55ef3222be8279594a3e0402ba2a8e540264590a840ee79e868c766693d3.

<!-- flowdocs:paragraph:64ab0ff16bc94fe089dc4a4c0f8a7c0e -->
Declared member: source declares SET\_CLIENT\_PREF\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:370; symbol: SET\_CLIENT\_PREF\_MUTATION; span SHA256: e47826c070db48a265b316996d973df6f54760325eff3c60c8084c5923d1ff8b.

<!-- flowdocs:paragraph:4c5912436b95a9a0ced2c45490696193 -->
Declared member: source declares fetchClientPrefs; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:365; symbol: fetchClientPrefs; span SHA256: b1c6c568bae96333d435a5619a12f1cb5955c51390dbafc6e6a29c4e8e42bd5a.

<!-- flowdocs:paragraph:f3049922b7d497fddab3aecbed3d4e1c -->
Declared member: source declares fetchMedia; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:174; symbol: fetchMedia; span SHA256: db2bed2f155fa3c5406f6cd32f203e0963f6bef7dd39df82e589bd84643961cd.

<!-- flowdocs:paragraph:82c9d7e5dd233fda7eacf8ee314b2380 -->
Declared member: source declares setClientPref; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:374; symbol: setClientPref; span SHA256: 0433e22579aa6ed0e538cb7bf2139e3667df7b6b770bc97276ac7a4596835753.

<!-- flowdocs:paragraph:44c0a536d967104974563ca4ad6c2d89 -->
Declared member: source declares back; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:286; symbol: back; span SHA256: dfddc3cd508dcddafaa86975b777bfc8c318d6883d4d5578069dadf9fbcf6d80.

<!-- flowdocs:paragraph:bab5f71e048e8a6d8fcc3fdd9e23464f -->
Declared member: source declares playNext; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:155; symbol: playNext; span SHA256: 549fbee98e13e47ba9989ac03512f6dc28514550c716cf5d050131e0c83e965d.

<!-- flowdocs:paragraph:7539efee92ad731f49b968bc2b4472fb -->
Declared member: source declares playPrevious; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:166; symbol: playPrevious; span SHA256: 3e97b13cecdeba1be11dfb641ef8c3be601de36cdc2c1352345b78f107526819.

