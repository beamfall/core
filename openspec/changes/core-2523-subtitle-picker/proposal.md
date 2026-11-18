# Subtitle picker labels, flags and order

Ticket: CORE-2523 (`queue:core:main`).

## Why

Tower QA on a TV episode with 42 muxed subtitle tracks found the Media detail page's Subtitles picker
lists the tracks flat in file order, with several tracks labelled only "English · Track 2" and no
SDH, forced or region marks. The file names those differences in each stream's `title` tag
("English (SDH)", "Forced") or only in the language tag's region or script (`es-419`,
`zh-Hant`), and the scan does not read the title. Plex and Infuse show those labels and group the list by language.

## What changes

- The probe reads SDH/CC, forced and commentary words in a subtitle stream's container `title` tag
  into the existing flags when the stream's dispositions do not set them ("non-SDH" and "Not
  Forced" set nothing). The title itself is not stored or sent: no migration and no wire change.
  Files probed before keep their flags until a rescan.
- Sidecar filename tags skip numeric and non-language segments and accept `_` as a region
  separator (`movie.pt_BR.srt`).
- The web Subtitles picker and the player's caption menu label a track as language name with region
  or script ("Spanish (Latin America)", "Chinese (Traditional)") plus its flags ("English · SDH",
  "English · Forced"), add format and source only where they tell same-label tracks apart, and add
  a number only when two tracks are otherwise identical.
- Tracks sort with the profile's subtitle languages first (the display locale's language when the
  profile has none), then by language name; each language's tracks stay together (a regional preference
  keeps the language's other regions with it), forced first, then regular, then SDH. Auto and Off stay at the top.

## Non-goals

- Exposing the free-form stream title on the wire ("Signs & Songs"); that is a separate wire minor.
- Native clients (follow-up per client; the flags they need are already on the wire).
- Re-probing existing files: title-derived flags appear after the next rescan of a file.

## Failure modes

- A track with no language sorts after every language, in file order.
- An unknown language tag shows the tag itself.
