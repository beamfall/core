# Console play history: filters, history table and rankings — technical-deep-dive

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:74a732545882e22d6672bc9041068768 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:35; symbol: ALL; span SHA256: ff90f3c4c9e00f62141a4cbb65a7eb2dc81a58af6fd26e22ce851a90921cf523.

<!-- flowdocs:paragraph:cf868655e425101f28697285c29758b7 -->
Declared member: source declares ALL; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:35; symbol: ALL; span SHA256: ff90f3c4c9e00f62141a4cbb65a7eb2dc81a58af6fd26e22ce851a90921cf523.

<!-- flowdocs:paragraph:434bee0d47ad3b89b92ee1a26c954e52 -->
Declared member: source declares HistoryTable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:56; symbol: HistoryTable; span SHA256: 3d5a6b53ad59aeeb5bfc6ec442ac4a9bce0f98d8a2a4379ed47214ea812a1516.

<!-- flowdocs:paragraph:6a27584ce1d04c3e9a3fdfa790270a64 -->
Declared member: source declares Rank; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:181; symbol: Rank; span SHA256: d5b20b0236726a5d60f827a88a0ea062db1ddaf51308cd0ab5829b963363fc24.

<!-- flowdocs:paragraph:1ac584a2c61cc41093b837c017ea8dc2 -->
Declared member: source declares RankList; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:190; symbol: RankList; span SHA256: 3cb4e1cf343b786be3d8b2a7ace021c7673bad8a99d46f3a39d0f9baca57db47.

<!-- flowdocs:paragraph:3da495f77ba17b7da23b348ceeffed51 -->
Declared member: source declares Rankings; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:214; symbol: Rankings; span SHA256: 4938eae624dd5ed3c82440f2b915dd461314fa60f3ab8f0c8a9164bf48d91f43.

<!-- flowdocs:paragraph:44d02b8a15e512a6a7760ac33f463f52 -->
Declared member: source declares WINDOWS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:36; symbol: WINDOWS; span SHA256: f2cc3e962ddb97ffb3598f31cadfaf30ba3e06918e5bcdfe473ce57adf126af5.

<!-- flowdocs:paragraph:f68490bdf494b2e10b4bcde35be06484 -->
Declared member: source declares WindowDays; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:37; symbol: WindowDays; span SHA256: 4217b880841a1d2742b2d19abd41e1cb90566c1e88e53dc16b6a8dd675697b9d.

<!-- flowdocs:paragraph:47e07f67190a40dc1922e34a2f00e7e3 -->
Declared member: source declares dayStart; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:50; symbol: dayStart; span SHA256: 925a56d13b153752bf8f523d79fec8c9f93c6b94da7edf3e153b199c0b7fdf30.

<!-- flowdocs:paragraph:466009035fbc79fd3a12db4551ace00a -->
Declared member: source declares watched; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:40; symbol: watched; span SHA256: f2b47cbf7b9c2ea5b1b235c20e9e843086736f0c034bab39690184549021b2b9.

<!-- flowdocs:paragraph:a79460167f74ec6c26dacb74c50f5bf8 -->
Declared member: source declares PLAY\_HISTORY\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:344; symbol: PLAY\_HISTORY\_QUERY; span SHA256: 0021d1122d24eb4af0dbe27fa379158834ec4fe2dc2c42de867eee8a34a16830.

<!-- flowdocs:paragraph:b4f39be70bdf45677a98be4f7904da14 -->
Declared member: source declares PLAY\_STATS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:366; symbol: PLAY\_STATS\_QUERY; span SHA256: 7a243d24a412f129f3cf1d143d20e711fed5ff6e0249be95e5ac5fe4713ca5b7.

<!-- flowdocs:paragraph:c32fa072da1c40a681a4312465e53def -->
Declared member: source declares PROFILES\_ADMIN\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:738; symbol: PROFILES\_ADMIN\_QUERY; span SHA256: f81461f06935721b01e01e88c898fdf527fb268cfc0ae778a3d6072847f6aeba.

<!-- flowdocs:paragraph:f9b66af8a2b854a73e91279eb850119e -->
Declared member: source declares fetchPlayHistory; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:348; symbol: fetchPlayHistory; span SHA256: 1bf1733f904c3cd7076aa4d074aeef6b0b60ea31a49bfe117b2d6e73606e043a.

<!-- flowdocs:paragraph:b651aab3a994f8e3d751449d0fafbaac -->
Declared member: source declares fetchPlayStats; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:370; symbol: fetchPlayStats; span SHA256: 0c61aaec9c4fee35049e7da139cc483d4651d814b1603cee1fa4cd2c33f97ae9.

<!-- flowdocs:paragraph:3c220aa6a133d5108e1baef5fdab15c8 -->
Declared member: source declares fetchProfilesAdmin; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:746; symbol: fetchProfilesAdmin; span SHA256: 30be662031e6633e4b0e408c2b988f565f8a3a3e93f158e5aa170a57abe0b1a1.

<!-- flowdocs:paragraph:a4bea20cfe2056348bde0ceb5b36ea59 -->
Declared member: source declares LIBRARIES\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:77; symbol: LIBRARIES\_QUERY; span SHA256: 65bf15c326193e30a3a53d948eb6f5e5664d2b9bbf29e2359a95b66f56bd612e.

<!-- flowdocs:paragraph:0e199bb523e268f134fe3457eb5018aa -->
Declared member: source declares fetchLibraries; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:81; symbol: fetchLibraries; span SHA256: 0c03cc7cbf8f14c88723f102436a0d578a5823acaf69a7e777c6207282cb096e.

