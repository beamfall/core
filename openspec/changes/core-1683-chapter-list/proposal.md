# Chapter list with thumbnails in the players

Ticket: CORE-1683 (`queue:core:main`).

## Why

The server imports container chapters as Markers and already renders a thumbnail for each Marker
(`marker-<startMillis>.jpg` under the playback response's `assets.markerThumbnailBase`), but the
web player only offers previous/next chapter buttons and the Android TV, iPhone and macOS video
players have no chapter list at all. Only tvOS lists chapters, and only as text. Jellyfin offers a
chapter picker with images on Android TV and Swiftfin.

## What Changes

- Define which Markers are chapters for every client: Markers with an empty segment kind, point or
  range (container chapters arrive as ranges), sorted by start and de-duplicated by start time.
- The web player gains a Chapters control in the transport row that opens a list of chapters with
  thumbnail, title and start time; choosing one seeks there. The thumbnail is the Marker thumbnail
  when the server has generated it, else the trickplay tile at the chapter start, else none.
- Android TV, iPhone and macOS players gain the same list in their own repositories (this change
  records the shared behaviour; the client code lands in `beamfall-android` and `beamfall-apple`).

## Non-goals

- No server or wire change: the thumbnail URL and the Marker query already exist.
- Detail-page chapter rows and Android phone chapters (CORE-0342).
