# Manage trash: restore, purge and reversible activity — technical-deep-dive

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:17c9749ef4e9f802099d4b5d6a99c8c1 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:259; symbol: ActivitySection; span SHA256: 3e919863e35f12ddc418f5cf97c9e47dd440cd05819cdb3dc85ab4dea03403d4.

<!-- flowdocs:paragraph:b3c3ed2b35925653fa14389834d538f7 -->
Declared member: source declares ActivitySection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:259; symbol: ActivitySection; span SHA256: 3e919863e35f12ddc418f5cf97c9e47dd440cd05819cdb3dc85ab4dea03403d4.

<!-- flowdocs:paragraph:c8430e007a594389524e6d1a2185c385 -->
Declared member: source declares PurgeSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:159; symbol: PurgeSection; span SHA256: 460803bbad5407e93b0571e9fecea61b9d14ac1b969e86e6c73beb173aecea89.

<!-- flowdocs:paragraph:c9f5c13243403bae850dd7d55fc914de -->
Declared member: source declares RETAINED\_TRASH\_ANCHOR; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:47; symbol: RETAINED\_TRASH\_ANCHOR; span SHA256: 7505e1df5dbbc85aad1fc4b436942166531d4aef68bd57cf74ab686c8d851139.

<!-- flowdocs:paragraph:556774117a7dd984f876a9ddb6b09d24 -->
Declared member: source declares REVERSIBLE\_ACTIONS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:46; symbol: REVERSIBLE\_ACTIONS; span SHA256: 324089c18c47d9efee38c22afc16d9c9d0906ebbeb6bd60e3eef4727a020e347.

<!-- flowdocs:paragraph:87337d56f78850332ace5460c3302db2 -->
Declared member: source declares ACTIVITY\_LOG\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:719; symbol: ACTIVITY\_LOG\_QUERY; span SHA256: 43c011cc1803f3c7444e87a8b0648774414a8641ab8063ce65c1c523cc6a36cb.

<!-- flowdocs:paragraph:e7e72654fea9ac35686a9b0aa4c573b2 -->
Declared member: source declares fetchActivityLog; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:723; symbol: fetchActivityLog; span SHA256: 7a0b83fc8d4571e2c2cfba58455501c76ba2feb04967c6ec685995d9e196e31e.

<!-- flowdocs:paragraph:e59e6ea8da4e5abc0af293ed87e14e79 -->
Declared member: source declares PURGE\_TRASH\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:371; symbol: PURGE\_TRASH\_MUTATION; span SHA256: fe78a49205d64943b0a2e214375a349308a3ecbefda522ca9db9d88d9b77512f.

<!-- flowdocs:paragraph:aaf659664f86bf1a3949d475658d9b90 -->
Declared member: source declares RESTORE\_MEDIA\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:360; symbol: RESTORE\_MEDIA\_MUTATION; span SHA256: 495531356e0a72d6f1e7e802942c3e6861784cbb51b00611c339a5876590ea06.

<!-- flowdocs:paragraph:b6c1bd4f44494ebe46f047a344f25f0a -->
Declared member: source declares TRASH\_LIST\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:352; symbol: TRASH\_LIST\_QUERY; span SHA256: e75efa54867c96b466b420c4f3c69d833b22f10ca412db2565cb51f6af2e5178.

<!-- flowdocs:paragraph:a12ce785b4b427d45057656367ac67fe -->
Declared member: source declares fetchTrashList; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:355; symbol: fetchTrashList; span SHA256: 7358184a12a7b4ef7a7f77e5da7fddf6b0e28cdf46bb0b390782b676494055cb.

<!-- flowdocs:paragraph:e964f8deca66272421c757dab87cd6ac -->
Declared member: source declares purgeTrash; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:375; symbol: purgeTrash; span SHA256: 63a1a7737d8d45b616fadeba58caeb5e2d6642bfbe4c27674903917ab10757be.

<!-- flowdocs:paragraph:99f4ae2bd04afa4b290e84d0de91ea52 -->
Declared member: source declares restoreMedia; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:364; symbol: restoreMedia; span SHA256: cee67d6bc6fa55224522b5bb3d6b701d1dcdc28bf560d90189a42f9afbf4865e.

<!-- flowdocs:paragraph:65850ff24cb7ddbe44479729139d3118 -->
Declared member: source declares onConfirm; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:170; symbol: onConfirm; span SHA256: 6810ed5c434e60ef6fd32995dea228e67c94c66de1b7db941af508ae1acf060c.

<!-- flowdocs:paragraph:c9e1854badfd83ff45b50d89f7a8c799 -->
Declared member: source declares onRestore; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:77; symbol: onRestore; span SHA256: 08a959361fcaa0ac93584ebdcb2d3a4b276675f7cd5d1d7b61eb4823f2c8a8f9.

<!-- flowdocs:paragraph:c28f1c633da9ee365848812b3b4405c5 -->
Declared member: source declares onUndo; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:281; symbol: onUndo; span SHA256: dd6e29f8cecfaf9e545f9bd64f014bb5e27f7f5e4d38053004b166e8d6d75c9e.

<!-- flowdocs:paragraph:c3a407b79b893e7b318e01beb1003d88 -->
Declared member: source declares rows; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TrashPage\.tsx:285; symbol: rows; span SHA256: 358be536561de107d0724d036e20f697fecf5b1fae6698e40add684da1f9cba1.

