# Profile customization: picture, colour and picker order

Tickets: CORE-1240 (picture), CORE-1241 (icon and colour), CORE-2477 (order) on `queue:core:main`.
Client follow-ups: CORE-2478 (Apple) and CORE-2479 (Android).

## Why

The profile setup screens only let a household rename a Profile and pick its kind. The owner
wants to give each Profile a colour, choose the order Profiles appear in on every device, and
upload a picture that every device shows in place of the initial.

## What changes

- **Order (CORE-2477).** Profiles carry a manager-chosen `sort_order` (migration 0122; existing
  Profiles keep their name order). `reorderProfiles(profileIDs)` needs an Admin Session and the
  full set of live Profiles. Every Profile listing, the pickers included, follows it.
- **Icon and colour (CORE-1241).** `setProfileAvatar(profileID, avatar)` lets an Admin Session
  set another Profile's avatar token. It reuses the existing token and tone palette, so clients
  need no new colour model.
- **Picture (CORE-1240).** `POST /api/profiles/picture` (multipart `profileID` and `file`,
  16 MiB cap) accepts JPEG or PNG. The server applies EXIF orientation, crops the centre square,
  resizes to at most 512 px, flattens transparency onto a dark backdrop and re-encodes a JPEG
  with no metadata. `GET /api/profiles/picture/{id}` serves it with an ETag and, on the versioned
  URL, an immutable cache. `DELETE` removes it. A Profile may change its own picture; changing
  another Profile's needs an Admin Session. `ProfileSummary.pictureURL` carries the versioned URL,
  or is empty.
- Each change writes an Activity entry (`profile.order`, `profile.avatar`, `profile.picture`).
- Wire contract `client-core-wire` 2.27.0 adds the two mutations and `pictureURL`.
- **Web.** Console Profiles rows gain a drag grip, up and down buttons and Alt+Arrow keys, with
  an optimistic move, a screen-reader announcement and a rollback on failure. Each row's avatar
  opens a Customize dialog with the photo editor and the icon-and-colour chooser. The viewer's
  Profile settings page gets the same photo editor. The editor crops in the browser (drag, arrow
  keys, zoom) and uploads a lossless PNG; the server still normalizes it.

## Non-goals

- Native clients render `pictureURL` in CORE-2478 and CORE-2479, after this contract lands.
- No animated pictures, no GIF or HEIC input, and no per-device order.
