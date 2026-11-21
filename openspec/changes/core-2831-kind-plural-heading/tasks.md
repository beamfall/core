## 1. Server

- [x] 1.1 `RelatedEntityGroupInfo.KindPluralDisplayName` filled from the kind declaration; graph test covers a kind with and without a plural
- [x] 1.2 Pin plugin-sdk v0.4.7 and the first-party plugin releases declaring SDK range [0.4.7, 0.4.7]
- [x] 1.3 Publish wire contract 2.40.0 and update the registry pin

## 2. Web

- [x] 2.1 Entity query selects `kindPluralDisplayName`; `groupHeading` falls back plural, display name, kind
- [x] 2.2 Related group headings and the sibling switcher label use `groupHeading`; unit test
