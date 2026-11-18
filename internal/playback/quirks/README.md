# Device quirks database (CC0-1.0)

Everything in this directory — and only this directory — is dedicated to the public domain under
[CC0-1.0](LICENSE). The Go code that consumes it remains AGPL v3 like the rest of the server.

ADR-0161 §5 binds this licence: compatibility facts about hardware are shipped with an AGPL server,
but they must not become a proprietary moat over forks. `ParseQuirkDB` refuses to load a database
whose `license` field is anything other than `CC0-1.0`, so the dedication cannot be dropped by
accident.

## What lives here

`baseline.json` is the release-embedded quirks database (ADR-0161 §1). It is `//go:embed`-ed into the
binary and parsed at `NewService`, so an air-gapped server gets the full overlay with no fetch, no
feed, and no default endpoint.

The shipped baseline carries only entries backed by evidence. ADR-0161 names failure *families*
(Dolby Vision profile 7 on profile-8-only chipsets, high-bitrate stutter, bad tone-mapping, mangled
passthrough) but no concrete client/tier/firmware facts, and ADR-0161 §2 is explicit that entries are
the output of triage on a real diagnostic bundle. Every entry is restrictive, so an invented selector
would degrade real devices that were working.

The one entry today, `chromecast-default-receiver-floor`, resolves Google's Default Media Receiver
(ADR-0194 §6) at its published floor: H.264, 1080 lines, stereo, no HDR (CORE-2563). Without a
matching selector a receiver-bound Stream Token is refused at the byte routes.

## Adding an entry

Each entry is one reviewable device fact carrying its own `note` — why it exists — and is expected to
cite the triage evidence that produced it. `internal/conformance` gates the shipped file: it must
parse under the real validator and must contain no domain vocabulary.
