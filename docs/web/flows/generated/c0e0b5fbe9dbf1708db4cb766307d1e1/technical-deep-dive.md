# Manage operations: jobs, schedules, folder watcher — technical-deep-dive

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:c1de3dce91b7f396971bee242a7ee11a -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:84; symbol: formatDateTime; span SHA256: 4c4f22aad3de466b51cd4fb27a8e47bb30b49fd73e9f5433d3cc6725e95cc64a.

<!-- flowdocs:paragraph:c7343d1e7a0e044fb2694915ec0501a0 -->
Declared member: source declares formatDateTime; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:84; symbol: formatDateTime; span SHA256: 4c4f22aad3de466b51cd4fb27a8e47bb30b49fd73e9f5433d3cc6725e95cc64a.

<!-- flowdocs:paragraph:cad45495d7acbe067081a6f456543207 -->
Declared member: source declares renderTrigger; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:30; symbol: renderTrigger; span SHA256: b6ab8b3228fc0c5159561e879f52fd413548a721ca93b7e202f2466c63870fc8.

<!-- flowdocs:paragraph:61a56d7f6b4a9917ca3fe77ac84edc63 -->
Declared member: source declares triggerFieldsFrom; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:57; symbol: triggerFieldsFrom; span SHA256: 3afd703d8f215cff22fcd3e1c7ecdd390a185268dd358fc41fa306556f376533.

<!-- flowdocs:paragraph:2c6610fed111dc6c22e18ac4f81ac22c -->
Declared member: source declares triggerInput; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:68; symbol: triggerInput; span SHA256: 37b860d4ecf4497f6cbba70d7c4b92cafc7818db60130e2c86833c3e8f755bdd.

<!-- flowdocs:paragraph:68d1180e695c6aeb0dfdec7d81d182a8 -->
Declared member: source declares zeroPad; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:22; symbol: zeroPad; span SHA256: a8bbfeee1f4710747e95ff2b83e4fa2c5445fbe3f86c41025ba32f6446dc1bfd.

<!-- flowdocs:paragraph:876960edeeedaab105301c516e9af652 -->
Declared member: source declares JobsSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:64; symbol: JobsSection; span SHA256: d115794e3b9924d222392d69d46022092879b8691cef2d3e75560e0c3f7c0fd1.

<!-- flowdocs:paragraph:759f2ea3523bd568d13686b87d177aae -->
Declared member: source declares NumberField; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:331; symbol: NumberField; span SHA256: 88d6a238a478acba8b79a0db8581b527d0018b441b6f2f54887945ddbb8fc53e.

<!-- flowdocs:paragraph:4a7226e73feb0b98e7d9f3c98870e964 -->
Declared member: source declares ScheduleRow; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:206; symbol: ScheduleRow; span SHA256: 23e7b88c49da99f9721251f0b587c4849e586b6629af474efa3a032ab443dacf.

<!-- flowdocs:paragraph:834af99aadffbd87c233776957b3aa28 -->
Declared member: source declares SchedulesSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:133; symbol: SchedulesSection; span SHA256: cb70d11a533afb5c1ea066e2025d18de18c97c3f6ca9a26a9e4333d6564b5ba3.

<!-- flowdocs:paragraph:1af7f34578772fd659c0a6473d7f3e26 -->
Declared member: source declares WatcherSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:358; symbol: WatcherSection; span SHA256: 01e47cc31f66add0f22d0bd27040b8156893bcef87029122eaa9ab4b8021418d.

<!-- flowdocs:paragraph:e159e0fae40c2e1b276fd3bac366904f -->
Declared member: source declares fetchJobs; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:290; symbol: fetchJobs; span SHA256: 84981ee21f1139b8d18ea75b74fdb61f6a726342170062d90fb00fab530ce0f4.

<!-- flowdocs:paragraph:eff96eed8c5f9b7458ab3c4f9cfba99e -->
Declared member: source declares fetchSchedules; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:304; symbol: fetchSchedules; span SHA256: 8f7f91a66ac2d98c6751f6875c06b0f39cdc5028ab4c01cd592b0bc1231b763c.

<!-- flowdocs:paragraph:e7f689cfa116353efbae6254350e8da3 -->
Declared member: source declares fetchWatcherStatus; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:1500; symbol: fetchWatcherStatus; span SHA256: b0a721ef9635281e55cb77bab61686995e42b230b9b7f0ef0d767d01e8816162.

<!-- flowdocs:paragraph:22cdae94af277062cdd51e3d3735d7d9 -->
Declared member: source declares setScheduleEnabled; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:313; symbol: setScheduleEnabled; span SHA256: 7511c93a628d542a33daf52b07545303b47030f036b30a37a5c231fb97450591.

<!-- flowdocs:paragraph:c79796b01ac4625a5b8c9e55a66d5e03 -->
Declared member: source declares setScheduleTrigger; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:328; symbol: setScheduleTrigger; span SHA256: e3da2088ab56a0ce5aec50952ec7fb740e7caeac5f97cb963c1e813b5efa01f7.

<!-- flowdocs:paragraph:8d3e7355a03f7df5436fed18b2e6c21c -->
Declared member: source declares onToggle; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/OperationsPage\.tsx:151; symbol: onToggle; span SHA256: b33265846093e8829ec114495ee710a728fd91273644f686468b753584b2619c.

