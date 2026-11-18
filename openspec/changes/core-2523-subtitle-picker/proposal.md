# Subtitle picker labels, flags and order

Ticket: CORE-2523 (`queue:core:main`).

## Why

Tower QA on a TV episode with 42 muxed subtitle tracks found the Media detail page's Subtitles picker
lists the tracks flat in file order, with several tracks labelled only "English · Track 2" and no
SDH, forced or region marks. The file names those differences in each stream's `title` tag
("English (SDH)", "Spanish (Latin America)", "Chinese (Traditional)"), which the scan does not
keep. Plex and Infuse show those labels and group the list by language.

## What changes

- The scan keeps a subtitle stream's container `title` tag and reads SDH/CC, forced and commentary
  words in it into the existing flags when the stream's dispositions do not set them. Two
  append-only migrations add the `title` column to `media_file_stream` in both planes; rows probed
  before stay empty until a rescan.
- Sidecar filename tags skip numeric and non-language segments and accept `_` as a region
  separator (`movie.pt_BR.srt`).
- `mediaTracks` and `playbackInfo` caption tracks carry the stream `title`. This is an additive
  client-core wire field (`title`, omitted when empty): client-core-wire minor 2.27.0.
- The web Subtitles picker and the player's caption menu label a track as language name with region
  or script ("Spanish (Latin America)", "Chinese (Traditional)") plus its flags ("English · SDH",
  "English · Forced"), use the stream title when it names more than the language, and add a number
  only when two tracks are otherwise identical.
- Tracks sort with the profile's subtitle languages first (the display locale's language when the
  profile has none), then by language name; each language's tracks stay together, forced first,
  then regular, then SDH. Auto and Off stay at the top.

## Non-goals

- Native clients (follow-up per client; they get the `title` field from the repin).
- Re-probing existing files: titles appear after the next rescan of a file.

## Failure modes

- A track with no language sorts after every language, in file order.
- A title that names only the language adds nothing to the label.
- An unknown language tag shows the tag itself.
