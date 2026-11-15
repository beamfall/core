# Viewer Profile settings: preferences toggles and avatar — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a97b335f727dff65e293f397bab4d272 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/preferences\.ts:71; symbol: DOWNSTREAM\_AUDIO\_DEFAULT; span SHA256: 3771a4b3ebde3d18b22a870949ce024390336f24c946805d4f2275fab532ccc4.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6d03d343cff1d14f26774f27046a9347
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/preferences.ts at line 106 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/preferences.ts at line 110 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/viewer/lib/preferences.ts at line 111 - order unresolved
    Note over S,U: Source contains call Map in internal/web/app/src/areas/viewer/lib/preferences.ts at line 165 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/lib/preferences.ts at line 165 - order unresolved
    Note over S,U: Source contains call interval in internal/web/app/src/areas/viewer/lib/preferences.ts at line 166 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 168 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 169 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 170 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 171 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 172 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 173 - order unresolved
    Note over S,U: Source contains call speed in internal/web/app/src/areas/viewer/lib/preferences.ts at line 174 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 175 - order unresolved
    Note over S,U: Source contains call interval in internal/web/app/src/areas/viewer/lib/preferences.ts at line 177 - order unresolved
    Note over S,U: Source contains call skipIntervalOverrideKey in internal/web/app/src/areas/viewer/lib/preferences.ts at line 177 - order unresolved
    Note over S,U: Source contains call downstreamAudio in internal/web/app/src/areas/viewer/lib/preferences.ts at line 179 - order unresolved
    Note over S,U: Source contains call parseStillWatchingAfter in internal/web/app/src/areas/viewer/lib/preferences.ts at line 180 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/preferences.ts at line 180 - order unresolved
    Note over S,U: Source contains call bool in internal/web/app/src/areas/viewer/lib/preferences.ts at line 181 - order unresolved
    Note over S,U: Source contains call prePlayExtras in internal/web/app/src/areas/viewer/lib/preferences.ts at line 182 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/lib/preferences.ts at line 154 - order unresolved
    Note over S,U: Source contains call skipIntervalOverrideKey in internal/web/app/src/areas/viewer/lib/preferences.ts at line 154 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/viewer/lib/preferences.ts at line 155 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/preferences.ts at line 132 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/viewer/lib/preferences.ts at line 133 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 63 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 64 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 66 - order unresolved
    Note over S,U: Source contains call useRegister in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 71 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 72 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 73 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 75 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 80 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 87 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 551 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 555 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 26 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 27 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 28 - order unresolved
    Note over S,U: Source contains call resolveAvatar in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 28 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 30 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 34 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 37 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 47 - order unresolved
    Note over S,U: Source contains call resolveAvatar in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 92 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 92 - order unresolved
    Note over S,U: Source contains call onSave in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 60 - order unresolved
    Note over S,U: Source contains call avatarToken in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 71 - order unresolved
    Note over S,U: Source contains call drain in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 73 - order unresolved
```
