# Admin Session unlock gate \(admin password\) reused by Console — technical-deep-dive

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:25b4d224ac5aa3cd6563469a65a8b480 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/enroll/AdminSessionGate\.tsx:36; symbol: AdminSessionGate; span SHA256: 1c57e2944272208cfdc6a8b847b66f625611ef933a795bad19af3022fb47da32.

<!-- flowdocs:paragraph:2aa4828a2d07bc87ee051a1626789b2f -->
Declared member: source declares endAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:76; symbol: endAdminSession; span SHA256: 4ee3d8497d06e82a98de946da060f09c88687949557d97077c0ae75fe3f3216d.

<!-- flowdocs:paragraph:d543143a692c7568c65d703976448a79 -->
Declared member: source declares fetchAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:40; symbol: fetchAdminSession; span SHA256: f0be9cc4c6a65260b545e70f6a28e7d9bd5c6838e5f968c60379e0c713627100.

<!-- flowdocs:paragraph:cec02c761d4f09c94914053a6b6f6852 -->
Declared member: source declares settleRebind; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:26; symbol: settleRebind; span SHA256: b13faca8956ab229de82053f9ca2976e5f0b4a1b28d78439f5d3ec44657139bb.

<!-- flowdocs:paragraph:64becf3273529055b2a17858c756dd22 -->
Declared member: source declares verifyAdminSession; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:59; symbol: verifyAdminSession; span SHA256: f4599ac1ba9ab422871325940cd512172d342310a7c310ce60759d5045907b08.

<!-- flowdocs:paragraph:a4a82ae91435a5934cfceb672f115151 -->
Declared member: source declares lock; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/enroll/AdminSessionGate\.tsx:78; symbol: lock; span SHA256: 06677a5cdaf2d22c49d102127a84a7af7b9f39705c9c620d78175a283c8d3abf.

<!-- flowdocs:paragraph:2d99ee946585793f823c046f3f2e5533 -->
Declared member: source declares onUnlock; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/enroll/AdminSessionGate\.tsx:88; symbol: onUnlock; span SHA256: 1af32882afea6cb06eb460caef60de67db199292a98eebee2eda75a57825f43b.

<!-- flowdocs:paragraph:0ddf713c0dd54196d666353ae6587aad -->
Declared member: source declares refresh; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/enroll/AdminSessionGate\.tsx:64; symbol: refresh; span SHA256: 909277901cb82a2cc4d8d523b1d3b2413716066348eff2836cf39ff1c128bb09.

