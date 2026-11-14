# Console admin gate: locked reasons and unlock — technical-deep-dive

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:74dd4d329f220b755dd7ea86aaf6faaf -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:46; symbol: AdminGate; span SHA256: 7fec3f21e920c5b6baebe521f9c0721935304bf74d69e13ad72c3a8769272c45.

<!-- flowdocs:paragraph:4f7e4cbfc91384964766754aa49cf78b -->
Declared member: source declares AdminGateCopy; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:38; symbol: AdminGateCopy; span SHA256: 04f4361231f3a7143f3d17e421bc13ce29ccd846980d0107c76d92e9118a7886.

<!-- flowdocs:paragraph:ffeeb5cd4d368c85e967d62b8b8f76ea -->
Declared member: source declares LockedCard; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:278; symbol: LockedCard; span SHA256: 154d0a7996d337f3d348e6bf261d15be6aca8befc7fdae189377ba21f1f7359e.

<!-- flowdocs:paragraph:b6acb80c1a7799677945a4e20b43ef80 -->
Declared member: source declares LOCK\_REASON\_LEAD; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/lib/lockReason\.ts:67; symbol: LOCK\_REASON\_LEAD; span SHA256: 6beffc989626c7d600405c4bc44441a1191f21d532c3fe5adf6c3b43e4192c2f.

<!-- flowdocs:paragraph:5bc071c1d6a72fdddab75b0de6e3f514 -->
Declared member: source declares LOCK\_REASON\_TITLE; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/lib/lockReason\.ts:59; symbol: LOCK\_REASON\_TITLE; span SHA256: f876092528938aeb38b403ea891f2d9da98ed3ab47c4c58ecdda45c1557c8690.

<!-- flowdocs:paragraph:d7a9d72e8c8dbf68d79f2ebde297b835 -->
Declared member: source declares LockInput; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/lib/lockReason\.ts:14; symbol: LockInput; span SHA256: 06016b461f1f623f1c5aaff0e5159666f2980c89b28d646acf79207e7a1e5b58.

<!-- flowdocs:paragraph:8401acd68dfa6fb07c40e2e2fc14e800 -->
Declared member: source declares LockReason; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/lib/lockReason\.ts:8; symbol: LockReason; span SHA256: 73018f3ebd72685fdb2e3fc8892cb79e87adf07a15f895e442d23420646a1ad9.

<!-- flowdocs:paragraph:8ad3248c91ffca5de631d21872f09023 -->
Declared member: source declares lockReason; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/lib/lockReason\.ts:36; symbol: lockReason; span SHA256: dc5aa7eae9582619ded81737e219970a4d59f338206f0f03c5bb68f9516b4566.

<!-- flowdocs:paragraph:da675e881acc2744ac5f92dd627d0dac -->
Declared member: source declares ADMIN\_SESSION\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:37; symbol: ADMIN\_SESSION\_QUERY; span SHA256: 7637b1f7769ff3bc9442cf478380d039d662aae2984ab6fab7cfb12a3626f99c.

<!-- flowdocs:paragraph:48711a2b2899dc522f038cafaa6ff933 -->
Declared member: source declares END\_ADMIN\_SESSION\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:78; symbol: END\_ADMIN\_SESSION\_MUTATION; span SHA256: e16f9e242676d67389305fa4fef379b13b4c91d376bc5e935d74a24731b79481.

<!-- flowdocs:paragraph:be642d3a74dd3eec0dfbc594232685e0 -->
Declared member: source declares RebindOutcome; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:24; symbol: RebindOutcome; span SHA256: a13b573d7be9cfe91b8383ea226d2f958d932ac9472eeae193776b1bac15bdb1.

<!-- flowdocs:paragraph:fe91d6791d08c506419369ff9f484dc5 -->
Declared member: source declares VERIFY\_ADMIN\_SESSION\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:45; symbol: VERIFY\_ADMIN\_SESSION\_MUTATION; span SHA256: 4b84d3f9a36beddd3414c3a523f3127adfbe695c850500e34b7de5c46e9b5151.

<!-- flowdocs:paragraph:139915c1008f66d85dc82777a42d6f6c -->
Declared member: source declares VerifiedAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:49; symbol: VerifiedAdminSession; span SHA256: 05d5bb3f70c1a92614252126735d4d109a51a963ac4c1273020d4378416aa8d1.

<!-- flowdocs:paragraph:1b507dd7a968efae5756dfbe34d92851 -->
Declared member: source declares endAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:81; symbol: endAdminSession; span SHA256: 4ee3d8497d06e82a98de946da060f09c88687949557d97077c0ae75fe3f3216d.

<!-- flowdocs:paragraph:42ca20597f0587d573698f6934c1d8e3 -->
Declared member: source declares fetchAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:40; symbol: fetchAdminSession; span SHA256: f0be9cc4c6a65260b545e70f6a28e7d9bd5c6838e5f968c60379e0c713627100.

<!-- flowdocs:paragraph:b7c758d9d710f2e1c4d0274cd5efb429 -->
Declared member: source declares settleRebind; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:26; symbol: settleRebind; span SHA256: b13faca8956ab229de82053f9ca2976e5f0b4a1b28d78439f5d3ec44657139bb.

<!-- flowdocs:paragraph:e841d23b51a43a4cc966507b9ee06350 -->
Declared member: source declares verifyAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:63; symbol: verifyAdminSession; span SHA256: f4599ac1ba9ab422871325940cd512172d342310a7c310ce60759d5045907b08.

<!-- flowdocs:paragraph:fd500bd7e8cb5c3e47875168c93e6c3e -->
Declared member: source declares beginRequest; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:95; symbol: beginRequest; span SHA256: 9d3cf248d26566fc0f6ae341ef3f981e8fe5296a8b52517a6ef9beba2157cc2e.

<!-- flowdocs:paragraph:a4888331ccca07b2f35c176d336fb6b9 -->
Declared member: source declares invalidateRequest; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:88; symbol: invalidateRequest; span SHA256: b548ce3f48cacbebf195598bd93f9a1911656ec0d6dca2b99263368fbf37c3bf.

<!-- flowdocs:paragraph:81bdd77bf362eb35b2131de6f72337a6 -->
Declared member: source declares lock; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:225; symbol: lock; span SHA256: e01bede2263f9ba16a986748ede26ae84b276779a6662c58115eeea6f755879c.

<!-- flowdocs:paragraph:954a27e183f2322a5fc8df533d0410f5 -->
Declared member: source declares onUnlock; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/AdminGate\.tsx:185; symbol: onUnlock; span SHA256: 1af32882afea6cb06eb460caef60de67db199292a98eebee2eda75a57825f43b.

