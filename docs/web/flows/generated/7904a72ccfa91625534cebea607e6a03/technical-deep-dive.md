# Manage tags: tag tree, assign, bulk, trashed tags — technical-deep-dive

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e24238003c40f26313784936c7d41737 -->
Declared members are listed below; generated call observations do not establish executed behavior\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:359; symbol: AssignSection; span SHA256: ff76541b48b3a2f9b4e7cb4baf150f18989432602ae1aa6b427a1a99ae9c95a4.

<!-- flowdocs:paragraph:4232bcf2f18a20ee3994c4c548f5eb5f -->
Declared member: source declares AssignSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:359; symbol: AssignSection; span SHA256: ff76541b48b3a2f9b4e7cb4baf150f18989432602ae1aa6b427a1a99ae9c95a4.

<!-- flowdocs:paragraph:3a187a3c83ce9806c0da5b115539acb5 -->
Declared member: source declares BULK\_MODES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:46; symbol: BULK\_MODES; span SHA256: 1270d8a8e48a519f1815495894aa530dc2078b6af14280629a9545fe8d7b1ffc.

<!-- flowdocs:paragraph:8f5b8d6909495c94eede4339cc78f00f -->
Declared member: source declares BulkSection; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:500; symbol: BulkSection; span SHA256: 3c383cc682afd79b490743824e18f3a48083b6146d4cd89ca9a8473f56e35762.

<!-- flowdocs:paragraph:fab8df5c61abcceb9d28dcaae23f2c8c -->
Declared member: source declares NO\_PARENT; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:50; symbol: NO\_PARENT; span SHA256: c1618910db513a8b7a3027e9b1be137bef80fc54baecaeac73a1be927855a09b.

<!-- flowdocs:paragraph:5bc2da60d616745d567af30c09955979 -->
Declared member: source declares TARGET\_TYPES; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:45; symbol: TARGET\_TYPES; span SHA256: 07c5d17641f363468fc70f6a7a04f3580193d04bffc56fa0d9bb6bada08c99e8.

<!-- flowdocs:paragraph:94ab0f371aaea0af540c117d77882ba2 -->
Declared member: source declares TagTree; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:155; symbol: TagTree; span SHA256: 7dfdc7777dbcece2e48ae9fc8b495405b1f0ec2db65a491652b3e6cb1f81fbc6.

<!-- flowdocs:paragraph:6d7a63e0c44897f0a5394c24d70b9e9b -->
Declared member: source declares TrashedTable; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:659; symbol: TrashedTable; span SHA256: 086ccfd78f305ae8c97f706490d07dd0fb767ae26e1322db3c63937a6d48c5e6.

<!-- flowdocs:paragraph:1eba8616c9051332fd978f053559fc72 -->
Declared member: source declares targetTypeLabel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:353; symbol: targetTypeLabel; span SHA256: 035715d7a99987a18bdf3492e2151c7530c894e9907fa98dbe91f296ed02f446.

<!-- flowdocs:paragraph:d12a84eab9296ce30c89608483ad934d -->
Declared member: source declares treeOrder; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:53; symbol: treeOrder; span SHA256: ae5bc53081cfea9afb04a9b7a3f33eb2adc1e8b108950b14f40c15fe45139f44.

<!-- flowdocs:paragraph:7fd4cd1349fb6e04ba09c4470c26b1de -->
Declared member: source declares ASSIGN\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:519; symbol: ASSIGN\_TAG\_MUTATION; span SHA256: a3dad7c8044d723866be01bd3fa2bff78953a4617f4d2d91b9319a52b8a9f693.

<!-- flowdocs:paragraph:0d108b0de2f2394914cc5748170b2f18 -->
Declared member: source declares BULK\_ASSIGN\_TAGS\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:557; symbol: BULK\_ASSIGN\_TAGS\_MUTATION; span SHA256: fd73f3afb3bcd09a9face10d35f95330c3f56ec9cc636f6b5e908d2f68e037a6.

<!-- flowdocs:paragraph:078a5f2a811bcfe01bfe6c62573406a8 -->
Declared member: source declares CREATE\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:464; symbol: CREATE\_TAG\_MUTATION; span SHA256: 7413e5ec2eb384e29e07182aed425d26cb36a28b6130119bb5a0a8f687777016.

<!-- flowdocs:paragraph:bba7ea3ddad57d6ad4a0cfd834704430 -->
Declared member: source declares DELETE\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:497; symbol: DELETE\_TAG\_MUTATION; span SHA256: bd3905331df7706fb4b8e5cf14c41b414ea425fed46d48ec3e65f95e7485832f.

<!-- flowdocs:paragraph:be0e70481a3a35a219cfef8d66954dbf -->
Declared member: source declares RESTORE\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:508; symbol: RESTORE\_TAG\_MUTATION; span SHA256: 96ce3a2fddfd24fd086a8e90af26552885f86cc532b79d21cdeeaff6a19e7c7f.

<!-- flowdocs:paragraph:446e7d3a7767db165a1f34625681fdb5 -->
Declared member: source declares TAGS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:434; symbol: TAGS\_QUERY; span SHA256: 3dec0a341e76458f765fed5fdf0cce90463d240f4a9e30a9b41ed363cb082681.

<!-- flowdocs:paragraph:026aaa074c13dea9e81ee4bfa9cddbf6 -->
Declared member: source declares TAG\_ASSIGNMENTS\_QUERY; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:447; symbol: TAG\_ASSIGNMENTS\_QUERY; span SHA256: adceceb0140b4775e4fb954198685b98cdb75533f9c6d5debf9bd7ca6f41632b.

<!-- flowdocs:paragraph:e6eac2df76eda3a77f33a359a92fc65d -->
Declared member: source declares TAG\_FIELDS; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:432; symbol: TAG\_FIELDS; span SHA256: 6b3e66b139b63a3041a99cac08e903694276687d229cb796f87cdca8087fed3f.

<!-- flowdocs:paragraph:9add3f9272bcb928cf87698aa54927c0 -->
Declared member: source declares UNASSIGN\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:538; symbol: UNASSIGN\_TAG\_MUTATION; span SHA256: cf7bcb91a87434ae5c44b4dbad5e9b00cd28f26d291616b73c88a85f63c8ac05.

<!-- flowdocs:paragraph:aeb9d116554c1d85c5cffbb63c9de14a -->
Declared member: source declares UPDATE\_TAG\_MUTATION; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:478; symbol: UPDATE\_TAG\_MUTATION; span SHA256: c290931ec3f4b8c49689630738f87d1fe74956ee64a2e612d3720fccffc322c5.

<!-- flowdocs:paragraph:f279965d0b3ac0a2f1b942e6557a144f -->
Declared member: source declares assignTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:525; symbol: assignTag; span SHA256: 66ce7f8fecd70f463c49a2442a874b4a5264abc9958651b38f10d120624f0ea8.

<!-- flowdocs:paragraph:5ce0b418be5300e85b25dfd0f600cb69 -->
Declared member: source declares bulkAssignTags; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:563; symbol: bulkAssignTags; span SHA256: 3f1e8357ac1a2aadc296a1421297cf3b676c934187f56daa33f49e07c5e2c7af.

<!-- flowdocs:paragraph:f1a35540c8a14acfd35abf995e365c3a -->
Declared member: source declares createTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:470; symbol: createTag; span SHA256: 0791ea12c772306bb9bc73da97938d43133490b0fa733e6508a50e7247dbe84c.

<!-- flowdocs:paragraph:45b6aea6d108380dd03ca5e7255a38ad -->
Declared member: source declares deleteTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:501; symbol: deleteTag; span SHA256: 50000ecd8580d17d8eb66470e71ff4fc485f761aed8f99cc094cbd6c8e511d12.

<!-- flowdocs:paragraph:58d98bf6979673217471a6464102c1b9 -->
Declared member: source declares fetchTagAssignments; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:453; symbol: fetchTagAssignments; span SHA256: 5bb7b1c70ef5076c9e6c6970bf7c19af5ec1e19260f203b8bddf41bde72626d7.

<!-- flowdocs:paragraph:6c785594ce3ba4311adcb3357e854fa9 -->
Declared member: source declares fetchTags; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:440; symbol: fetchTags; span SHA256: f3cc9babf71aab1087bffb58c3186203f6e18352bbafb23e53d1c9911a160b35.

<!-- flowdocs:paragraph:da881f56fb07f816f353ece006d2941b -->
Declared member: source declares restoreTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:512; symbol: restoreTag; span SHA256: ffbeecc73cb75f52d707e125a6a514cb7d81806dcdecbdd0014263008da7a296.

<!-- flowdocs:paragraph:96d4e60b9f73f6e086dda08dc3d4c2bf -->
Declared member: source declares unassignTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:544; symbol: unassignTag; span SHA256: 9cab80c0a5387dd66af5fd7507bb471586613b440c996103f1794f244962eaa9.

<!-- flowdocs:paragraph:b648e8b46ab80146e7845996f5507d32 -->
Declared member: source declares updateTag; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/lib/graphql/manage\.ts:484; symbol: updateTag; span SHA256: 66c5ce068cf528db2c088d33ccba0336ada084bb415371fcb0d9afeacaef3073.

<!-- flowdocs:paragraph:8ec4164f2cd5d80156ec6620c39e911f -->
Declared member: source declares beginEdit; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:192; symbol: beginEdit; span SHA256: 22277a6b46fb28e274c7cd1a69274f337378a7ad5224b0a1cba3ee0674edbb94.

<!-- flowdocs:paragraph:1a3758f886694ab4d1bbbd7faa242824 -->
Declared member: source declares idList; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:508; symbol: idList; span SHA256: f47e8fea18ad593f6de7c7220d669a877d63afa530d7530c3599c77dee69aafe.

<!-- flowdocs:paragraph:971485f689efa0231d4113989c26745c -->
Declared member: source declares liveTags; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:107; symbol: liveTags; span SHA256: d9b2678e77c13572f282c5c047a09d366ff76113f4473336a563340b1a4ca1ba.

<!-- flowdocs:paragraph:a68615bbc3c60963e14978c6051aef08 -->
Declared member: source declares load; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:89; symbol: load; span SHA256: 2f47398c6e5509cbfebc0c377dd35d4a596a68219cb223c67dd0dfbaa39fc73d.

<!-- flowdocs:paragraph:31ae762262d54c734df7ce976f7ff907 -->
Declared member: source declares modeLabel; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:523; symbol: modeLabel; span SHA256: f7416a0aac4829e2ce8af996fdaff89069dc10150d22573e2bf47d6eea3c23f3.

<!-- flowdocs:paragraph:d193044ddeb5913f0a25d0b5bfe71247 -->
Declared member: source declares onApply; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:530; symbol: onApply; span SHA256: 582bb2423c7a0a7a81ba1666fd8d80677950ac4294d6aed9d539bafb315b479e.

<!-- flowdocs:paragraph:1bedb9b599b887e26c65600da345c6d9 -->
Declared member: source declares onAssign; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:377; symbol: onAssign; span SHA256: b139e9c798c239b977ca15ece6822519081938e14133a21f176b5fb1a1304a0d.

<!-- flowdocs:paragraph:c5f3d9a55f0040fcd304030ed6b47d7b -->
Declared member: source declares onCreate; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:178; symbol: onCreate; span SHA256: 8f50b122e721c92a85cf020fb3351961a35d2b8608e0a11ee372e8af46a7bc13.

<!-- flowdocs:paragraph:1f3512486928ce4cf66887248b32986c -->
Declared member: source declares onDelete; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:212; symbol: onDelete; span SHA256: 4c3e4e6e8d0a6e55123a167d4372e53e301ae46c4355b5ddc37457dcdafc7c42.

<!-- flowdocs:paragraph:d9488604e690cc5370c35819b77ded33 -->
Declared member: source declares onLoad; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:366; symbol: onLoad; span SHA256: e22ce9c47b0f569daff82a62c650be02d6d0c249c529c9ddda56d74470254742.

<!-- flowdocs:paragraph:1f0e9d0f94b16aff4adb711de8944cbe -->
Declared member: source declares onRestore; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:670; symbol: onRestore; span SHA256: f095e910dd426da0aa9dfe956ec7e2b3596ac206dcab8c9a2e060b769a6ed69d.

<!-- flowdocs:paragraph:d33268b1c6da953001f931dcbaa1fbfa -->
Declared member: source declares onSaveEdit; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:199; symbol: onSaveEdit; span SHA256: 87973e506d01a92d72eb130e44b0d18969fc26f7fb60d0a477190832a86ce6f7.

<!-- flowdocs:paragraph:3b4e5eece6ab1050a3887d859159efab -->
Declared member: source declares onUnassign; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:389; symbol: onUnassign; span SHA256: 8ad59f275d5e5b85e78911450c819557cebb63068e9925fe7e97cabb6361669a.

<!-- flowdocs:paragraph:01d1cd9ddfafa95d834e89df4d47dca6 -->
Declared member: source declares parentOptions; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:224; symbol: parentOptions; span SHA256: ab2a24fbbbdf868dc086fd869b8766ebb74ba6b8ba652323833670e635c2460a.

<!-- flowdocs:paragraph:34dc0feee97241936fdf572fd2faca84 -->
Declared member: source declares rows; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:176; symbol: rows; span SHA256: 83a2828240edd419890099bd078e5dd575a169d5f779a27b200856846c50b9d9.

<!-- flowdocs:paragraph:29fba70b8888bd54e9c5ff2dbd5779d6 -->
Declared member: source declares toggle; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:514; symbol: toggle; span SHA256: 7590df6fa8f320948edd1dc7f27747f3a73bf301468ba7908fbf7cab8c25f693.

<!-- flowdocs:paragraph:9b3beef2a1ffb19766c8aac667c44069 -->
Source contains a lexical call to delete; dispatch and effects are unresolved\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:517; symbol: ; span SHA256: 85b22b65b08bcbca944dd5babbd31c2d82dcf22e4bc4b443e4b0f266df0165eb.

<!-- flowdocs:paragraph:3ec0c463bfbd05ade8273cf70512dd55 -->
Declared member: source declares trashedTags; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:108; symbol: trashedTags; span SHA256: 779860df9c5001254f44d43b8277f215f059ae393645fec0e982e8d646b551b1.

<!-- flowdocs:paragraph:c1d30fbd86694639716853ab49eee294 -->
Declared member: source declares visit; membership is caller\-declared, not runtime evidence\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:64; symbol: visit; span SHA256: a689fcd662ae454aeaa93a62b163a9f280426fb38622513261526a81709202ab.

