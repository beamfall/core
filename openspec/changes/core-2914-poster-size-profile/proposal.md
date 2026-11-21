# CORE-2914: the web poster size roams with the Profile

## Why

CORE-2893 made the poster size one app-wide choice, but it lives in the browser's localStorage
(`beamfall.posterSize`), so a viewer who picks Small on one browser or device sees Large on the
next. The owner asked for the per-Profile follow-up on 2026-11-21.

## What changes

- A pick in the poster size control is also saved to the signed-in Profile through the existing
  per-Profile client preference seam (`setClientPref`, `profile_client_prefs`) under the key
  `web.poster-size`, the same seam the sidebar's library choice uses (`web.sidebar-libraries`,
  CORE-2435). No schema, migration or server change.
- When a Profile's preferences load and carry a saved size, the web app adopts it as the browser's
  value, so the first paint keeps the last size this browser drew and every tab follows through
  the existing storage event.
- A Profile with no saved size keeps the browser's value. Nothing is written to a Profile until a
  viewer picks a size, so one Profile's browser value never lands on another Profile.
- Saves run in order and never under a later identity: a Profile switch, reveal or relock drops a
  queued save. A failed save keeps the browser's choice and stays silent.
- Phone and desktop sizes already scale separately in `chrome.css`, so one roaming choice stays
  sensible on both.

## Non-goals

- Native clients (they keep their own size settings).
- Moving a browser-only choice made before this change up to the Profile on its own: the first
  pick after this lands saves it.

## Impact

- Web: `src/components/chrome/posterSize.ts`, `AppShell.tsx`; tests in `posterSize.test.ts` and
  `e2e/poster-size.spec.ts`.
- Depends on CORE-2893 (the app-wide control), which this branch builds on.
