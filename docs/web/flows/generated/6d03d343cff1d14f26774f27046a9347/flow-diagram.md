# Viewer Profile settings: preferences toggles and avatar — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a97b335f727dff65e293f397bab4d272 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/preferences\.ts:71; symbol: bool; span SHA256: c70cba4d32e3c85a1f4452e3eea1cfc0c2b6a55bcca22f5723f03b8b0706fdc9.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6d03d343cff1d14f26774f27046a9347
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/lib/preferences.ts at line 72 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 42 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 50 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 51 - order unresolved
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
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 53 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/viewer/pages/ProfileSettingsPage.tsx at line 53 - order unresolved
    Note over S,U: Source contains call onSave in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 60 - order unresolved
    Note over S,U: Source contains call avatarToken in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setChoice in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 71 - order unresolved
    Note over S,U: Source contains call drain in internal/web/app/src/components/beamfall/profile/AvatarChooser.tsx at line 73 - order unresolved
```
