# Console access: devices and pairing codes — technical-deep-dive

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b86d0772ed233f57d577991e3f90ad09 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:57; symbol: Devices; span SHA256: 91122bd481a61a50c8979c2c0c3e5c18a73f7bcb28014fd595308c1dee7ffbf5.

<!-- flowdocs:paragraph:5dfc7490e9381b3da5aef229288a379e -->
Declared member: source declares PairingCodeBanner; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:34; symbol: PairingCodeBanner; span SHA256: ebc14cf17ced8e9523db158092eb0f3c1e1c9ba6459a4d9d043584d07044c9ce.

<!-- flowdocs:paragraph:d1d51ad8bb894be1a228a48706b6636d -->
Declared member: source declares PairingCode; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:162; symbol: PairingCode; span SHA256: b3348fdb951b9f9a2778ef5da443bd40c987d8e422ab50c29460e3ae2bfe16cc.

<!-- flowdocs:paragraph:a4006b7f59c5d393d460d89eb567ff00 -->
Declared member: source declares mintPairingCode; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/auth\.ts:172; symbol: mintPairingCode; span SHA256: 5def720378d83676b5aa475bfd7d6464c38f32797ddee8a491b13c9b6d787b80.

<!-- flowdocs:paragraph:cda7af3f244d1e4e98cafef68a8f9c33 -->
Declared member: source declares CONSOLE\_CONFIG\_FIELDS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:60; symbol: CONSOLE\_CONFIG\_FIELDS; span SHA256: b411ac10419f025e04f75e76a7d72f1e56b58a6bb29a48cfc563ac5e494d4524.

<!-- flowdocs:paragraph:0055be14473615d47e71469e2892bd80 -->
Declared member: source declares CONSOLE\_CONFIG\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:63; symbol: CONSOLE\_CONFIG\_QUERY; span SHA256: 807152d4b6fac63afa4aabcd9d0c39bf5450489c0dfe06a584dfdc9ce66b34a8.

<!-- flowdocs:paragraph:bd17cb1345fe6e9d650bdb756e55a053 -->
Declared member: source declares ConsoleConfigPatch; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:138; symbol: ConsoleConfigPatch; span SHA256: bb928d35d539663a8de47415fff2ece595fbc56665addb8b729d3ebd010264be.

<!-- flowdocs:paragraph:662ef4b4d6f315f4e66f203e4e8c9f48 -->
Declared member: source declares DEVICES\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:401; symbol: DEVICES\_QUERY; span SHA256: 2efe78749cb59a6e16b6191959a5275d16d86d9bcdd864daf177341bb9f940e8.

<!-- flowdocs:paragraph:312ca1370fd569124ad4a2cfcf913e91 -->
Declared member: source declares REVOKE\_ALL\_DEVICES\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:428; symbol: REVOKE\_ALL\_DEVICES\_MUTATION; span SHA256: 5fa095e2038fb56ad1f2e437cb0addfc02d4ea858f1b028d092a1209a046b8f6.

<!-- flowdocs:paragraph:29a38c876ffba4356d96a828ad69de17 -->
Declared member: source declares REVOKE\_DEVICE\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:409; symbol: REVOKE\_DEVICE\_MUTATION; span SHA256: 59f1db45841632fd08b6a4e2d08abe9bc49825b7cf4eee308d0f389a9337259a.

<!-- flowdocs:paragraph:961e370f7284c09eed5bc015feb46e33 -->
Declared member: source declares SET\_CONSOLE\_CONFIG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:146; symbol: SET\_CONSOLE\_CONFIG\_MUTATION; span SHA256: 96ec96c025389982e0cb99715108b2047ff92f89ee6b1840b7d19749b076c9b5.

<!-- flowdocs:paragraph:bfb0a6481e9881d2b1e59265d908ea82 -->
Declared member: source declares SET\_DEVICE\_ROLE\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:442; symbol: SET\_DEVICE\_ROLE\_MUTATION; span SHA256: 342743982823959afe4b968bb369c85e34c0b05e193c0dd16f161960f5ecf9ff.

<!-- flowdocs:paragraph:244d8079355d7d4a0a6b388f48646c4f -->
Declared member: source declares fetchConsoleConfig; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:66; symbol: fetchConsoleConfig; span SHA256: 8dd3bd806c9c85460236240555998ab733acc75debb41fac0e0ab7e545350c68.

<!-- flowdocs:paragraph:b37d106850b56329aeee04e04b976799 -->
Declared member: source declares fetchDevices; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:404; symbol: fetchDevices; span SHA256: 2cb96a1f6fdf5156d5272f0e55d47e88874ef6c042a24d3b1ad18553236c3879.

<!-- flowdocs:paragraph:d0972acc40d280803c98161707a75ba2 -->
Declared member: source declares revokeAllDevices; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:432; symbol: revokeAllDevices; span SHA256: 4a001838a63575bc60c42cf5c1fb5b6cbdc5cf7f136a5ac753f10e5b7c2a9d8d.

<!-- flowdocs:paragraph:5b75cd746dcceddf5042e724579c2d95 -->
Declared member: source declares revokeDevice; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:414; symbol: revokeDevice; span SHA256: cbe9eefbb3aad859b2bea0a0e169d185407fb4aab1130b76cd28efca2fc4d326.

<!-- flowdocs:paragraph:6f8e5f8f97014695f6a4e814274f808f -->
Declared member: source declares setConsoleConfig; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:149; symbol: setConsoleConfig; span SHA256: 0eed1af2eab202efaef46289683c7d5a1392b5f4e27278154bb1cf6e90ab4615.

<!-- flowdocs:paragraph:be15de1a28e25ee1a0136dfed851ae94 -->
Declared member: source declares setDeviceRole; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/console\.ts:446; symbol: setDeviceRole; span SHA256: 4c2d988a76b26183ef266a6b30e952c1ede100119e46d4d17305892a94ad8aec.

<!-- flowdocs:paragraph:0e49288bf1b86bfecd03dbc996703f3a -->
Declared member: source declares mint; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:64; symbol: mint; span SHA256: cd65ca0ea707495a1ea37239efd34aebf98545a050e36c2f549bc5eb4b64a737.

<!-- flowdocs:paragraph:71350a4a566d1d61c77f3d312fe65f75 -->
Declared member: source declares onRevoke; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:73; symbol: onRevoke; span SHA256: 6ffa5a943bfa7a454177b97dc794cb925246ef426a7d60d7f80a97fa9143ac1e.

<!-- flowdocs:paragraph:79f760ee5e5ac87496f1a1736c3df340 -->
Declared member: source declares onRevokeAll; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:78; symbol: onRevokeAll; span SHA256: 341805df7a7c9f3af90335011cdc4566f750b94aa398fcad7002503b4777eaf8.

<!-- flowdocs:paragraph:54e88848aa69f41f97760ffc4272b771 -->
Declared member: source declares onSetRole; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:83; symbol: onSetRole; span SHA256: 44496156b2655924478674a87fd3f5a42bf93eb287a6dc80340981295b9b7cf6.

<!-- flowdocs:paragraph:cb2e04b05dd4534ed01441ac36f2f73e -->
Declared member: source declares onToggleRemote; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:88; symbol: onToggleRemote; span SHA256: 5111050b7be5f77389af84d6028f25185d545673aa1050b1f56ad34846690caa.

