# Console API tokens and keys: scopes, create, revoke, one\-time secret — technical-deep-dive

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e219d28ebe1ffbb769e619914b209653 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:34; symbol: API\_SCOPE\_LABELS; span SHA256: ff2b726eff11658140fc63668c837d96b0b9923876a2c2de81700c20a04bfbfb.

<!-- flowdocs:paragraph:2bb8cbb0aa065b9574a3590e01c0edb6 -->
Declared member: source declares API\_SCOPE\_LABELS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:34; symbol: API\_SCOPE\_LABELS; span SHA256: ff2b726eff11658140fc63668c837d96b0b9923876a2c2de81700c20a04bfbfb.

<!-- flowdocs:paragraph:148a83d34c730a728e44454fa41bca08 -->
Declared member: source declares ApiKeys; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:164; symbol: ApiKeys; span SHA256: 20624b60cf8c1951bfc18f94235201d7aeed84b894de399acebcc989ff6b8ae1.

<!-- flowdocs:paragraph:ab022088e9a699c1dc0b8af689bdfe6f -->
Declared member: source declares ApiTokens; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:74; symbol: ApiTokens; span SHA256: deeaf3aa4ce5855b185fd7a7685ac4f392e2ef984fbd04656a7ade945b80c2b2.

<!-- flowdocs:paragraph:e341fb118f10ac44717c524e1bf085d0 -->
Declared member: source declares SCOPES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:26; symbol: SCOPES; span SHA256: b3d0143357c959b5d39127a639a46dc63359090a3e6dc66a2278f1b8f0499d71.

<!-- flowdocs:paragraph:52970b91c949eb737b600936637f867a -->
Declared member: source declares SecretBanner; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:50; symbol: SecretBanner; span SHA256: ae05286ce2122c9665914d533faddda3f58b087a8cd3a077a4f1a9cd18aa86bd.

<!-- flowdocs:paragraph:de21a9c41cf2856ecee0b686f08034f5 -->
Declared member: source declares apiScopeLabel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:42; symbol: apiScopeLabel; span SHA256: 82ae899bfdd27ffca06a52728a8ee3c7eb509e58ef1686ab4124bbf6b30f4abb.

<!-- flowdocs:paragraph:1bc7ea7333da5d2976f75338e2f2c4db -->
Declared member: source declares humanizeScope; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:36; symbol: humanizeScope; span SHA256: e14dd46aad2280927a5f64b848019121b2715173ee1c914a87ef566138363b0a.

<!-- flowdocs:paragraph:7bb0de6eea0c45a5163a6489e7058e38 -->
Declared member: source declares API\_KEYS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:674; symbol: API\_KEYS\_QUERY; span SHA256: 0fabe07c3b3eb3dc1c463bba3efb3cd31b1bbcb593fd5d35feaed4259cc61713.

<!-- flowdocs:paragraph:23eaa393f0d1493d5feeca45c3b761a2 -->
Declared member: source declares API\_TOKENS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:633; symbol: API\_TOKENS\_QUERY; span SHA256: e83075a0e7340ed40c31c7151387f69e82a75fef62f72ed09e348078fa24cfbb.

<!-- flowdocs:paragraph:2d0e7c253f95769fc91a31ae0fe46343 -->
Declared member: source declares CREATE\_API\_KEY\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:696; symbol: CREATE\_API\_KEY\_MUTATION; span SHA256: 69016f5757e36b23bad155e43c7d31db99735a2bacba47466c34a55fea928819.

<!-- flowdocs:paragraph:bf2bdd14db1855afa2722473a969aa3d -->
Declared member: source declares CREATE\_API\_TOKEN\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:654; symbol: CREATE\_API\_TOKEN\_MUTATION; span SHA256: 806c027f311f449d935fc703fa24efb5e3f6a437b2ec411539007fdb068bd6a0.

<!-- flowdocs:paragraph:e3e445f43fc8e79c45585dc0fa074e18 -->
Declared member: source declares REVOKE\_API\_KEY\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:683; symbol: REVOKE\_API\_KEY\_MUTATION; span SHA256: d07d2d4fc17beb9f30b26d353253fc6ff22e29fc8502610479b12f7b88a57f0a.

<!-- flowdocs:paragraph:0a6fb0ba3b0b1a75d7ba4cfa138db1f0 -->
Declared member: source declares REVOKE\_API\_TOKEN\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:641; symbol: REVOKE\_API\_TOKEN\_MUTATION; span SHA256: 8e4e07d774700d4b2b0743898c5a61b0518244a7ddc026884fcf1a6a48ecfbb7.

<!-- flowdocs:paragraph:7de6ab75ac4e847ecaf89ddaed9cff48 -->
Declared member: source declares createAPIKey; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:700; symbol: createAPIKey; span SHA256: 127dcf2ccd243a337084e8e140f35c0d147940e46b8d358cbb0a7bdeebcbf199.

<!-- flowdocs:paragraph:e257d50b5653548e5bcc19da56772586 -->
Declared member: source declares createAPIToken; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:658; symbol: createAPIToken; span SHA256: 62a51b29d25bb0b42273f51f426038fd1ec45356e167e5f9bd7df85f2d8926e7.

<!-- flowdocs:paragraph:0872b8bb82b065abde3975c526d2bd9c -->
Declared member: source declares fetchAPIKeys; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:678; symbol: fetchAPIKeys; span SHA256: 52a564a6bf9864b48e15f70d0c51d26b25fe40ae79f8384762d4db60312fb22a.

<!-- flowdocs:paragraph:a89e8a2503cfbb8560eeed24aad15e22 -->
Declared member: source declares fetchAPITokens; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:636; symbol: fetchAPITokens; span SHA256: d331fc7bb09898dbd8f9d3e78b4b29fbb5bca0ba0cedaa6bd7ea7d943f7a3641.

<!-- flowdocs:paragraph:84e9ee748c508d63a8e0c0ec2dcc4565 -->
Declared member: source declares revokeAPIKey; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:687; symbol: revokeAPIKey; span SHA256: 562e5d9ed105d09f6b20076547702f60cec3dead12110d533e863cd2d66cf83e.

<!-- flowdocs:paragraph:dd0c3cf9bfae6b21f844c10bfeea046c -->
Declared member: source declares revokeAPIToken; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:645; symbol: revokeAPIToken; span SHA256: 64558e4cdde127d2bbde1c993cf46dc25359c95b1a2b5bbca94bb0b7d44e91b1.

<!-- flowdocs:paragraph:2db50eff1e3abb81d8580843cdf0fc41 -->
Declared member: source declares toggleScope; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:176; symbol: toggleScope; span SHA256: 5cd4f777b1c8d65b484d7268ba1682a0614d5f1df404bedcadb9e1919b15059f.

