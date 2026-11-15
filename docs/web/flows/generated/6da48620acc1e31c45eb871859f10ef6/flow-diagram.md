# Console access: library grants, rating ceilings, access schedules, managed policy — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:3d4e315da87c0018723e32b2ddead748 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/lib/schedulePreview\.ts:9; symbol: AccessWindow; span SHA256: 1ca7681e58d4347f95ebf7158b8a82b6a9aa8527b4cf756c5ff3bea726162698.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6da48620acc1e31c45eb871859f10ef6
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/lib/schedulePreview.ts at line 82 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/console/lib/schedulePreview.ts at line 29 - order unresolved
    Note over S,U: Source contains call weekMinute in internal/web/app/src/areas/console/lib/schedulePreview.ts at line 30 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 557 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 558 - order unresolved
    Note over S,U: Source contains call accessGrantsForProfile in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 559 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 270 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 271 - order unresolved
    Note over S,U: Source contains call fetchAccessSchedule in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 272 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 233 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 234 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 166 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 167 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 169 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 412 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 413 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 414 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 415 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 418 - order unresolved
    Note over S,U: Source contains call setSeenRemoteMaxBitrate in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 420 - order unresolved
    Note over S,U: Source contains call setStoredRemoteCap in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 421 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 424 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 426 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 427 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 428 - order unresolved
    Note over S,U: Source contains call useShowAdvanced in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 429 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 61 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 62 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 63 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 65 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 68 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 70 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 86 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 88 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 90 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 91 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 94 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 109 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 119 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 134 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 140 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 143 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 373 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 375 - order unresolved
    Note over S,U: Source contains call nextTransition in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 376 - order unresolved
    Note over S,U: Source contains call getDay in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 376 - order unresolved
    Note over S,U: Source contains call getHours in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 376 - order unresolved
    Note over S,U: Source contains call getMinutes in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 376 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 379 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 382 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 383 - order unresolved
    Note over S,U: Source contains call formatMinute in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 384 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 388 - order unresolved
    Note over S,U: Source contains call update in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 282 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 432 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 433 - order unresolved
    Note over S,U: Source contains call setCapApplied in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 434 - order unresolved
    Note over S,U: Source contains call setProfileStreamCap in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 436 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 436 - order unresolved
    Note over S,U: Source contains call setCapApplied in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 437 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 439 - order unresolved
    Note over S,U: Source contains call update in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 290 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 290 - order unresolved
    Note over S,U: Source contains call setCertificationCeiling in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 238 - order unresolved
    Note over S,U: Source contains call onChanged in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 239 - order unresolved
    Note over S,U: Source contains call update in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 286 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 286 - order unresolved
    Note over S,U: Source contains call setRevoking in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 565 - order unresolved
    Note over S,U: Source contains call revokeAccessGrant in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 567 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 568 - order unresolved
    Note over S,U: Source contains call setRevoking in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 570 - order unresolved
    Note over S,U: Source contains call setSelected in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 172 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 173 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 173 - order unresolved
    Note over S,U: Source contains call setPreauth in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 464 - order unresolved
    Note over S,U: Source contains call setExtensionPreauthorization in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 465 - order unresolved
    Note over S,U: Source contains call setData in internal/web/app/src/areas/console/sections/ProfilesAccess.tsx at line 278 - order unresolved
```
