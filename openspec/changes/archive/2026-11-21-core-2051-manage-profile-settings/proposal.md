# Add Profile settings to Manage

CORE-2051

## Why

The web picker mentions setting a Profile PIN, and Manage currently offers only the PIN that
unlocks restricted content. The server can set an adult Profile's entry PIN, update preferred
subtitle languages, and change whether converting is allowed during playback.

## What changes

- Add Profile settings to Manage's Switch profile screen behind the owner Admin Session.
- Use the existing Profile roster to show whether each adult Profile has an entry PIN. Set or replace
  it using the existing `updateProfile` mutation.
- Use `currentSession` to display the current Profile's ordered subtitle languages. Save or clear
  the list with `updateProfile`.
- Explain the new controls in the README. Keep the playback error accurate about converting.

## API limits

The existing reads expose subtitle languages only for the current Profile. They expose no Profile's
converting permission. Editing other Profiles' subtitle languages and exposing a converting control
with readback require server changes outside CORE-2051.
