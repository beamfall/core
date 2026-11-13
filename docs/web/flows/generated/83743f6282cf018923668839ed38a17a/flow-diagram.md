# Viewer who&\#39;s\-watching Profile picker with PIN prompt — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:8dbf2b040d7e71d2cd950e4eb4ed6fe8 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/beamfall/profile/ProfileAvatar\.tsx:12; symbol: ProfileAvatar; span SHA256: 4d97af950c27a62df588bc3e465891483c94b5e26de48989d283f9d642a20f03.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 83743f6282cf018923668839ed38a17a
    Note over S,U: Source contains call resolveAvatar in internal/web/app/src/components/beamfall/profile/ProfileAvatar.tsx at line 23 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/components/beamfall/profile/ProfileAvatar.tsx at line 24 - order unresolved
    Note over S,U: Source contains call cn in internal/web/app/src/components/beamfall/profile/ProfileAvatar.tsx at line 31 - order unresolved
    Note over S,U: Source contains call profileInitial in internal/web/app/src/components/beamfall/profile/ProfileAvatar.tsx at line 33 - order unresolved
    Note over S,U: Source contains call charCodeAt in internal/web/app/src/components/beamfall/profile/avatar.ts at line 72 - order unresolved
    Note over S,U: Source contains call from in internal/web/app/src/components/beamfall/profile/avatar.ts at line 96 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/components/beamfall/profile/avatar.ts at line 96 - order unresolved
    Note over S,U: Source contains call toLocaleUpperCase in internal/web/app/src/components/beamfall/profile/avatar.ts at line 97 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/components/beamfall/profile/avatar.ts at line 79 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/components/beamfall/profile/avatar.ts at line 80 - order unresolved
    Note over S,U: Source contains call generatedTone in internal/web/app/src/components/beamfall/profile/avatar.ts at line 82 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/components/beamfall/profile/avatar.ts at line 84 - order unresolved
    Note over S,U: Source contains call generatedTone in internal/web/app/src/components/beamfall/profile/avatar.ts at line 85 - order unresolved
    Note over S,U: Source contains call setItem in internal/web/app/src/components/beamfall/profile/entry.ts at line 25 - order unresolved
    Note over S,U: Source contains call getItem in internal/web/app/src/components/beamfall/profile/entry.ts at line 16 - order unresolved
    Note over S,U: Source contains call markProfilePicked in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 77 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 78 - order unresolved
    Note over S,U: Source contains call destination in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 78 - order unresolved
    Note over S,U: Source contains call done in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 97 - order unresolved
    Note over S,U: Source contains call onSwitch in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 98 - order unresolved
    Note over S,U: Source contains call setFailure in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 99 - order unresolved
    Note over S,U: Source contains call setPinProfileID in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call setFailure in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 82 - order unresolved
    Note over S,U: Source contains call setBusyID in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 83 - order unresolved
    Note over S,U: Source contains call switchProfile in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 85 - order unresolved
    Note over S,U: Source contains call done in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 86 - order unresolved
    Note over S,U: Source contains call failureKind in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 88 - order unresolved
    Note over S,U: Source contains call setFailure in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 91 - order unresolved
    Note over S,U: Source contains call setBusyID in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 92 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 213 - order unresolved
    Note over S,U: Source contains call onSubmit in internal/web/app/src/areas/viewer/pages/ProfilePickerPage.tsx at line 215 - order unresolved
```
