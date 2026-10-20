package playback

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// baselineQuirkDB is the release-embedded quirks database: it
// ships inside the binary, so an air-gapped server gets the full overlay with
// no fetch, no feed, and no default endpoint. Everything under quirks/ is
// CC0-1.0 (see quirks/LICENSE), deliberately separate from this file's AGPL.
//
//go:embed quirks/baseline.json
var baselineQuirkDB []byte

// QuirkDB is the versioned device-quirk database. Version is an
// opaque, monotonically-authored identifier reported in diagnostics so triage
// can tell "quirk missing" from "quirk stale"; it is never parsed
// for ordering by the server. License is the database's own licence, which
// ADR-0161 §5 fixes at CC0-1.0 — compatibility facts shipped with an AGPL
// server must not become a proprietary moat over forks.
type QuirkDB struct {
	Version string       `json:"version"`
	License string       `json:"license"`
	Entries []QuirkEntry `json:"entries"`
}

// QuirkEntry is one reviewable device fact: which devices it selects, how it
// narrows the playback decision, and which client-side behavior flags it raises.
type QuirkEntry struct {
	// ID is stable, unique within the DB, and WIRE-VISIBLE via appliedQuirks,
	// so the parser enforces its shape rather than merely documenting it.
	ID       string        `json:"id"`
	Note     string        `json:"note"`
	Selector QuirkSelector `json:"selector"`
	Override QuirkOverride `json:"override,omitempty"`
	Flags    []string      `json:"flags,omitempty"`
}

// QuirkSelector describes DEVICES, never content. Matching is
// affirmative-evidence-only: every DECLARED axis must affirmatively match the
// request, and an axis the entry omits matches everything. A client value that
// is absent or unparseable never satisfies a declared axis, so an unidentified
// device is never penalised by a firmware-bounded entry.
type QuirkSelector struct {
	Client      string   `json:"client"`
	Tiers       []string `json:"tiers,omitempty"`
	FirmwareMin string   `json:"firmwareMin,omitempty"`
	FirmwareMax string   `json:"firmwareMax,omitempty"`
	// DownstreamAudio restricts the entry to a speaker path. ADR-0161 §1 admits
	// this axis ONLY for audio-passthrough facts, resolving to conservative
	// audio policy — the server cannot identify the AVR, so a downstream-audio
	// entry must not reach past audio. The parser enforces that.
	DownstreamAudio string `json:"downstreamAudio,omitempty"`
}

// QuirkOverride is the narrowing this entry imposes. Every field can only make
// the decision MORE conservative: the client's declared Capability Profile is
// never rewritten, and a deny is evaluated as a veto rather than as a list
// subtraction (which would silently WIDEN capability, because the profile gates
// treat an empty declared list as "not gated").
type QuirkOverride struct {
	DenyVideoCodecs     []string `json:"denyVideoCodecs,omitempty"`
	DenyAudioCodecs     []string `json:"denyAudioCodecs,omitempty"`
	DenyHDRFormats      []string `json:"denyHDRFormats,omitempty"`
	DenySubtitleFormats []string `json:"denySubtitleFormats,omitempty"` // how ADR-0161's "force burn-in" is expressed
	MaxHeight           int      `json:"maxHeight,omitempty"`           // 0 = no cap
	// MaxBitrate is the video maxrate ceiling in bits/s — the encoder's
	// -maxrate, NOT a total wire budget: the ladder's fixed audio/mux allowance
	// rides atop it unconstrained. 0 = no cap.
	MaxBitrate       int  `json:"maxBitrate,omitempty"`
	MaxAudioChannels int  `json:"maxAudioChannels,omitempty"` // 0 = no cap
	ForceRemux       bool `json:"forceRemux,omitempty"`       // never direct-play the source container; video may still be copied
	ForceTranscode   bool `json:"forceTranscode,omitempty"`   // absorbing: never pass the source video through

	// DenyVideoProfiles and MaxLevel are RESERVED AND UNPARSEABLE (ADR-0161's
	// 2026-07-21 self-amendment). ADR-0161 §1 names both, but the transcode
	// target's profile and level are not carried by streamtoken.Grant, so an
	// entry declaring them would gate direct-play and then be silently
	// re-emitted by the transcode that replaced it. Rather than ship an override
	// that lies, the parser REJECTS the whole database when either appears. The
	// fields exist only so that rejection can name them precisely.
	DenyVideoProfiles []string `json:"denyVideoProfiles,omitempty"`
	MaxLevel          int      `json:"maxLevel,omitempty"`
}

// behaviorFlagVocabulary is the CLOSED vocabulary of generic, server-decided
// client behavior flags playbackInfo may return. It is
// deliberately tiny: a quirk not expressible as an override or one of these
// flags is a contract gap to fix in ADR-0161/ADR-0138, never an inline client
// hack. Growing this list is a deliberate wire-contract change —
// internal/conformance pins the literal.
var behaviorFlagVocabulary = []string{
	"avoid-engine:avplayer",
	"avoid-engine:libmpv",
	"avoid-engine:media3",
	"avoid-engine:web-mse",
	"seek-restarts-session",
}

// BehaviorFlagVocabulary returns a copy of the closed behavior-flag vocabulary
// so conformance can pin the literal without reaching into the package.
func BehaviorFlagVocabulary() []string {
	return slices.Clone(behaviorFlagVocabulary)
}

// canonicalVideoCodecs is the closed set of ffprobe codec_name values a quirk
// may deny. It is pinned rather than derived because videoCodecCap matches
// case-insensitively against whatever the client declares, so it would happily
// accept "h265" at match time — the vocabulary only bites if parser and matcher
// agree on one canonical form.
var canonicalVideoCodecs = []string{"av1", "h264", "hevc", "mpeg2video", "vc1", "vp9", "vvc"}

// canonicalHDRFormats is the closed set hdrFormat can emit.
var canonicalHDRFormats = []string{
	"hdr10", "hdr10plus", "hlg",
	hdrDolbyVisionP5, hdrDolbyVisionP7, hdrDolbyVisionP8, hdrDolbyVisionGeneric,
}

// canonicalAudioCodecs is the closed set of normalised audio codec tokens a
// quirk may deny.
var canonicalAudioCodecs = []string{
	"aac", "ac3", "alac", "dts", "dts-hd-ma", "eac3", "flac", "mp3", "opus", "pcm", "truehd", "vorbis",
}

// bitmapSubtitleFormats is the closed set of bitmap subtitle families a quirk
// may deny. Text families are rejected — see stagedTextSubtitleFormats.
var bitmapSubtitleFormats = []string{"dvbsub", "pgs", "vobsub"}

// stagedTextSubtitleFormats are the text subtitle families ADR-0161's
// 2026-07-21 self-amendment stages: ffmpeg.ResolveSubtitleDelivery short-
// circuits every non-bitmap codec to a sidecar BEFORE it reads the declared
// format list, so denying one would narrow a list that is never consulted — a
// silent no-op, which the enforceable-or-unparseable rule forbids.
var stagedTextSubtitleFormats = []string{"ass", "srt", "webvtt"}

var quirkIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ParseQuirkDB decodes and VALIDATES a quirks database. It fails closed: an
// entry naming a flag outside behaviorFlagVocabulary, a duplicate entry ID, an
// empty selector Client, an inverted firmware range, or an override this build
// cannot carry through to the emitted stream is an error for the WHOLE
// database, never a silently skipped entry. A half-loaded quirks DB would make
// the playback decision depend on which entries happened to parse.
func ParseQuirkDB(data []byte) (QuirkDB, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var db QuirkDB
	if err := decoder.Decode(&db); err != nil {
		return QuirkDB{}, fmt.Errorf("quirks database: %w", err)
	}
	return validateQuirkDB(db)
}

// validateQuirkDB returns the validated, canonicalized DEEP CLONE of db. Every
// installation path runs it — ParseQuirkDB after decoding and SetQuirks instead
// of trusting its caller — so a directly-constructed database cannot bypass the
// rules the parser enforces, and the installed copy shares no slice with the
// caller.
func validateQuirkDB(db QuirkDB) (QuirkDB, error) {
	if strings.TrimSpace(db.Version) == "" {
		return QuirkDB{}, fmt.Errorf("quirks database: version is required")
	}
	if db.License != quirkDBLicense {
		return QuirkDB{}, fmt.Errorf("quirks database: license must be %q (ADR-0161 §5), got %q", quirkDBLicense, db.License)
	}
	clone := QuirkDB{Version: db.Version, License: db.License}
	seen := make(map[string]struct{}, len(db.Entries))
	for _, entry := range db.Entries {
		validated, err := validateQuirkEntry(entry)
		if err != nil {
			return QuirkDB{}, err
		}
		if _, dup := seen[validated.ID]; dup {
			return QuirkDB{}, fmt.Errorf("quirks database: entry %q is declared twice", validated.ID)
		}
		seen[validated.ID] = struct{}{}
		clone.Entries = append(clone.Entries, validated)
	}
	return clone, nil
}

const quirkDBLicense = "CC0-1.0"

func validateQuirkEntry(entry QuirkEntry) (QuirkEntry, error) {
	if !quirkIDPattern.MatchString(entry.ID) {
		return QuirkEntry{}, fmt.Errorf("quirks database: entry ID %q must match %s (it is wire-visible via appliedQuirks)", entry.ID, quirkIDPattern)
	}
	selector, err := validateQuirkSelector(entry.ID, entry.Selector)
	if err != nil {
		return QuirkEntry{}, err
	}
	override, err := validateQuirkOverride(entry.ID, entry.Override)
	if err != nil {
		return QuirkEntry{}, err
	}
	flags, err := validateQuirkFlags(entry.ID, entry.Flags)
	if err != nil {
		return QuirkEntry{}, err
	}
	if override.isEmpty() && len(flags) == 0 {
		return QuirkEntry{}, fmt.Errorf("quirks entry %q carries neither an override nor a behavior flag", entry.ID)
	}
	if selector.DownstreamAudio != "" {
		if err := checkDownstreamAudioScope(entry.ID, override, flags); err != nil {
			return QuirkEntry{}, err
		}
	}
	return QuirkEntry{ID: entry.ID, Note: entry.Note, Selector: selector, Override: override, Flags: flags}, nil
}

func validateQuirkSelector(id string, selector QuirkSelector) (QuirkSelector, error) {
	client := strings.TrimSpace(selector.Client)
	if client == "" {
		return QuirkSelector{}, fmt.Errorf("quirks entry %q has an empty selector client", id)
	}
	tiers, err := canonicalTiers(id, selector.Tiers)
	if err != nil {
		return QuirkSelector{}, err
	}
	downstream := strings.ToLower(strings.TrimSpace(selector.DownstreamAudio))
	if downstream != "" && downstream != "stereo" && downstream != "passthrough" {
		return QuirkSelector{}, fmt.Errorf("quirks entry %q selector downstreamAudio %q is outside {stereo, passthrough}", id, selector.DownstreamAudio)
	}
	if err := checkFirmwareBound(id, "firmwareMin", selector.FirmwareMin); err != nil {
		return QuirkSelector{}, err
	}
	if err := checkFirmwareBound(id, "firmwareMax", selector.FirmwareMax); err != nil {
		return QuirkSelector{}, err
	}
	if selector.FirmwareMin != "" && selector.FirmwareMax != "" {
		if cmp, ok := compareFirmware(selector.FirmwareMin, selector.FirmwareMax); ok && cmp > 0 {
			return QuirkSelector{}, fmt.Errorf("quirks entry %q selector firmwareMin %q is above firmwareMax %q", id, selector.FirmwareMin, selector.FirmwareMax)
		}
	}
	return QuirkSelector{
		Client:          client,
		Tiers:           tiers,
		FirmwareMin:     selector.FirmwareMin,
		FirmwareMax:     selector.FirmwareMax,
		DownstreamAudio: downstream,
	}, nil
}

func canonicalTiers(id string, tiers []string) ([]string, error) {
	var canonical []string
	for _, tier := range tiers {
		normalized := strings.ToLower(strings.TrimSpace(tier))
		if normalized == "" {
			return nil, fmt.Errorf("quirks entry %q declares an empty selector tier", id)
		}
		canonical = append(canonical, normalized)
	}
	return sortedSet(canonical), nil
}

func validateQuirkOverride(id string, override QuirkOverride) (QuirkOverride, error) {
	if len(override.DenyVideoProfiles) > 0 {
		return QuirkOverride{}, stagedOverrideError(id, "denyVideoProfiles")
	}
	if override.MaxLevel != 0 {
		return QuirkOverride{}, stagedOverrideError(id, "maxLevel")
	}
	if err := checkNonNegativeCap(id, "maxHeight", override.MaxHeight); err != nil {
		return QuirkOverride{}, err
	}
	if err := checkNonNegativeCap(id, "maxBitrate", override.MaxBitrate); err != nil {
		return QuirkOverride{}, err
	}
	if err := checkNonNegativeCap(id, "maxAudioChannels", override.MaxAudioChannels); err != nil {
		return QuirkOverride{}, err
	}
	if floor := bandwidthFor(h264LadderRungs[len(h264LadderRungs)-1].VideoKbps); override.MaxBitrate > 0 && override.MaxBitrate < floor {
		return QuirkOverride{}, fmt.Errorf("quirks entry %q maxBitrate %d is below the ladder floor %d — the ladder cannot honour it, so express the device with a deny or forceTranscode plus a maxHeight", id, override.MaxBitrate, floor)
	}
	video, err := canonicalDenyList(id, "denyVideoCodecs", override.DenyVideoCodecs, normalizeVideoCodec)
	if err != nil {
		return QuirkOverride{}, err
	}
	if slices.Contains(video, "h264") {
		return QuirkOverride{}, fmt.Errorf("quirks entry %q denies h264, the floor transcode target — no encode target would remain", id)
	}
	audio, err := canonicalDenyList(id, "denyAudioCodecs", override.DenyAudioCodecs, canonicalAudioCodec)
	if err != nil {
		return QuirkOverride{}, err
	}
	if slices.Contains(audio, "aac") {
		return QuirkOverride{}, fmt.Errorf("quirks entry %q denies aac, the last-resort audio target — no encode target would remain", id)
	}
	hdr, err := canonicalDenyList(id, "denyHDRFormats", override.DenyHDRFormats, canonicalHDRFormat)
	if err != nil {
		return QuirkOverride{}, err
	}
	subtitles, err := canonicalDenyList(id, "denySubtitleFormats", override.DenySubtitleFormats, canonicalSubtitleFormat)
	if err != nil {
		return QuirkOverride{}, err
	}
	return QuirkOverride{
		DenyVideoCodecs:     video,
		DenyAudioCodecs:     audio,
		DenyHDRFormats:      hdr,
		DenySubtitleFormats: subtitles,
		MaxHeight:           override.MaxHeight,
		MaxBitrate:          override.MaxBitrate,
		MaxAudioChannels:    override.MaxAudioChannels,
		ForceRemux:          override.ForceRemux,
		ForceTranscode:      override.ForceTranscode,
	}, nil
}

func checkFirmwareBound(id, label, bound string) error {
	if bound == "" {
		return nil
	}
	if _, ok := parseFirmware(bound); !ok {
		return fmt.Errorf("quirks entry %q selector %s %q is not dotted-numeric", id, label, bound)
	}
	return nil
}

func checkNonNegativeCap(id, label string, value int) error {
	if value < 0 {
		return fmt.Errorf("quirks entry %q has a negative %s (%d)", id, label, value)
	}
	return nil
}

func stagedOverrideError(id, field string) error {
	return fmt.Errorf("quirks entry %q declares %s, which ADR-0161's 2026-07-21 self-amendment stages: streamtoken.Grant carries no target profile or level, so the override could not survive the transcode that replaced direct-play. It becomes representable when the deferred grant/encoder ticket lands", id, field)
}

func canonicalDenyList(id, field string, values []string, canonical func(string) (string, bool)) ([]string, error) {
	var out []string
	for _, value := range values {
		token, ok := canonical(value)
		if !ok {
			return nil, fmt.Errorf("quirks entry %q %s names %q, which is not a token this build can match — a deny that never fires would report itself in appliedQuirks while constraining nothing", id, field, value)
		}
		out = append(out, token)
	}
	return sortedSet(out), nil
}

func validateQuirkFlags(id string, flags []string) ([]string, error) {
	var out []string
	for _, flag := range flags {
		if !slices.Contains(behaviorFlagVocabulary, flag) {
			return nil, fmt.Errorf("quirks entry %q names behavior flag %q outside the closed vocabulary %v", id, flag, behaviorFlagVocabulary)
		}
		out = append(out, flag)
	}
	return sortedSet(out), nil
}

// checkDownstreamAudioScope confines a downstream-audio-selected entry to
// conservative audio policy: the server cannot identify the AVR,
// so such an entry may carry nothing outside the two audio overrides.
func checkDownstreamAudioScope(id string, override QuirkOverride, flags []string) error {
	audioOnly := QuirkOverride{DenyAudioCodecs: override.DenyAudioCodecs, MaxAudioChannels: override.MaxAudioChannels}
	if len(flags) > 0 || !overridesEqual(override, audioOnly) {
		return fmt.Errorf("quirks entry %q selects on downstreamAudio, so it may carry only denyAudioCodecs/maxAudioChannels (ADR-0161 §1 confines that axis to conservative audio policy)", id)
	}
	return nil
}

func overridesEqual(a, b QuirkOverride) bool {
	return slices.Equal(a.DenyVideoCodecs, b.DenyVideoCodecs) &&
		slices.Equal(a.DenyAudioCodecs, b.DenyAudioCodecs) &&
		slices.Equal(a.DenyHDRFormats, b.DenyHDRFormats) &&
		slices.Equal(a.DenySubtitleFormats, b.DenySubtitleFormats) &&
		a.MaxHeight == b.MaxHeight &&
		a.MaxBitrate == b.MaxBitrate &&
		a.MaxAudioChannels == b.MaxAudioChannels &&
		a.ForceRemux == b.ForceRemux &&
		a.ForceTranscode == b.ForceTranscode
}

func (o QuirkOverride) isEmpty() bool {
	return overridesEqual(o, QuirkOverride{})
}

// normalizeVideoCodec resolves an authored video-codec token to the one
// canonical form videoCodecCap matches. Aliases are deliberately NOT mapped —
// a quirks DB is authored by triage, so "h265" is corrected at source rather
// than guessed at.
func normalizeVideoCodec(codec string) (string, bool) {
	token := strings.ToLower(strings.TrimSpace(codec))
	return token, slices.Contains(canonicalVideoCodecs, token)
}

// canonicalAudioCodec is the ONE audio canonicalizer the parser, the source
// veto, and the transcode-target exclusion all share. normalizedAudioCodec
// folds only DTS aliases (so "EAC-3" and "eac3" stay distinct) while
// hlsAudioSupportsEAC3 strips hyphens — two normalizations would let a deny
// validate, miss the source stream, AND fail to exclude the E-AC-3 target,
// re-emitting exactly the codec it denied. normalizedAudioCodec itself is left
// unchanged so the base-profile path keeps its current behaviour.
func canonicalAudioCodec(codec string) (string, bool) {
	token := normalizedAudioCodec(codec)
	switch token {
	case "eac3", "eac-3", "ec3", "ec-3", "e-ac-3":
		token = "eac3"
	case "ac-3":
		token = "ac3"
	case "dca":
		token = "dts"
	}
	return token, slices.Contains(canonicalAudioCodecs, token)
}

func canonicalHDRFormat(format string) (string, bool) {
	token := strings.ToLower(strings.TrimSpace(format))
	return token, slices.Contains(canonicalHDRFormats, token)
}

func canonicalSubtitleFormat(format string) (string, bool) {
	token := strings.ToLower(strings.TrimSpace(format))
	return token, slices.Contains(bitmapSubtitleFormats, token)
}

// parseFirmware splits a firmware string into numeric segments. EVERY segment
// must be non-empty and fully numeric, or the value is unparseable: treating a
// string we cannot understand as if we had read it would contradict the
// affirmative-evidence-only contract.
func parseFirmware(value string) ([]int, bool) {
	fields := strings.Split(strings.TrimSpace(value), ".")
	segments := make([]int, 0, len(fields))
	for _, field := range fields {
		segment, err := strconv.Atoi(field)
		if err != nil || segment < 0 {
			return nil, false
		}
		segments = append(segments, segment)
	}
	return segments, len(segments) > 0
}

// compareFirmware compares two dotted-numeric firmware strings segment-wise,
// missing trailing segments treated as 0 (so "6" and "6.0.0" compare equal).
// ok is false when either value is unparseable.
func compareFirmware(a, b string) (int, bool) {
	left, leftOK := parseFirmware(a)
	right, rightOK := parseFirmware(b)
	if !leftOK || !rightOK {
		return 0, false
	}
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := 0, 0
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l != r {
			return l - r, true
		}
	}
	return 0, true
}

// matches reports whether this entry's selector affirmatively describes the
// requesting device. Every DECLARED axis must match; an omitted axis matches
// everything.
func (e QuirkEntry) matches(profile CapabilityProfile) bool {
	if !strings.EqualFold(strings.TrimSpace(profile.Client), e.Selector.Client) {
		return false
	}
	if len(e.Selector.Tiers) > 0 {
		tier := strings.ToLower(strings.TrimSpace(profile.Tier))
		if tier == "" || !slices.Contains(e.Selector.Tiers, tier) {
			return false
		}
	}
	if e.Selector.DownstreamAudio != "" && !strings.EqualFold(e.Selector.DownstreamAudio, profile.DownstreamAudio) {
		return false
	}
	return e.Selector.firmwareMatches(profile.Firmware)
}

// firmwareMatches applies the affirmative-evidence rule to the firmware axis: a
// bounded range means "broken up to X, fixed after", so applying it to a
// firmware we could not read would degrade devices that are already fixed.
func (s QuirkSelector) firmwareMatches(firmware string) bool {
	if s.FirmwareMin == "" && s.FirmwareMax == "" {
		return true
	}
	if s.FirmwareMin != "" {
		cmp, ok := compareFirmware(firmware, s.FirmwareMin)
		if !ok || cmp < 0 {
			return false
		}
	}
	if s.FirmwareMax != "" {
		cmp, ok := compareFirmware(firmware, s.FirmwareMax)
		if !ok || cmp > 0 {
			return false
		}
	}
	return true
}

// quirkOverlay is the composed narrowing for one request. It carries every
// enforceable QuirkOverride field plus the wire-visible attribution.
type quirkOverlay struct {
	DenyVideoCodecs     []string
	DenyAudioCodecs     []string
	DenyHDRFormats      []string
	DenySubtitleFormats []string
	MaxHeight           int
	MaxBitrate          int
	MaxAudioChannels    int
	ForceRemux          bool
	ForceTranscode      bool
	// matched records affirmative resolution through at least one ADR-0161
	// selector. Receiver-bound grants carry this proof to the byte layer so an
	// unknown receiver profile fails closed.
	matched bool
	// flags are the closed-vocabulary behavior flags the matched entries raise,
	// sorted and deduplicated.
	flags []string
	// overrideIDs are the IDs of matched entries carrying a NON-EMPTY override,
	// sorted and deduplicated. A flags-only entry is deliberately absent, so
	// attribution can never blame it for a verdict it did not change.
	overrideIDs []string
}

// Resolve returns the composed overlay for one request. Entries are evaluated
// in DB order, but composition is order-independent and idempotent by
// construction: deny lists union, caps take min, the force flags OR, and flags
// union. There is no last-writer, only a meet.
func (db QuirkDB) Resolve(profile CapabilityProfile) quirkOverlay {
	var ov quirkOverlay
	for _, entry := range db.Entries {
		if !entry.matches(profile) {
			continue
		}
		ov.matched = true
		ov.absorb(entry)
	}
	ov.DenyVideoCodecs = sortedSet(ov.DenyVideoCodecs)
	ov.DenyAudioCodecs = sortedSet(ov.DenyAudioCodecs)
	ov.DenyHDRFormats = sortedSet(ov.DenyHDRFormats)
	ov.DenySubtitleFormats = sortedSet(ov.DenySubtitleFormats)
	ov.flags = sortedSet(ov.flags)
	ov.overrideIDs = sortedSet(ov.overrideIDs)
	return ov
}

func (ov *quirkOverlay) absorb(entry QuirkEntry) {
	override := entry.Override
	ov.DenyVideoCodecs = append(ov.DenyVideoCodecs, override.DenyVideoCodecs...)
	ov.DenyAudioCodecs = append(ov.DenyAudioCodecs, override.DenyAudioCodecs...)
	ov.DenyHDRFormats = append(ov.DenyHDRFormats, override.DenyHDRFormats...)
	ov.DenySubtitleFormats = append(ov.DenySubtitleFormats, override.DenySubtitleFormats...)
	ov.MaxHeight = minNonZero(ov.MaxHeight, override.MaxHeight)
	ov.MaxBitrate = minNonZero(ov.MaxBitrate, override.MaxBitrate)
	ov.MaxAudioChannels = minNonZero(ov.MaxAudioChannels, override.MaxAudioChannels)
	ov.ForceRemux = ov.ForceRemux || override.ForceRemux
	ov.ForceTranscode = ov.ForceTranscode || override.ForceTranscode
	// Emission guard (defence 2): even a DB constructed in-process can never put
	// an unknown token on the wire.
	for _, flag := range entry.Flags {
		if slices.Contains(behaviorFlagVocabulary, flag) {
			ov.flags = append(ov.flags, flag)
		}
	}
	if !override.isEmpty() {
		ov.overrideIDs = append(ov.overrideIDs, entry.ID)
	}
}

// minNonZero treats 0 as "no ceiling", so composition can only lower a ceiling,
// never raise one.
func minNonZero(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}

func sortedSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	return slices.Compact(values)
}

func (ov quirkOverlay) deniesVideoCodec(codec string) bool {
	token, ok := normalizeVideoCodec(codec)
	return ok && slices.Contains(ov.DenyVideoCodecs, token)
}

func (ov quirkOverlay) deniesAudioCodec(codec string) bool {
	token, ok := canonicalAudioCodec(codec)
	return ok && slices.Contains(ov.DenyAudioCodecs, token)
}

// vetoesVideoPassthrough reports whether the overlay forbids passing the SOURCE
// video through untouched: a denied video codec, a denied HDR format, a source
// above a quirk height/bitrate cap, evidence missing for any of those, or
// ForceTranscode.
//
// Evidence rule (unverifiable ⇒ restrict): the profile gates deliberately pass
// a fact they cannot check, which is right for a client's own declared
// envelope but wrong for a quirk — a matched entry is positive evidence that
// THIS DEVICE IS BROKEN, so a file we cannot prove safe must not be handed to
// it.
func (ov quirkOverlay) vetoesVideoPassthrough(media store.Media, file store.MediaFile, streams []store.MediaFileStream) bool {
	if ov.ForceTranscode {
		return true
	}
	if len(ov.DenyVideoCodecs) > 0 {
		codec, ok := normalizeVideoCodec(media.Codec)
		if !ok || slices.Contains(ov.DenyVideoCodecs, codec) {
			return true
		}
	}
	if ov.MaxHeight > 0 && (media.VideoHeight <= 0 || media.VideoHeight > ov.MaxHeight) {
		return true
	}
	if ov.MaxBitrate > 0 {
		bitrate := estimatedSourceBitrate(media, file)
		if bitrate <= 0 || bitrate > ov.MaxBitrate {
			return true
		}
	}
	return ov.vetoesHDR(streams)
}

// vetoesHDR fails closed only when the video stream ROW is absent. An empty
// hdrFormat on a recorded stream is affirmative evidence of SDR, and SDR cannot
// violate an HDR-format deny — treating it as missing evidence would push every
// ordinary SDR file off direct-play on any device carrying any HDR deny.
func (ov quirkOverlay) vetoesHDR(streams []store.MediaFileStream) bool {
	if len(ov.DenyHDRFormats) == 0 {
		return false
	}
	video, ok := primaryVideoStream(streams)
	if !ok {
		return true
	}
	format := hdrFormat(video)
	return format != "" && slices.Contains(ov.DenyHDRFormats, format)
}

// vetoesAudioPassthrough reports whether the overlay forbids passing the SOURCE
// audio through untouched: a denied audio codec, a stream above a quirk channel
// cap, audio evidence missing while an audio restriction is active, or
// ForceTranscode. It is a veto on AUDIO passthrough only, so on an otherwise
// playable file it selects the video-copy rung rather than a full re-encode.
func (ov quirkOverlay) vetoesAudioPassthrough(streams []store.MediaFileStream) bool {
	if ov.ForceTranscode {
		return true
	}
	if len(ov.DenyAudioCodecs) == 0 && ov.MaxAudioChannels <= 0 {
		return false
	}
	recorded := false
	for _, stream := range streams {
		if stream.Type != "audio" {
			continue
		}
		recorded = true
		codec, ok := canonicalAudioCodec(stream.Codec)
		if !ok || slices.Contains(ov.DenyAudioCodecs, codec) {
			return true
		}
		if ov.MaxAudioChannels > 0 && (stream.Channels <= 0 || stream.Channels > ov.MaxAudioChannels) {
			return true
		}
	}
	return !recorded
}

// vetoesSourceContainer reports whether the overlay forbids serving the source
// file as-is even when its streams are fine.
func (ov quirkOverlay) vetoesSourceContainer() bool {
	return ov.ForceRemux || ov.ForceTranscode
}

// vetoesRendition reports whether the overlay forbids serving this CACHED
// rendition. It judges the rendition's OWN shape — never the source's, which is
// a different encode. An override the rendition row cannot be checked against
// is grounds for refusal, not for a pass: the cached branch runs before the live
// transcode, so serving unverified would ship exactly the shape a matched quirk
// forbids. ForceRemux/ForceTranscode deliberately do NOT veto here — both are
// absorbing over SOURCE passthrough, and a cached rendition is neither the
// source nor its container.
func (ov quirkOverlay) vetoesRendition(r store.PlaybackRendition) bool {
	if len(ov.DenyHDRFormats) > 0 || len(ov.DenyAudioCodecs) > 0 || ov.MaxAudioChannels > 0 {
		return true
	}
	if len(ov.DenyVideoCodecs) > 0 {
		codec, ok := normalizeVideoCodec(r.Codec)
		if !ok || slices.Contains(ov.DenyVideoCodecs, codec) {
			return true
		}
	}
	if ov.MaxHeight > 0 && (r.Height <= 0 || r.Height > ov.MaxHeight) {
		return true
	}
	return ov.MaxBitrate > 0 && (r.Bandwidth <= 0 || r.Bandwidth > ov.MaxBitrate)
}

// subtitleFormats returns the client's declared subtitle formats minus the ones
// this overlay denies. Subtraction is monotone for the bitmap families the
// parser admits: ResolveSubtitleDelivery treats a shorter list — including an
// empty one — as LESS capable, so the result can only move a track toward
// burn-in, never away from it. It is also the only way force-burn-in can work:
// burn-in reaches the pipeline through selectedBurnSubtitleIndex, and a veto
// cannot set a grant field.
func (ov quirkOverlay) subtitleFormats(declared []string) []string {
	if len(ov.DenySubtitleFormats) == 0 {
		return declared
	}
	kept := make([]string, 0, len(declared))
	for _, format := range declared {
		if token, ok := canonicalSubtitleFormat(format); ok && slices.Contains(ov.DenySubtitleFormats, token) {
			continue
		}
		kept = append(kept, format)
	}
	return kept
}

// bitrateHeightCeiling expresses a video-maxrate ceiling as a height ceiling.
// The ladder vocabulary is closed and fixed, so this is exact: it returns the
// height of the best rung whose advertised bandwidth fits under the cap. The
// parser rejects a cap below the ladder floor, so a candidate always exists.
func (ov quirkOverlay) bitrateHeightCeiling() int {
	if ov.MaxBitrate <= 0 {
		return 0
	}
	for _, rung := range h264LadderRungs {
		if bandwidthFor(rung.VideoKbps) <= ov.MaxBitrate {
			return rung.Height
		}
	}
	return h264LadderRungs[len(h264LadderRungs)-1].Height
}

// heightCeiling is the single height bound the overlay imposes on the transcode
// ladder: the tighter of the explicit cap and the bitrate cap's rung height.
func (ov quirkOverlay) heightCeiling() int {
	return minNonZero(ov.MaxHeight, ov.bitrateHeightCeiling())
}

// remuxPreservesHDR reports whether a container swap would carry this source's
// HDR metadata intact. HDR10+ dynamic metadata and Dolby Vision
// RPU both ride in the elementary stream and are the known remux casualties, so
// their presence makes the remux path ineligible — a quirk cannot override
// another ADR's correctness rule. A source with no recorded video stream is
// unverifiable and therefore does not take the remux path.
func remuxPreservesHDR(streams []store.MediaFileStream) bool {
	video, ok := primaryVideoStream(streams)
	if !ok {
		return false
	}
	return !video.HDR10Plus && video.DoViProfile == 0
}
