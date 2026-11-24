# Burn styled ASS/SSA subtitles for clients that cannot render them

Ticket: CORE-3448 (`queue:core:main`).

## Why

Anime releases carry ASS/SSA subtitles with positioned signs (`\pos`, `\move`, `\an`), per-style
fonts, outlines, karaoke and several lines on screen at once. The served web player renders them
with JASSUB, but every other client gets the track flattened to WebVTT: AVPlayer on Apple TV,
iPhone, iPad and the Mac's AVPlayer route, and Android. Signs land at the bottom of the frame as
plain text, overlap the dialogue, and karaoke loses its timing. The owner asked for positioned
anime subtitles everywhere.

## What changes

- When the server's FFmpeg has the libass `ass` filter, a selected ASS/SSA stream on a Capability
  Profile that does not declare `ass` resolves to burn-in instead of a WebVTT sidecar. The HLS
  session extracts the script and the file's font attachments into its scratch directory and
  renders them with libass, so the picture matches mpv.
- Seek restarts offset the subtitle clock, so a session that starts mid-file stays in sync.
- The encoder's confinement grants read access to the extracted script and fonts.
- A track the server would burn in is advertised with its stream index and no WebVTT URL, the
  shape a bitmap track already has, so the Apple and Android kits route it to burn-in rather than
  to the flattened overlay. A profile that declares `webvtt` (a Cast receiver, whose sender cannot
  request burn-in) keeps the flattened sidecar.
- Version negotiation ignores the subtitle selection: a selected index names a stream of the
  Version that advertised it, so a burn must not move playback to a sibling Version without it.
  This also fixes the same loss for bitmap tracks.
- Clients that declare `ass` keep rendering it themselves. No wire change: the existing
  `BurnSubtitleIndex` grant field carries the verdict.

## Trade-off

A selected ASS track on such a client now costs a video transcode instead of the CORE-1670
video-copy remux. Turning subtitles off returns to the remux.

Apple builds from before the matching BeamfallKit change see a URL-less ASS track as unavailable
until they update, because the profile carries no signal that separates them from new builds.
Apple builds need the kit change before or with this server change; a follow-up ticket tracks a
capability signal that would keep the sidecar for older builds.

## Non-goals

- Encrypted Libraries: every burn-in is refused there (ADR-0236), so ASS keeps the WebVTT sidecar.
- Client-side libass on Apple or Android (licence and App Store review are separate tickets).
- Fontconfig access for libass fallback fonts under the Linux landlock and macOS sandbox: embedded
  fonts are covered; scripts that rely on system fonts fall back to libass's default font.
