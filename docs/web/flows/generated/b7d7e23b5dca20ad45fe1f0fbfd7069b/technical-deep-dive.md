# Manage markers: segment edit, bulk delete, trashed markers — technical-deep-dive

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:85b4e9c2498fdb402caaeb906be9979a -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:603; symbol: BulkDeleteConfirm; span SHA256: 06875e3212b9669e3dc36e7164f0f7193644d51ad12ab79d317d837f8da0d044.

<!-- flowdocs:paragraph:7023ffbfc2bb2250db03ea31c0d96767 -->
Declared member: source declares BulkDeleteConfirm; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:603; symbol: BulkDeleteConfirm; span SHA256: 06875e3212b9669e3dc36e7164f0f7193644d51ad12ab79d317d837f8da0d044.

<!-- flowdocs:paragraph:e9ef76c414e0f4882672bea5db46b772 -->
Declared member: source declares BulkSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:460; symbol: BulkSection; span SHA256: cc5fd469943ba0f38a42c647779cf00affb99187db36533449ef5aa35d3c186a.

<!-- flowdocs:paragraph:bc4c0304ea3128eb66bbce104d247900 -->
Declared member: source declares ConfirmDelete; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:429; symbol: ConfirmDelete; span SHA256: 27d76b722cf8099ce058cd0b827026bebceedac6b2bf0c8ae01acc4ce97b36c4.

<!-- flowdocs:paragraph:c3245011e72849a517159fcc403ab60d -->
Declared member: source declares MarkerRow; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:211; symbol: MarkerRow; span SHA256: f18be83652007bb46967d665bb4ecc55024232ec2f0403e5dfb73d136689e4ef.

<!-- flowdocs:paragraph:139767f9a87795ba498106571cd04a5a -->
Declared member: source declares SEGMENT\_KINDS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:58; symbol: SEGMENT\_KINDS; span SHA256: b6eddb15ecc389be311f02a7fc248eae764f606514233b667606fe539dfc1609.

<!-- flowdocs:paragraph:74eab67c50278391712c1539eed1bcea -->
Declared member: source declares TrashedSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:647; symbol: TrashedSection; span SHA256: e714e114f7d2d96d8c3d3da267f6e90b8622a80573393d59c7fbd27c04a391fa.

<!-- flowdocs:paragraph:47c4f1448d2efc7cee9086a0c6624c43 -->
Declared member: source declares blankDraft; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:61; symbol: blankDraft; span SHA256: 647158fadbe20d2589d2a6fb4b867a367cf818e4d57e072f1b91b11bbdb35d9f.

<!-- flowdocs:paragraph:3887283283ee2adafb3afd577e30742e -->
Declared member: source declares byStart; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:77; symbol: byStart; span SHA256: d79543a5fee8dd9a8f765c91325ee8a6a3a9df6b6ee7d089471109159cf49ab0.

<!-- flowdocs:paragraph:f5a9fc048e443fcdb543c152658315b6 -->
Declared member: source declares toDraft; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:65; symbol: toDraft; span SHA256: 60a71a558504a9ca7a04bb501686a495682e5580623f837c60404c8523de0f8d.

<!-- flowdocs:paragraph:536669df3c1c3b086ea53a5addea4d24 -->
Declared member: source declares CREATE\_MARKER\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:604; symbol: CREATE\_MARKER\_MUTATION; span SHA256: 5a99e9f4b1e2a095550078530befb3a0c7fb34b158342adeedbea4ff8cd80f82.

<!-- flowdocs:paragraph:2148d28a51fe2af3e94732955bf1528c -->
Declared member: source declares DELETE\_MARKER\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:632; symbol: DELETE\_MARKER\_MUTATION; span SHA256: e1ebabc33f82dc42958ccfc613d799ff3cef6128c6a64eb08827f8ccb28802ce.

<!-- flowdocs:paragraph:e60ea1a7e61d34590a0eb69b6d216a5a -->
Declared member: source declares MARKERS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:582; symbol: MARKERS\_QUERY; span SHA256: 5972ee84aa0d21ff787e140723cd89fa51f999d17244ee3e262a870769a8f604.

<!-- flowdocs:paragraph:1a115196231e885f3238423efd1d4fbc -->
Declared member: source declares MARKER\_FIELDS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:580; symbol: MARKER\_FIELDS; span SHA256: 9214c1f8be29998f3378c5481fbbbd9bb04b113376d7f5cc7e4598f2f73eed7b.

<!-- flowdocs:paragraph:a6e068cb4e6cb89c5f7f52d8e924a5ff -->
Declared member: source declares RESTORE\_MARKER\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:643; symbol: RESTORE\_MARKER\_MUTATION; span SHA256: b2a6fe8630422560fb8f6e9509e3209a26d7eb570541c71ac67d4da577a399ab.

<!-- flowdocs:paragraph:5e2ba002bb9565b6686c6f8b2c02cf41 -->
Declared member: source declares TRASHED\_MARKERS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:591; symbol: TRASHED\_MARKERS\_QUERY; span SHA256: 7ba6b3550d6697260af439d792afa363c2f89bae43808989182cca3ec71cbc20.

<!-- flowdocs:paragraph:8a6e9c5a84a04853e99b45480e5f66af -->
Declared member: source declares UPDATE\_MARKER\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:618; symbol: UPDATE\_MARKER\_MUTATION; span SHA256: b51cc2febb998c632b6c89c807793879f683d0507209c8e337532298fa0d464f.

<!-- flowdocs:paragraph:27d6726dbea2d13be4841fc376da12d6 -->
Declared member: source declares createMarker; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:610; symbol: createMarker; span SHA256: df4e18ffc0b8f17591e13413c7e6ea933ceffab31a5fb48f6d10e2f20ae2dd64.

<!-- flowdocs:paragraph:ef1c035ad16a24d035eae2bb30433296 -->
Declared member: source declares deleteMarker; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:638; symbol: deleteMarker; span SHA256: 38819f3ef35e94b2cf92074202b7429b70f1314455d7800affec8c780bc60eab.

<!-- flowdocs:paragraph:f5a3b2b9bc738592c6266ec72e556fba -->
Declared member: source declares fetchMarkers; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:586; symbol: fetchMarkers; span SHA256: 06b73eb84970e1820f64967935674c7d13dbd16412c043bc7b74662b2f100e5a.

<!-- flowdocs:paragraph:00dd4772af35e7f3f97af14b97572139 -->
Declared member: source declares fetchTrashedMarkers; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:597; symbol: fetchTrashedMarkers; span SHA256: d5400495ff48d820ad76a40a65eaf48ad2ab1c5ff0e0d24a0495361a52be76e4.

<!-- flowdocs:paragraph:1650ff6c46bdda7f0af4cc7a68bfaaf8 -->
Declared member: source declares restoreMarker; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:649; symbol: restoreMarker; span SHA256: b23143815963a972127ae90b45de907058b3cb9528dec78fc90e4bd2087bef30.

<!-- flowdocs:paragraph:371a02d42d8805f1678fcc8e5f7209dc -->
Declared member: source declares updateMarker; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:624; symbol: updateMarker; span SHA256: c803ebd2f5c37ea492602ff65ae545e4cc1879c8a67553a98f1b7ae2df5d4e58.

<!-- flowdocs:paragraph:1ed9d59215abd086bd40612af1f3c4af -->
Declared member: source declares onBulkDelete; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:502; symbol: onBulkDelete; span SHA256: 665fed7140ddaaf50d2a388e55bff83a5d6de7374b7945e044d9bbdfdb430a95.

<!-- flowdocs:paragraph:7d63d9db502277c6e81bc0ab39532a68 -->
Declared member: source declares onCreate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:114; symbol: onCreate; span SHA256: 8f50b122e721c92a85cf020fb3351961a35d2b8608e0a11ee372e8af46a7bc13.

<!-- flowdocs:paragraph:acca7cb055b8cc7bc4b0ab6bfcb18ef8 -->
Declared member: source declares onDelete; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:282; symbol: onDelete; span SHA256: 6ff8d4de84b560f6d2d8cc904b0fb0b38480fbaa86b6748d98bff2959ed8f536.

<!-- flowdocs:paragraph:29cf6997af373238132671c8ffbb8f97 -->
Declared member: source declares onLoad; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:111; symbol: onLoad; span SHA256: f8a2bc0e7f2cefedcdf000c978356afc6f5cabb419cc719c5d15b8fa6e2d4c0d.

<!-- flowdocs:paragraph:616ba622e18b7ae7095d5c08910c664f -->
Declared member: source declares onRestore; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:658; symbol: onRestore; span SHA256: 21ceb0f8da48cfee16332294e21cc955602bea1ecfe95ea21ea1ed51420c9c9e.

<!-- flowdocs:paragraph:87af1eca1cfc964f9eefa5ee0a6dad41 -->
Declared member: source declares onRetag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:484; symbol: onRetag; span SHA256: 4819542f0e11a011e8e0d43b624b3b84e095eda7fa0880bac1584515b41fcb5c.

<!-- flowdocs:paragraph:608c5ef13221d169b92713a7422a2184 -->
Declared member: source declares onSave; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:254; symbol: onSave; span SHA256: 3d64c594d55c18c8c6b1803ae3aed6c3669c98ef6ecebb13768b3a192c66bfd7.

<!-- flowdocs:paragraph:0948f032e3d18fcf4ef10ddd1bffb068 -->
Declared member: source declares onSaved; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:125; symbol: onSaved; span SHA256: 98312116b09580ea408f03316feee3b2964624d70c1fa086235ba4813393b06f.

<!-- flowdocs:paragraph:4795f6add77ece97712d737b945cfa5d -->
Declared member: source declares reload; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:112; symbol: reload; span SHA256: 8d9aa1c81c9d50d3080a6bd4a9626951e65887a4c4679894be146fa1e856a656.

<!-- flowdocs:paragraph:10c9452f589b79220016f7a26d0af59c -->
Declared member: source declares removeTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:252; symbol: removeTag; span SHA256: 0137928eadbcdaf691f3863612b8423eca202333e336f50c62563aca6d135168.

<!-- flowdocs:paragraph:86017d9d65ca9e0f2357cbe9f065c7bf -->
Declared member: source declares set; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:242; symbol: set; span SHA256: fa8ed85e594f836705b4a096bd557e2a937e9c1edfbf99dd31a38cc245f2a627.

<!-- flowdocs:paragraph:cf4a4a2cb0bb7189848b41c13bfe1b8e -->
Declared member: source declares swapWith; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:266; symbol: swapWith; span SHA256: 26e40fce5fe268a9ad421cc077e7a004652b81c25dd25e433edb1e463be2ac98.

<!-- flowdocs:paragraph:bc3b2daf736b7f4d461a93dbb147964c -->
Declared member: source declares toggleSelected; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:127; symbol: toggleSelected; span SHA256: 7dfe9e23e575f33d977d80100bdcc32e4382827d49c920ef41e64fcee023d9ae.

<!-- flowdocs:paragraph:d65b4fee43c50d3dc9f75e62bc13fe38 -->
Source contains a lexical call to delete; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:131; symbol: ; span SHA256: 83df9fb23a05afd5956669684c8530eb0a3ee035855e959ba6ec6b3e1d798a13.

