# Console operations and updates: resource governor, job queue and job log — technical-deep-dive

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:341e75d03dbf155f5f3ab1d489b68617 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:150; symbol: GovernorSettings; span SHA256: abc1dc8faca9781de3fb18b37fb9703e88f622dd772866977a585ed5dcf4a906.

<!-- flowdocs:paragraph:02de4eb5cf80c773ae13470ce4846487 -->
Declared member: source declares GovernorSettings; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:150; symbol: GovernorSettings; span SHA256: abc1dc8faca9781de3fb18b37fb9703e88f622dd772866977a585ed5dcf4a906.

<!-- flowdocs:paragraph:b4538694399c5df137f447fa3ea624c8 -->
Declared member: source declares GovernorStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:85; symbol: GovernorStatus; span SHA256: 496b00b1c25867ad7e02c4822cc68276f9d3074e914ef2b250306333f2ab0ee5.

<!-- flowdocs:paragraph:5f6bf8565662d910369e29d712249e90 -->
Declared member: source declares JobLog; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:289; symbol: JobLog; span SHA256: bda9d536ecf50fa0118b19e0d17e5b42624bcda9ce7bed8ba6924d37922148c9.

<!-- flowdocs:paragraph:704c0bc1bfe2d4402e85fe1f23d1dd29 -->
Declared member: source declares JobQueue; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:317; symbol: JobQueue; span SHA256: fa897a9748b932ec5be05fe4cae936f8a35d0958e307d96494864093ec2140a7.

<!-- flowdocs:paragraph:c63def6a20cb9f855844f0b9abd3a604 -->
Declared member: source declares StatTile; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:64; symbol: StatTile; span SHA256: 426b1aba6edcbc0f829927dfbdc36dd94a4ddd7da341256bea83b6faa10c55fb.

<!-- flowdocs:paragraph:155be4902a0beb1e0edb2c70c0c0fc4a -->
Declared member: source declares isLive; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:59; symbol: isLive; span SHA256: 8e4f963aff0bff7ec127024d8e0cd0e56ef8e522f8cce8518302e7b049e62690.

<!-- flowdocs:paragraph:f470fa19d1ada9b8f3215318b2af1eed -->
Declared member: source declares jobLogSecretPatterns; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:39; symbol: jobLogSecretPatterns; span SHA256: 95bb2830ae8a59ae8eb8dca39334b00f5b0f1883613e06653bd95c95b8523b00.

<!-- flowdocs:paragraph:2660d4c8d748e52d47326c4071663d88 -->
Declared member: source declares redactJobLogPayload; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:49; symbol: redactJobLogPayload; span SHA256: 2856873faabefe3c0696de9552a9e7cda2e6df5c7b90559c3c4ede62da08aaa4.

<!-- flowdocs:paragraph:9b0c8e88498fd0eea42c3ca84b90c75b -->
Declared member: source declares statusTone; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:34; symbol: statusTone; span SHA256: 268e63d4691ce29825a25335520600508f4aa86cfdba29bae473d22f05a7e07c.

<!-- flowdocs:paragraph:52c002bd55dc95f3bd01e5dda5346f7d -->
Declared member: source declares CONSOLE\_CONFIG\_FIELDS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:60; symbol: CONSOLE\_CONFIG\_FIELDS; span SHA256: b411ac10419f025e04f75e76a7d72f1e56b58a6bb29a48cfc563ac5e494d4524.

<!-- flowdocs:paragraph:dad9034739cc0922886930c999298000 -->
Declared member: source declares CONSOLE\_CONFIG\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:63; symbol: CONSOLE\_CONFIG\_QUERY; span SHA256: 807152d4b6fac63afa4aabcd9d0c39bf5450489c0dfe06a584dfdc9ce66b34a8.

<!-- flowdocs:paragraph:8a3e484b9bc8e87d5155f03d97e3509f -->
Declared member: source declares ConsoleConfigPatch; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:138; symbol: ConsoleConfigPatch; span SHA256: bb928d35d539663a8de47415fff2ece595fbc56665addb8b729d3ebd010264be.

<!-- flowdocs:paragraph:efcd02c8fdb8807179c236934feca6f5 -->
Declared member: source declares JOB\_CANCEL\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:248; symbol: JOB\_CANCEL\_MUTATION; span SHA256: f812e8bd8b9257ac4b623692872690faf64729bbf33f7edfe0f0776491da779a.

<!-- flowdocs:paragraph:857799a0b03cfd599e12de9ba759e8e6 -->
Declared member: source declares JOB\_EVENTS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:235; symbol: JOB\_EVENTS\_QUERY; span SHA256: aa8d53b8e13af30e798cb3eb8a0c0825f8162939af48e31a9fea20e095531ac1.

<!-- flowdocs:paragraph:8ccc5bebc20010827d54ae900954bf8d -->
Declared member: source declares JOB\_RETRY\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:257; symbol: JOB\_RETRY\_MUTATION; span SHA256: 2d02c308738e06e82a972af1acbabb50576da86a4b84f7306f3525956bd3a320.

<!-- flowdocs:paragraph:54c4314d59fbf087ef175d2fbcadcfed -->
Declared member: source declares OPERATIONS\_GOVERNOR\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:220; symbol: OPERATIONS\_GOVERNOR\_QUERY; span SHA256: fbc3abb0686e13ecbc1a6c38e13b94db9d99a1aeec4660be0081b9c29c242f1c.

<!-- flowdocs:paragraph:37d363640b01add073e667137cccd0de -->
Declared member: source declares RUN\_EVENTS\_COMPACTION\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:266; symbol: RUN\_EVENTS\_COMPACTION\_MUTATION; span SHA256: e245dec5e3d58cd0b62f75a7db61ec7f93ae761c6119b28443d02cdb8792e339.

<!-- flowdocs:paragraph:e689aa03857aa2415eb0293e30393b69 -->
Declared member: source declares SET\_CONSOLE\_CONFIG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:146; symbol: SET\_CONSOLE\_CONFIG\_MUTATION; span SHA256: 96ec96c025389982e0cb99715108b2047ff92f89ee6b1840b7d19749b076c9b5.

<!-- flowdocs:paragraph:dc0f434c66ea1d01d1dd32aef87e4ab3 -->
Declared member: source declares cancelJob; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:252; symbol: cancelJob; span SHA256: 6dad4f556b1d71d1865efc973274e5b3d0b736187b0a5738bd5c04d9dfe5f8fa.

<!-- flowdocs:paragraph:3acab659c18aca6516c3c7bbf02f852c -->
Declared member: source declares fetchConsoleConfig; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:66; symbol: fetchConsoleConfig; span SHA256: 8dd3bd806c9c85460236240555998ab733acc75debb41fac0e0ab7e545350c68.

<!-- flowdocs:paragraph:d1c3a288b200eb64a9b581a4171e3e0b -->
Declared member: source declares fetchJobEvents; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:239; symbol: fetchJobEvents; span SHA256: cf2a0bf42a25c33a9e3f9adb68ec353c1876403bbee4d129ee204cc227d16026.

<!-- flowdocs:paragraph:4b3c85c4f89421535902227b23804260 -->
Declared member: source declares fetchOperationsGovernor; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:224; symbol: fetchOperationsGovernor; span SHA256: 5e974a6b1f6ca320aa62410df8ff09220fc51a4dca4042a7af3e5326899f455f.

<!-- flowdocs:paragraph:748bf7ad7007a58ef33f2409aa91efea -->
Declared member: source declares retryJob; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:261; symbol: retryJob; span SHA256: 067af1a5c4cb1b3ab22e990b1845f78d5acabd803a947bd2bda7c56020c86525.

<!-- flowdocs:paragraph:33ccbb4f58a21d994020839b2949a6ab -->
Declared member: source declares runEventsCompaction; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:270; symbol: runEventsCompaction; span SHA256: 12123e2009ab2ad2ba14f60874d0ffe4d2fa70dab2285007103325743e1682c8.

<!-- flowdocs:paragraph:140f942ac05d352378bc2adc242fbbe1 -->
Declared member: source declares setConsoleConfig; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:149; symbol: setConsoleConfig; span SHA256: 0eed1af2eab202efaef46289683c7d5a1392b5f4e27278154bb1cf6e90ab4615.

<!-- flowdocs:paragraph:1022c9c11c7ee4ef8d1a5511e11e9cc3 -->
Declared member: source declares JOBS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:286; symbol: JOBS\_QUERY; span SHA256: 10f1f3bacf7fbd455bf418c8503ff518f957a45ee1c80502a1ffd014086a0aad.

<!-- flowdocs:paragraph:126d0dddc3a149745eac5c3e3026920b -->
Declared member: source declares fetchJobs; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:290; symbol: fetchJobs; span SHA256: 84981ee21f1139b8d18ea75b74fdb61f6a726342170062d90fb00fab530ce0f4.

