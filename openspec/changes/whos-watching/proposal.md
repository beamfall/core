# Open on "Who's watching?" and say how to sign in

Ticket: CORE-1196 (`queue:core:main`).

## Why

The profile picker is a list of text tiles that nobody is ever sent to, switching to an adult
Profile fails because the web app never asks for its PIN, and the signed-out page is a bare
pairing-code form that does not say where a code comes from. A household should land on a clear
"Who's watching?" screen, and someone who is signed out should be able to work out what to do next.

## What Changes

- A Profile has an avatar. It is either generated from the Profile (an initial on a colour, stable
  for that Profile on every client) or chosen by that Profile from a built-in set. The choice is
  stored as the reserved per-Profile preference `profile.avatar`, in the existing
  `profile_client_prefs` table. No migration.
- `setClientPref` validates `profile.avatar`: a short lowercase token, nothing else. Core does not
  interpret it; a client that does not recognise a token shows the generated avatar.
- The `profiles` query returns each Profile's `avatar` token (empty when none is chosen).
- The web picker becomes "Who's watching?": large avatar tiles. Choosing an adult Profile asks for
  its PIN on the same screen; a wrong PIN and a throttled attempt each get their own message.
- With more than one Profile, the web app shows the picker once when a browser tab first opens the
  viewer, then continues to where the person was going. The header shows the current Profile's
  avatar, which opens the picker in one click.
- Profile settings gets an avatar chooser.
- The signed-out page says in plain words where a pairing code comes from, and keeps offering
  admin-password sign-in when the server allows it.

Uploading a photo as an avatar is not part of this change.

## Impact

- `internal/store`: read the avatar preference for every Profile; validate the reserved key.
- `internal/graph`: `ProfileSummary.avatar`; wire-contract goldens.
- `internal/web/app`: picker, PIN prompt, entry redirect, header avatar, settings chooser, signed-out
  page, strings.
