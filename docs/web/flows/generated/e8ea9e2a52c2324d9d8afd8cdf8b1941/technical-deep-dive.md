# Viewer search page: modes \(text, semantic, recommend\) and grouped results — technical-deep-dive

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:fb268ba5fc5641d6a895e78705802a11 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/viewer/lib/searchModes\.ts:33; symbol: availableModes; span SHA256: 5f4a403960512cc16dcbe3f84c42469146299f7a38480c983ce1ffedf91d050c.

<!-- flowdocs:paragraph:04b1c874b9b658ecbdc8e0197ad086f1 -->
Declared member: source declares availableModes; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchModes\.ts:33; symbol: availableModes; span SHA256: 5f4a403960512cc16dcbe3f84c42469146299f7a38480c983ce1ffedf91d050c.

<!-- flowdocs:paragraph:d16aaaaac29c89e5ffd9b30f689903c5 -->
Declared member: source declares recommendAvailable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchModes\.ts:23; symbol: recommendAvailable; span SHA256: 683eab7e01efa0af5d9f24e1f600b3f0bc26f379982026dd81133a6931191dcd.

<!-- flowdocs:paragraph:1c538f6a3c9320a13e37f7082ba9de3d -->
Declared member: source declares semanticAvailable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchModes\.ts:18; symbol: semanticAvailable; span SHA256: 9985055b7223a684220ce114edc0340e8c1c3a53df0dce2c7d729868a3fbaf07.

<!-- flowdocs:paragraph:547fb63b2a778da4528584ef9b7466b3 -->
Declared member: source declares HEADER\_SEARCH\_ROWS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:9; symbol: HEADER\_SEARCH\_ROWS; span SHA256: d71bd7a98b639f79d97d3192b9c54a5dea1875e738df5b2700b19fc79c2b034c.

<!-- flowdocs:paragraph:cfb5cca548ad84a4bc1a2bf7193e1c18 -->
Declared member: source declares MEDIA\_CLASS\_LABEL; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:11; symbol: MEDIA\_CLASS\_LABEL; span SHA256: 6376538f6b07acecb95b382fba2a73bda4ba78dfa66f35af399d7378805c26d5.

<!-- flowdocs:paragraph:7b6f29e70197bd13c03591731b6dd5e7 -->
Declared member: source declares SearchRowGroup; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:17; symbol: SearchRowGroup; span SHA256: bbbd779379e846d675748f889aff9e9dafc3eeef51367726f0e210d683e4f5b5.

<!-- flowdocs:paragraph:70902e4cc31e20815bf023edc798eed1 -->
Declared member: source declares Translate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:23; symbol: Translate; span SHA256: 3d5db25d82bf8f9f2a4932384f6b1a1e5e7fea89edface6710ddcc180f332786.

<!-- flowdocs:paragraph:9cec78669ac66d35bb395c54b5694dcf -->
Declared member: source declares groupKey; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:36; symbol: groupKey; span SHA256: c63f4da03db11a83be1d227bbd88c9d500724655cd1357d3f1d1485d3784a4b8.

<!-- flowdocs:paragraph:3e60ba45c9f3b13ab48fb347ad7769c9 -->
Declared member: source declares groupSearchRows; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:64; symbol: groupSearchRows; span SHA256: d4b0406901a8696c60e7e194e296edc23ff25057eb82bfc0a7a02cc2990a85d3.

<!-- flowdocs:paragraph:9b36041158ae4d9b75c5d4473f4ece6c -->
Declared member: source declares localized; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:25; symbol: localized; span SHA256: c410b30b39c95a67e137e66f86acc4359b5d7df234efc6fe17327a059684ed7b.

<!-- flowdocs:paragraph:6d790c34b79ea9178aef69a9f71c85f8 -->
Declared member: source declares searchCountLabel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:103; symbol: searchCountLabel; span SHA256: 922ab7534653c379f89f77c46029d3a40c870ca276ec7f802ded6dd5ee6ae308.

<!-- flowdocs:paragraph:265a6b0fdcb36e0a38ae52d26c5cfa0e -->
Declared member: source declares searchPagePath; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:97; symbol: searchPagePath; span SHA256: 6091e67ef6c04ab76b0434719e4f6f382b20241309bf3e14ffc17f09aefcc7fb.

<!-- flowdocs:paragraph:a6596219674fa228a4b4e32535042f3b -->
Declared member: source declares searchRowDetail; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:59; symbol: searchRowDetail; span SHA256: 84fbd8a6a151daf71ef73529fbff052203a93cef21a9debf0f5377da71373a6d.

<!-- flowdocs:paragraph:55c5789f8338c98fdc3564c6e3cead3e -->
Declared member: source declares searchRowOpenPath; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:87; symbol: searchRowOpenPath; span SHA256: 31071c4297b5e2f9a24d71066864aa7203dafb3e98a525868f59d07d2a781a3e.

<!-- flowdocs:paragraph:fb9d6964e51d4d0330982c7cc58840c2 -->
Declared member: source declares searchRowPlayPath; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:93; symbol: searchRowPlayPath; span SHA256: 31b79be61b9df2f02ec06ed487fc28f83da5f4e7e570a90e18522264178ac4c9.

<!-- flowdocs:paragraph:1e81e406b6487d96f8fd623ce77cbf6e -->
Declared member: source declares searchRowTypeLabel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/lib/searchRows\.ts:42; symbol: searchRowTypeLabel; span SHA256: fa6890f66b9de733bc19030354c42079af3280eddd3fdc5b41b9a462433d5e05.

<!-- flowdocs:paragraph:2cbccd52a57097b8f343d3b515abd04f -->
Declared member: source declares MODE\_LABEL; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/SearchPage\.tsx:24; symbol: MODE\_LABEL; span SHA256: f2be0306bddc4a777dce339044aa1cb9ba361da99d93a2b375ee6e193da0dfca.

<!-- flowdocs:paragraph:7e1a0ae9630ab4e33dbf29a311bd992d -->
Declared member: source declares SearchEmpty; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/SearchPage\.tsx:196; symbol: SearchEmpty; span SHA256: 849b6c68ae320f0e61c057aa7371dccf3167a460234dffc30077605528e309cb.

<!-- flowdocs:paragraph:a0e8065d562bc294487a64c59ee54fb4 -->
Declared member: source declares CLIENT\_PREFS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:362; symbol: CLIENT\_PREFS\_QUERY; span SHA256: 9bacd1650b0a3e712344a1d217c82eeb1787add2a7fe4dac99cd39090e64fe99.

<!-- flowdocs:paragraph:f935abeb276c97d995fe4130265051fa -->
Declared member: source declares SEMANTIC\_CAPABILITY\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:240; symbol: SEMANTIC\_CAPABILITY\_QUERY; span SHA256: 99c42887cee05db04d37a2049f6a51e1b2beded123fcff3310e06a637ca607ec.

<!-- flowdocs:paragraph:e452539ac1229754f61f6cb59cd1c2a6 -->
Declared member: source declares fetchClientPrefs; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:365; symbol: fetchClientPrefs; span SHA256: b1c6c568bae96333d435a5619a12f1cb5955c51390dbafc6e6a29c4e8e42bd5a.

<!-- flowdocs:paragraph:b7ab74fdbc6be606d4967d64c40f5baf -->
Declared member: source declares fetchSemanticCapability; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/viewer\.ts:248; symbol: fetchSemanticCapability; span SHA256: 8b38ef9e8ecb0e36660ae65f4c0ed74e5e40f4b20b306f35cf411e1f7eeb522f.

<!-- flowdocs:paragraph:bb32e7fc3052c9d1c1ee32fd32d96701 -->
Declared member: source declares clear; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/SearchPage\.tsx:80; symbol: clear; span SHA256: e21e1e1cbec41def7db16b0e3f8829f446aeb1c1d4a88f8f371a9c2d055e5e6a.

<!-- flowdocs:paragraph:b14a67179c83ad10d4f9f2e22dd6cd28 -->
Declared member: source declares queue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/SearchPage\.tsx:90; symbol: queue; span SHA256: 351f3bcf634ee5894a43a17f048f6fbdaacf3c52566e8558e3e4039189a63c23.

<!-- flowdocs:paragraph:ff3de23f4a244a8af7e1c95b8f1c4982 -->
Declared member: source declares setQuery; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/viewer/pages/SearchPage\.tsx:75; symbol: setQuery; span SHA256: bd252e89994ccf5d61d32fbc14c516bbb01fe39368774982c4a5311f42c920ee.

