// Package store owns catalog state and typed read methods.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

var (
	// ErrPairingCodeExists means a pairing code collision occurred.
	ErrPairingCodeExists = errors.New("pairing code already exists")
	// ErrPairingCodeInvalid means the pairing code is missing, expired, or consumed.
	ErrPairingCodeInvalid = errors.New("pairing code invalid")
	// ErrPairingCodeRoleMismatch means the pairing code does not grant the required role.
	ErrPairingCodeRoleMismatch = errors.New("pairing code role mismatch")
	// ErrClosed means the store was closed; no operation may silently succeed afterwards.
	ErrClosed = errors.New("store is closed")
	// ErrProfileDisabled means the Profile refuses new sessions and enrollment.
	ErrProfileDisabled = errors.New("profile is disabled")
	// ErrBeamHandoffInvalid means a Beam handoff token is missing, expired, consumed, or otherwise unusable.
	ErrBeamHandoffInvalid = errors.New("beam handoff token invalid")
)

// ValidationError rejects a caller-supplied value or reference: a bad
// argument, a missing required field, or an unknown row the caller named.
// The message is stable and safe to surface to clients verbatim — internal
// failures (SQL errors, I/O) must never use this type.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

// LibraryID is a stable Library identifier.
type LibraryID string

// ProfileID is a stable Profile identifier.
type ProfileID string

// SessionID is a stable Session identifier.
type SessionID string

// DeviceID is a stable Device identifier.
type DeviceID string

// MediaID is a stable Media identifier.
type MediaID string

// Library is a top-level set of folders bound to one optional Plugin.
type Library struct {
	ID           LibraryID
	Title        string
	PluginID     *string
	IsRestricted bool
	// PreviewClips is the per-Library preview-clip generation toggle;
	// default ON.
	PreviewClips bool
	// AIIndexing is the per-Library AI-indexing opt-out; default ON.
	// When false, the Enrichment Plugin must not index this Library's Media, so
	// semantic search is never available for it.
	AIIndexing bool
	// FamilyScope is the plugin-declared Library family scope chosen at
	// creation: opaque plugin data core stores verbatim and
	// never interprets. Empty means no scope was chosen — the plugin's default
	// per-file behavior. Identification reads it to fix classification
	// deterministically for the Library.
	FamilyScope string
	// Custodial is the backing-model axis: false is a Scanned
	// Library (it indexes the operator's own files on disk; a vanished
	// fingerprint goes missing, never deleted), true is a Custodial Library —
	// Beamfall holds the canonical copy itself. A Custodial Library carries a
	// durability contract: losing a fingerprint is data loss, not a
	// health-report missing, so its trashed Media are never hard-purged
	// (PurgeTrashedBefore exempts them; invariant #6). Default false makes
	// every existing Library Scanned. Orthogonal to Encrypted.
	Custodial bool
	// Encrypted marks the at-rest encryption regime active for this Library
	// (ADR-0045 §2.2-2.3). ENC-1 models the attribute and the invariants it
	// implies only; the key model (ENC-2: DEK/KEK/Recovery Key) and at-rest
	// cipher (ENC-3: SQLCipher) land in later tickets — no crypto here. Default
	// false leaves every existing Library not-encrypted. Orthogonal to
	// Custodial; the two compose (Photo Backup = Custodial + Encrypted).
	Encrypted bool
}

// Profile is the user-facing Profile selected by a client session.
type Profile struct {
	ID      ProfileID
	Name    string
	Kind    ProfileKind
	Library []LibraryID
	// MaxConcurrentStreams caps the Profile's simultaneous streams; nil means
	// unlimited (stored as NULL).
	MaxConcurrentStreams *int
	// TranscodeAllowed gates the server HLS transcode rung: false
	// means playbackInfo only ever offers direct play.
	TranscodeAllowed bool
	// Disabled hides the Profile from pickers and refuses new sessions;
	// engagement history is preserved untouched.
	Disabled bool
	// AudioLanguages is the ordered audio-track language preference list.
	AudioLanguages []string
	// SubtitleLanguages is the ordered subtitle-track language preference list.
	SubtitleLanguages []string
	// SubtitleMode selects the subtitle policy for track pre-selection.
	SubtitleMode SubtitleMode
	// PreferSDH prefers SDH/hearing-impaired subtitle tracks when available.
	PreferSDH bool
	// CertificationCeilings are the Profile's per-plugin certification
	// ceilings: Media in the plugin's Libraries ranked above
	// the ceiling on the plugin's ordered scale is filtered from this
	// Profile's reads server-side.
	CertificationCeilings []ProfileCertificationCeiling
	// CreatedAtMs is the Profile's creation wall-clock time in epoch ms;
	// 0 for Profiles predating migration 0069 (seeded or
	// pre-0069 rows).
	CreatedAtMs int64
	// IsOwner marks the single owner Profile — the only
	// Profile permitted to open an Admin Session. At most one Profile holds it
	// (single-owner invariant); the flag is movable via SetProfileOwner.
	IsOwner bool
	// IntegrationSignIn is the explicit per-Profile opt-in for the weaker,
	// non-enrolled integration Session kind. It defaults off.
	IntegrationSignIn bool
}

// ProfileCertificationCeiling pins a Profile's ceiling on one plugin's
// ordered certification scale. AllowUnrated overrides the fail-closed
// default that blocks unrated Media under a ceiling. FilterVisibility
// sets the visibility mode for blocked Media: "hide" (default, absent
// from browse) or "locked" (visible-but-unplayable, byte gate denied).
type ProfileCertificationCeiling struct {
	PluginID         string
	Ceiling          string
	AllowUnrated     bool
	FilterVisibility string
}

// ProfileKind describes a Profile's policy class.
type ProfileKind string

const (
	// ProfileAdult can reveal restricted Libraries when the session has passed the PIN gate.
	ProfileAdult ProfileKind = "adult"
	// ProfileManaged has no reveal path.
	ProfileManaged ProfileKind = "managed"
)

// SubtitleMode is the closed subtitle-policy vocabulary for track pre-selection.
type SubtitleMode string

const (
	// SubtitleModeOff never pre-selects a subtitle track.
	SubtitleModeOff SubtitleMode = "off"
	// SubtitleModeSmart enables subtitles when the audio language misses the preference list.
	SubtitleModeSmart SubtitleMode = "smart"
	// SubtitleModeForcedOnly pre-selects forced subtitle tracks only.
	SubtitleModeForcedOnly SubtitleMode = "forced-only"
	// SubtitleModeAlways always pre-selects a subtitle track.
	SubtitleModeAlways SubtitleMode = "always"
)

func validSubtitleMode(mode SubtitleMode) bool {
	switch mode {
	case SubtitleModeOff, SubtitleModeSmart, SubtitleModeForcedOnly, SubtitleModeAlways:
		return true
	}
	return false
}

// ProfileSettings is a partial Profile policy write: nil fields are left
// unchanged. MaxConcurrentStreams 0 clears the cap back to unlimited
// (stored NULL); an empty language list clears that preference list.
type ProfileSettings struct {
	Name                 *string
	Kind                 *ProfileKind
	MaxConcurrentStreams *int
	TranscodeAllowed     *bool
	Disabled             *bool
	AudioLanguages       *[]string
	SubtitleLanguages    *[]string
	SubtitleMode         *SubtitleMode
	PreferSDH            *bool
	IntegrationSignIn    *bool
}

func (settings ProfileSettings) empty() bool {
	return settings.Name == nil &&
		settings.Kind == nil &&
		settings.MaxConcurrentStreams == nil &&
		settings.TranscodeAllowed == nil &&
		settings.Disabled == nil &&
		settings.AudioLanguages == nil &&
		settings.SubtitleLanguages == nil &&
		settings.SubtitleMode == nil &&
		settings.PreferSDH == nil &&
		settings.IntegrationSignIn == nil
}

func (settings ProfileSettings) validate() error {
	if settings.Name != nil && strings.TrimSpace(*settings.Name) == "" {
		return ValidationError("profile name is required")
	}
	if settings.Kind != nil && *settings.Kind != ProfileAdult && *settings.Kind != ProfileManaged {
		return ValidationError("profile kind must be adult or managed")
	}
	if settings.MaxConcurrentStreams != nil && *settings.MaxConcurrentStreams < 0 {
		return ValidationError("maxConcurrentStreams must be non-negative")
	}
	if settings.SubtitleMode != nil && !validSubtitleMode(*settings.SubtitleMode) {
		return ValidationError("subtitleMode must be off, smart, forced-only, or always")
	}
	for _, list := range []*[]string{settings.AudioLanguages, settings.SubtitleLanguages} {
		if list == nil {
			continue
		}
		for _, language := range *list {
			if strings.TrimSpace(language) == "" {
				return ValidationError("language entries cannot be empty")
			}
		}
	}
	return nil
}

// SessionKind is the closed authorization posture of a Session.
type SessionKind string

const (
	SessionKindEnrolled    SessionKind = "enrolled"
	SessionKindIntegration SessionKind = "integration"
)

// IntegrationScope is the closed compat authority vocabulary.
type IntegrationScope string

const (
	IntegrationScopeRead    IntegrationScope = "compat.read"
	IntegrationScopeRefresh IntegrationScope = "compat.refresh"
)

// Session is the server-side authorization context for a Profile.
type Session struct {
	ID                SessionID
	DeviceID          DeviceID
	ProfileID         ProfileID
	Kind              SessionKind
	IntegrationScopes []IntegrationScope
	Revealed          bool
	ExpiresAt         time.Time
	// AdminUntil is the Admin Session window stamped on this session.
	// Zero means no admin authorization; a
	// future timestamp means the session may invoke Console operations until
	// then. Orthogonal to the device's Role and the session's Reveal state.
	AdminUntil time.Time
}

func validIntegrationScope(scope IntegrationScope) bool {
	return scope == IntegrationScopeRead || scope == IntegrationScopeRefresh
}

func encodeIntegrationScopes(scopes []IntegrationScope) (string, error) {
	if len(scopes) == 0 {
		return "", ValidationError("integration session scopes are required")
	}
	seen := map[IntegrationScope]bool{}
	encoded := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if !validIntegrationScope(scope) {
			return "", ValidationError("unknown integration session scope")
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		encoded = append(encoded, string(scope))
	}
	return strings.Join(encoded, ","), nil
}

func decodeIntegrationScopes(encoded string) ([]IntegrationScope, error) {
	if encoded == "" {
		return nil, errors.New("integration session scopes are missing")
	}
	parts := strings.Split(encoded, ",")
	scopes := make([]IntegrationScope, 0, len(parts))
	for _, part := range parts {
		scope := IntegrationScope(part)
		if !validIntegrationScope(scope) {
			return nil, errors.New("unknown integration session scope")
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

// Device is an enrolled client device.
type Device struct {
	ID                DeviceID
	Name              string
	Role              DeviceRole
	DistributionClass DeviceDistributionClass
	Surface           DeviceSurface
	SecretHash        string
	Revoked           bool
}

// DeviceSurface is the persisted first-party client surface bound to a device.
// This store-owned subset mirrors the device surfaces in capabilities without
// importing that architecture leaf into store.
type DeviceSurface string

const (
	DeviceSurfaceCoreWebUI     DeviceSurface = "core-web-ui"
	DeviceSurfaceMacOSManager  DeviceSurface = "macos-manager"
	DeviceSurfaceIOSViewer     DeviceSurface = "ios-viewer"
	DeviceSurfaceIPadManager   DeviceSurface = "ipad-manager"
	DeviceSurfaceTVOSViewer    DeviceSurface = "tvos-viewer"
	DeviceSurfaceAndroidViewer DeviceSurface = "android-viewer"
)

// NormalizeDeviceSurface trims and validates a persisted device surface.
// Empty values retain the pre-rollout enrollment behavior by starting at the
// core-web-ui placeholder; a catalog-verified native handshake may bind that
// placeholder once to its native surface.
func NormalizeDeviceSurface(surface DeviceSurface) (DeviceSurface, error) {
	switch DeviceSurface(strings.TrimSpace(string(surface))) {
	case "", DeviceSurfaceCoreWebUI:
		return DeviceSurfaceCoreWebUI, nil
	case DeviceSurfaceMacOSManager:
		return DeviceSurfaceMacOSManager, nil
	case DeviceSurfaceIOSViewer:
		return DeviceSurfaceIOSViewer, nil
	case DeviceSurfaceIPadManager:
		return DeviceSurfaceIPadManager, nil
	case DeviceSurfaceTVOSViewer:
		return DeviceSurfaceTVOSViewer, nil
	case DeviceSurfaceAndroidViewer:
		return DeviceSurfaceAndroidViewer, nil
	default:
		return "", ValidationError("device surface is not a supported first-party surface")
	}
}

// DeviceRole describes the server-side capability class for an enrolled device.
type DeviceRole string

const (
	// DeviceRoleViewer is a playback-oriented client.
	DeviceRoleViewer DeviceRole = "viewer"
	// DeviceRoleManager can perform management and enrollment workflows.
	DeviceRoleManager DeviceRole = "manager"
)

// DeviceDistributionClass describes where an enrolled client binary may run.
type DeviceDistributionClass string

const (
	DeviceDistributionDirect   DeviceDistributionClass = "direct"
	DeviceDistributionAppStore DeviceDistributionClass = "appStore"
)

// NormalizeDeviceDistributionClass trims and validates class, defaulting an
// empty value to DeviceDistributionDirect; any value other than direct or
// appStore is rejected as a ValidationError.
func NormalizeDeviceDistributionClass(class DeviceDistributionClass) (DeviceDistributionClass, error) {
	switch DeviceDistributionClass(strings.TrimSpace(string(class))) {
	case "":
		return DeviceDistributionDirect, nil
	case DeviceDistributionDirect:
		return DeviceDistributionDirect, nil
	case DeviceDistributionAppStore:
		return DeviceDistributionAppStore, nil
	default:
		return "", ValidationError("device distribution class must be direct or appStore")
	}
}

// NarrowDeviceDistributionClass resolves claim against the enrolled session's
// distribution class: an empty claim keeps the session's class, otherwise the
// claim must normalize successfully and cannot widen an appStore session to
// direct (App Store distribution can only narrow toward itself, never relax).
func NarrowDeviceDistributionClass(session DeviceDistributionClass, claim string) (DeviceDistributionClass, error) {
	base, err := NormalizeDeviceDistributionClass(session)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(claim) == "" {
		return base, nil
	}
	requested, err := NormalizeDeviceDistributionClass(DeviceDistributionClass(claim))
	if err != nil {
		return "", err
	}
	if base == DeviceDistributionAppStore && requested != DeviceDistributionAppStore {
		return "", ValidationError("device distribution class cannot widen session")
	}
	return requested, nil
}

// MediaClass is the small fixed vocabulary of playback classes (ADR-0029:
// audio is the first non-video class; ADR-0043: image completes the set).
// Named by shape, never domain — "image" is the class, "Photo" is a plugin
// display label (invariant #1).
type MediaClass string

const (
	// MediaClassVideo resumes and renders video transport.
	MediaClassVideo MediaClass = "video"
	// MediaClassAudio never resumes (plays from the top) and counts plays
	// at the 80% threshold.
	MediaClassAudio MediaClass = "audio"
	// MediaClassImage is a still image: no duration, no timeline,
	// no transcode/HLS ladder, and none of the video/audio Generated Assets.
	// It is served as direct authenticated bytes, like a download.
	MediaClassImage MediaClass = "image"
)

// MutationSource is the provenance enum for descriptive-metadata writes
// (ADR-0041 §mutated_by): which agent last wrote title/summary/year to a
// Media row. The zero value is the empty string; readers treat "" and
// MutationSourceScan as equivalent (pre-migration rows and scan-imported rows).
type MutationSource string

const (
	// MutationSourceManual is a human free-text/bulk metadata edit — the
	// highest-precedence writer (the "user/manual" tier). A manual edit
	// auto-locks the field: nothing below it (identification, re-match, scan,
	// hydration, AI) may overwrite it.
	MutationSourceManual MutationSource = "manual"
	// MutationSourceOperator means a human operator applied identification via
	// identifyApply (an explicit candidate choice) — the "identified" tier.
	MutationSourceOperator MutationSource = "operator"
	// MutationSourceScan means the title was derived from the filename by the
	// scanner — the "parsed" tier, the lowest-trust implicit assignment above
	// hydration.
	MutationSourceScan MutationSource = "scan"
	// MutationSourceHydrated marks plugin/tag entity-hydration fills — below
	// filename parse, above machine-derived (the "hydrated" tier).
	MutationSourceHydrated MutationSource = "hydrated"
	// MutationSourceAI marks machine-derived enrichment writes. It is the
	// lowest-precedence writer and must not clobber identified/user-authored
	// fields.
	MutationSourceAI MutationSource = "machine-derived"
)

func mutationSourceFromDB(source string) MutationSource {
	if source == "ai" {
		return MutationSourceAI
	}
	return MutationSource(source)
}

// metadataSourceRank is the total precedence order over metadata-field write
// sources: user/manual > identified > parsed > hydrated >
// machine-derived. A write of source S may overwrite a field last written by
// source T only when rank(S) >= rank(T); the provenance record of the winning
// source is authoritative over the legacy media.<field>_identified booleans.
// An empty/unknown source ranks as parsed — the same "" == scan reader rule the
// mutated_by column already uses (pre-migration and scan-imported rows).
// Importers are behavioral-state-only and never write here, so
// they carry no rank in this chain.
func metadataSourceRank(source MutationSource) int {
	switch source {
	case MutationSourceManual:
		return 4
	case MutationSourceOperator:
		return 3
	case MutationSourceScan:
		return 2
	case MutationSourceHydrated:
		return 1
	case MutationSourceAI:
		return 0
	default:
		return 2 // "" / unknown == parsed (scan-equivalent)
	}
}

// Media is the core generic playable entity.
type Media struct {
	ID        MediaID
	LibraryID LibraryID
	Title     string
	// Summary is the identified descriptive summary ("" until identification
	// applies one — ADR-0003 §4).
	Summary string
	// Year is the identified release year (0 until identification applies one).
	Year            int
	DurationSeconds int
	Container       string
	Codec           string
	Class           MediaClass
	// VideoWidth/VideoHeight are the probed frame size in pixels; 0 for
	// audio-class Media or files scanned before probing captured dimensions.
	// The transcode ladder reads these to never advertise a rendition above
	// the source (ADR-0010 §8: honest ABR).
	VideoWidth  int
	VideoHeight int
	// MutatedBy records which agent last wrote descriptive metadata (title/
	// summary/year) — see MutationSource. "" means pre-migration or no
	// metadata has been written yet; treat as MutationSourceScan.
	MutatedBy MutationSource
}

// MediaMetadata is a partial descriptive-metadata write (manual
// identification, ADR-0003 §4): nil fields are left unchanged.
type MediaMetadata struct {
	Title   *string
	Summary *string
	Year    *int
	// Source records who is making this write for provenance tracking. If
	// zero-value the store falls back to MutationSourceOperator (all
	// explicit identification writes are operator-initiated today).
	Source MutationSource
}

// ArtworkCandidate is one persisted scraped artwork alternative for a
// Media/slot pair. SourceURL is server-internal; clients receive preview URLs
// minted by API handlers instead of raw third-party URLs.
type ArtworkCandidate struct {
	ID        string
	MediaID   MediaID
	Slot      string
	SourceURL string
	PluginID  string
	ScraperID string
	Applied   bool
	CreatedAt time.Time
}

// MediaFile is the primary file backing one playable Media item.
type MediaFile struct {
	MediaID            MediaID
	Path               string
	ContentFingerprint string
	// VersionLabel names the Version (alternate encode) this file belongs to.
	// "" is the default Version — every single-file Media's lone
	// file. Files that share a (MediaID, VersionLabel) form one Version;
	// playback negotiates the best Version for the client before transcode.
	VersionLabel string
	// PartOrdinal orders the Parts of one Version into a single timeline
	// (0, 1, 2, …). 0 is the sole Part of a single-file Version.
	PartOrdinal int
	// OSHash is the OpenSubtitles-style 64-bit content hash as a lowercase
	// 16-hex string; "" until a scan computes it.
	OSHash string
	// PHash is a 64-bit perceptual difference hash over one sampled frame,
	// same encoding; "" until a scan with frame extraction computes it.
	PHash      string
	MissingAt  time.Time
	LastSeenAt time.Time
	// SizeBytes and ModTime are the file's stat() size and modification time at
	// the scan that last hashed it (the cheap-triple scan fast-path). Zero means
	// "not recorded" and never matches a real stat.
	SizeBytes int64
	ModTime   time.Time
}

// ImportMediaFile is a file-plane import request resolved by content identity.
type ImportMediaFile struct {
	LibraryID LibraryID
	Path      string
	Title     string
	Container string
	Codec     string
	Class     MediaClass
	// Year is the parser's best-guess release year; 0 when unknown. Written to
	// the media.year column with an identification-wins / empty-never-clobbers
	// CASE, mirroring Title.
	Year int
	// Summary is an import-supplied descriptive summary; "" when the scan/import
	// carries none. Written with the same per-field preservation CASE as Title
	// (ADR-0009 §8 / migration 0028 summary_identified): an identified or
	// otherwise-set summary is never clobbered by a re-scan, and an empty import
	// summary never erases a stored one. The launch importers leave
	// it "" (they import behavioral state, not metadata); the field lets a
	// scan-side sidecar reader fill a gap without a re-scan wiping it.
	Summary             string
	ContentFingerprint  string
	DurationSecondsHint int
	// VideoWidthHint/VideoHeightHint carry the probed frame size; 0 when the
	// probe was unavailable (re-scans without FFmpeg keep the stored values).
	VideoWidthHint  int
	VideoHeightHint int
	// OSHash and PHash carry scan-computed fingerprint hashes (MediaFile's
	// fields). Empty values never clobber stored ones — a re-scan on a
	// machine without FFmpeg must not erase an earlier phash.
	OSHash string
	PHash  string
	// SizeBytes and ModTime are the file's stat() size and modification time,
	// recorded so a later scan can recognise an unchanged file by the cheap
	// (path, size, mtime) triple and skip re-hashing (ADR-0009 identity is
	// unaffected — the skip only avoids recompute). Zero values mean "not
	// recorded" and never match a real stat.
	SizeBytes int64
	ModTime   time.Time
	// Streams and FontAttachments are the per-stream inventory captured at the
	// probe. When non-nil, the import full-replaces the stored
	// inventory for this content fingerprint (a re-probe is a full rescan, no
	// partial backfill); a nil slice (a probe-less re-scan) leaves the stored
	// inventory untouched, like the empty-never-clobbers hashes above.
	Streams         []MediaFileStream
	FontAttachments []MediaFileFontAttachment
	// Sidecars are the .srt/.vtt sidecar caption tracks discovered next to this
	// media file at scan. Same full-replace posture as
	// Streams/FontAttachments: a non-nil slice full-replaces the stored sidecar
	// rows for this content fingerprint (a re-scan refreshes — a removed sidecar
	// drops, a new one appears), a nil slice (the probe-less cheap-triple
	// fast-path) leaves the stored sidecars untouched.
	Sidecars []MediaFileSidecarCaption
	// Extra marks this Media as an Extra: a typed child file
	// (detected at scan by folder/suffix convention) that is excluded BY
	// DEFAULT from the discovery surfaces — browse Sources, continue-watching,
	// dedupe, and stats — while staying directly addressable. This is the
	// generic SHAPE bit (invariant #1): the specific extra TYPE (trailer, BTS,
	// deleted clip, …) is plugin display vocabulary, never stored in Core. On a
	// re-scan the bit is sticky once set: a later pass that no longer recognises
	// the convention (e.g. a renamed parent folder) does not silently
	// un-exclude the Extra.
	Extra bool
}

// ImportMediaFilePart is one file in a parser-confirmed Parts group. The scan
// parser supplies SourceOrdinal; storage normalizes it to zero-based
// MediaFile.PartOrdinal inside the default Version.
type ImportMediaFilePart struct {
	File          ImportMediaFile
	SourceOrdinal int
}

// mediaExcludeExtrasClause is the default-applied negative predicate that hides
// Extras from a discovery Source's `media m` scan. Append it to
// the WHERE of any Source that should exclude Extras by default (browse,
// all-items, search). An explicit "extras of X" read (MediaRelationsOf with
// MediaRelationExtraOf) does NOT use it, so Extras remain directly addressable.
// It is a const string with no bound parameters, so appending it never disturbs
// a query's positional argument list.
const mediaExcludeExtrasClause = ` AND m.extra = 0`

// MediaFileStream is one stream of a media file's per-stream inventory.
// The fields mirror ffmpeg.ProbeStream; scan maps the probe
// onto these so the store stays free of an ffmpeg import. Generic container
// shape, never domain vocabulary (invariant #1).
type MediaFileStream struct {
	Index           int
	Type            string
	Codec           string
	Profile         string
	Level           int
	Language        string
	Default         bool
	Forced          bool
	HearingImpaired bool
	VisualImpaired  bool
	Commentary      bool
	Channels        int
	ChannelLayout   string
	BitDepth        int
	ColorTransfer   string
	ColorPrimaries  string
	HDR10Plus       bool
	DoViProfile     int
	DoViBLCompatID  int
}

// MediaFileFontAttachment is one muxed font attachment captured at scan:
// libass renders ASS subtitles with these fonts.
type MediaFileFontAttachment struct {
	Filename string
	MimeType string
}

// MediaFileSidecarCaption is one sidecar file discovered next to a media file
// at scan: an external sibling file, not a
// muxed stream, so it carries its own path instead of a stream index. RelPath
// is relative to the media file's directory.
//
// Kind is the closed, GENERIC sidecar-type discriminator (invariant #1):
// SidecarKindCaption (a .srt/.vtt subtitle — Format is the 'srt'|'vtt'
// vocabulary, served as WebVTT, with the Language/Forced/SDH player track hints
// parsed from the filename) or SidecarKindInteractive (a plugin-recognised
// interactive-control track — Format is the bare extension, served as raw
// bytes, with no caption hints). The type name and Kind value are shape words;
// which extensions map to the interactive kind is plugin-declared data, never
// named in Core.
type MediaFileSidecarCaption struct {
	RelPath  string
	Kind     string
	Format   string
	Language string
	Forced   bool
	SDH      bool
}

// Sidecar kind values — the closed GENERIC discriminator on
// MediaFileSidecarCaption.Kind (invariant #1). Empty (the 0059 zero value /
// 0065 column DEFAULT) reads as caption.
const (
	// SidecarKindCaption is the 0059 subtitle sidecar: served as WebVTT with
	// language/forced/SDH track hints.
	SidecarKindCaption = "caption"
	// SidecarKindInteractive is the plugin-recognised interactive-control
	// sidecar: served as raw bytes, no caption hints.
	SidecarKindInteractive = "interactive"
)

// MediaFingerprints are the content-identity values the scrape engine
// substitutes into scraper templates ({oshash}, {phash}, {duration} —
// ADR-0027 D2 §7), read off the Media row and its primary file.
type MediaFingerprints struct {
	OSHash          string
	PHash           string
	DurationSeconds int
}

// SourceQuery describes a bounded manifest Source request.
type SourceQuery struct {
	Kind      string
	Limit     int
	Offset    int
	ProfileID ProfileID
	// Query narrows the all-items Source into an explicit in-archive search.
	// Empty keeps the ordinary unfiltered catalog feed.
	Query string
	// EntityKind scopes an "entity-items" query to one declared kind.
	EntityKind string
	// EntityFieldFilters AND-composes registered, Filterable Entity field
	// equality predicates for an "entity-items" query. Ignored by other kinds.
	EntityFieldFilters []EntityField
	// EntityID scopes an "entity-media" query to one context Entity (the
	// detail view's entity); required for that kind, empty for the rest.
	EntityID EntityID
	// LibraryID restricts results to one Library (a library nav entry's
	// Sections); empty means every visible Library.
	LibraryID LibraryID
	// FieldFilters narrows the catalog feeds ("all-items",
	// "recently-added") by equality/range on plugin-declared Media field
	// values. The wire shape is the smallest backward-compatible one:
	// strings stay text equality, numbers and booleans stay typed equality,
	// and range objects use gte/lte. Ignored on every other kind — the
	// engagement and entity feeds carry their own scope, per the plugin
	// contract's filterable-source rule.
	FieldFilters map[string]any
	// EntityFilter narrows the catalog feeds to Media holding a relation row
	// from one named entity (declared kind + stored title) in a visible
	// Library. A missing entity — or one hidden behind the Reveal contract —
	// yields an empty feed, never an error (hidden and missing stay
	// indistinguishable). Ignored on every other kind, like FieldFilters.
	EntityFilter *EntityFilter
	// Seed parameterizes the "random" Source kind: the same seed yields the
	// same shuffle order across calls. Empty is a valid seed; ignored on every
	// other kind.
	Seed string
	// Value selects the band of a valued predicate Source:
	// the "resume-progress" band ("in-progress") or the "availability" band
	// ("archived"/"streamed-provisional"/"not-downloaded"). An unrecognized
	// value yields an empty feed (never an error), like a missing entity.
	// Ignored on every other kind.
	Value string
}

// EntityFilter names one entity by declared kind plus stored title (the same
// Library+kind+name key the deterministic entity-ID derivation uses).
type EntityFilter struct {
	Kind string
	Name string
}

// SourceItem is one generic item returned to hydrate a manifest Section.
//
// Restricted records whether the item's owning Library is restricted
// (ADR-0004 §4: Library-level granularity — there is no per-item secret flag).
// It is descriptive metadata on an item the store has ALREADY reveal-filtered
// in (libraryVisible), so it is only ever non-false for an item a Revealed
// scope is lawfully being served. An unrevealed scope never receives a
// restricted-Library item in the first place, so its items carry Restricted
// false. The graph layer additionally suppresses the field on the wire unless
// the scope is Revealed — the absence is the privacy enforcement.
type SourceItem struct {
	ID         MediaID
	Kind       string
	Title      string
	Summary    string
	Restricted bool
	// Locked is true when the item is visible in browse but blocked by a
	// certification ceiling whose FilterVisibility is "locked" (ACCESS-1,
	// ADR-0113 §2.1). Set by annotateLockedLocked; absent means not-locked.
	Locked bool
	// Muted is true when the acting Profile's Per-Profile Mute reaches
	// this item — it is surfaced deliberately (explicit search, direct navigation)
	// or survives a passive rail via a positive signal (a Favorite-overridden
	// broad-category mute, §2.6/§2.7). Set by annotateMutedLocked; absent means
	// not-muted. Distinct from the mute *suppression* predicate
	// (applyMuteFilterLocked): a badged item still appears, marked-not-hidden so
	// the state is legible and reversible in place (§2.7).
	Muted bool
}

// PairingCode is a short-lived enrollment grant.
type PairingCode struct {
	Code       string
	ProfileID  ProfileID
	DeviceRole DeviceRole
	ExpiresAt  time.Time
	ConsumedAt time.Time
}

// AuthAttempt records an authentication or enrollment attempt for throttling.
type AuthAttempt struct {
	OccurredAt time.Time
	Action     string
	Key        string
	Success    bool
}

// LibraryFilter is the store-level read policy for Library-scoped content.
//
// AllowedIDs contract — a populated slice means "only these Libraries". An
// empty/nil AllowedIDs means "no Libraries" and fails CLOSED (H4, 2026-06-16):
// a default-constructed filter is the EMPTY visibility set, not the full one.
// The single deliberate "all Libraries" grant is expressed by AllLibraries —
// the ops-class admin API token / admin-scoped key and every server-internal
// full scan (schedule registration, generated-asset/artwork passes, the
// migration wizard, subscription retention, profile creation) set it
// explicitly. A session-scoped Profile that resolves with zero Library grants
// therefore sees NOTHING, rather than every unrestricted Library.
//
// The restricted-Library guard below is an independent, additional
// confidentiality boundary: IsRestricted Libraries stay hidden unless Revealed,
// regardless of AllowedIDs/AllLibraries.
type LibraryFilter struct {
	// AllowedIDs restricts visibility to the listed Libraries. Empty/nil means
	// "no Libraries" (fails closed); use AllLibraries for the all-Libraries
	// grant. A default-constructed filter is therefore the empty set.
	AllowedIDs []LibraryID
	// AllLibraries is the explicit "every Library" sentinel — the one safe way
	// to express full visibility with an empty AllowedIDs. Set only by the
	// ops-class token / admin-scoped key and server-internal full scans; never
	// by a Profile session (so a zero-grant session fails closed).
	AllLibraries bool
	Revealed     bool
	// SealedIDs lists Libraries that are currently Sealed (their at-rest DEK is
	// not in memory — ADR-0098 area 2 / ADR-0045 §2.2). A Sealed Library serves
	// NOTHING: any Library named here is hidden fail-closed by libraryVisible,
	// independent of AllowedIDs/AllLibraries, Revealed, and IsRestricted (Sealed
	// and IsRestricted are orthogonal — a sealed-but-unrestricted Library must
	// still be hidden). Sealed state is runtime, in-memory, and known only to the
	// encryption seam (internal/libencrypt), so the store cannot derive it: the
	// composition layer that holds the Sealed predicate stamps it here before any
	// data-plane read (offline-pack build/serve, Collection resolve). Empty/nil
	// means "nothing sealed", so deployments without library encryption pay
	// nothing and legitimate reads are unaffected.
	SealedIDs []LibraryID
	// Certification constraints filter Media above a Profile's
	// certification ceiling, composed with reveal filtering on every read
	// path.
	Certification []CertificationConstraint
	// PlaybackGate, when true, applies all certification constraints as hard
	// exclusions regardless of FilterVisibility.
	// Browse uses false so "locked" Media remains visible; the byte gate uses
	// true so the server denies the actual byte stream.
	PlaybackGate bool
	// GrantedMediaIDs lists media items for which the managed Profile holds an
	// active AccessGrant. These IDs bypass the cert
	// enforcement in both browse (annotateLockedLocked skips them) and the byte
	// gate (certificationFilterSQL adds an OR m.id IN (?) escape hatch).
	// Populated at scope resolution time for managed Profiles; nil for adults.
	GrantedMediaIDs []MediaID
}

// CertificationConstraint is one resolved certification ceiling: inside
// Libraries bound to PluginID, only Media whose `certification` Media field
// holds one of the Allowed values is visible. Media with no certification
// value, or a value outside Allowed (above the ceiling, or unknown to the
// scale), is filtered fail-closed; AllowUnrated re-admits unrated Media
// only. The caller resolves Allowed from the plugin's ordered scale — the
// store never interprets scale values.
type CertificationConstraint struct {
	PluginID     string
	Allowed      []string
	AllowUnrated bool
	// Locked is true when the profile ceiling's FilterVisibility is "locked".
	// When Locked, blocked Media is visible in browse (not excluded
	// by certificationFilterSQL) but denied at the byte gate (excluded when
	// PlaybackGate is true).
	Locked bool
}

// Store is the single SQL-backed catalog and events state owner.
type Store struct {
	mu             sync.RWMutex
	backupIngestMu sync.Mutex
	closed         bool
	catalogW       *sql.DB // single-conn writer, BEGIN IMMEDIATE
	catalogR       *sql.DB // read pool, WAL readers
	eventsW        *sql.DB
	eventsR        *sql.DB
	now            func() time.Time
	dataDir        string
	catalogPath    string
	eventsPath     string
	logger         *slog.Logger
	// convertStepHook, when non-nil, fires at each named ownership-succession
	// fence point inside ConvertManagedFile. Test-only: it lets a
	// deterministic interleaving test expire or hand over the job lease at an
	// exact fence, which wall-clock scheduling cannot express. Never set in
	// production wiring.
	convertStepHook func(step string)
	// entityIndexes holds the registered plugin-declared field index
	// postures: kind → field → spec (RegisterEntityFieldIndexes). Guarded by
	// mu like every other member.
	entityIndexes map[string]map[string]EntityFieldIndex
	// mediaFieldSearchPolicies holds the active plugin-scoped Media field
	// search postures. Guarded by mu and atomically replaced at activation.
	mediaFieldSearchPolicies map[string]map[string]bool
	// entityKindPolicies holds the registered kind-level Entity Kind policies:
	// kind → policy (RegisterEntityKindPolicies). These are the declarative
	// KIND-level seams (FullTextSearchable, OrphanPolicy:"gc", CursorPaginated)
	// the store honors, distinct from the per-FIELD index postures above.
	// Guarded by mu.
	entityKindPolicies map[string]EntityKindPolicy
	// trashDisabled mirrors the trash.enabled config toggle (set once at
	// Open, restart-scoped). While true, autonomous destruction paths skip
	// (hard invariant 6); interactive trash, restore, and expired-trash
	// purge are unaffected.
	trashDisabled bool
	// trashDisabledSweepSkips counts retention sweeps skipped because trash
	// is disabled (one per skipped sweep, not per item).
	trashDisabledSweepSkips    atomic.Int64
	encryptedDBRoot            string
	encryptedDBWorkingRoot     string
	encryptedDBLifecycleEnable bool
	encryptedDBs               map[LibraryID]*encryptedLibraryDB
	// encDerivativeReconciler is the app-injected seam (nil by default) invoked
	// at the unseal→register flip with the just-enumerable media inventory so the
	// app layer can purge/re-protect orphaned encrypted-Library derivatives
	// (ADR-0045 §reconcile). The store stays a storage leaf: it owns the
	// lifecycle timing, not the derivative filesystem layout. Guarded by mu.
	encDerivativeReconciler EncryptedDerivativeReconciler
	// snapshotSealer is the app-injected Backup Set encryption/verification
	// seam. The store owns snapshot consistency and publication, while the
	// backup package owns envelope cryptography. Set once at composition.
	snapshotSealer       SnapshotSealer
	testEncryptedMediaID MediaID
	allowedRoots         []string
	mountCapabilityProbe *mountCapabilityProbe
	// role is the process authority posture, set once at Open.
	// Main (the zero value) authors freely; Attached refuses catalog-identity
	// authoring and engagement-projection writes via guardAuthority.
	role serverrole.Role
	// importTokens is the session-scoped server-side import-token registry
	// (apple ADR-0003 §1): it maps an opaque server-minted token to the
	// registered out-of-Library source reference. RegisterImportSource mints a
	// token; ImportManagedFile resolves it and refuses any token the store did
	// not vend. Guarded by importTokenMu (a dedicated mutex so token
	// registration never contends on s.mu). Lazily allocated.
	importTokens  map[string]managedImportToken
	importTokenMu sync.Mutex
}

// managedImportToken is the stable-reference record a server-minted import
// token resolves to (apple ADR-0003 §1). sourcePath is the registered locator
// the native layer couriered to the server; ImportManagedFile binds it to an fd
// at import time so a path-level symlink swap after registration is inert.
type managedImportToken struct {
	sourcePath string
}

// RegisterImportSource mints an unforgeable opaque token (crypto/rand) bound to
// an out-of-Library source locator and records it in the session-scoped
// import-token registry (apple ADR-0003 §1). The native authority calls this
// (over the authorized loopback bearer, ADR-0003 §3) AFTER it resolves its
// security-scoped URL: it hands the SERVER the locator and gets back the opaque
// token the JS realm uses. The JS realm never sees a path and cannot self-mint
// a token. ImportManagedFile is the only consumer and rejects any token not in
// this registry. The map is lazily allocated under importTokenMu so a nil-map
// access is impossible regardless of constructor path.
func (s *Store) RegisterImportSource(sourcePath string) (string, error) {
	token, err := newManagedTrashID()
	if err != nil {
		return "", err
	}
	s.importTokenMu.Lock()
	defer s.importTokenMu.Unlock()
	if s.importTokens == nil {
		s.importTokens = map[string]managedImportToken{}
	}
	s.importTokens[token] = managedImportToken{sourcePath: sourcePath}
	return token, nil
}

// InvalidateImportTokens clears the import-token registry (Admin Session end):
// every previously minted token is afterward refused by ImportManagedFile, so a
// locator can never be replayed across sessions.
func (s *Store) InvalidateImportTokens() {
	s.importTokenMu.Lock()
	defer s.importTokenMu.Unlock()
	s.importTokens = nil
}

// resolveImportToken returns the registered source locator for a server-minted
// token, or false if the store did not vend it (the ADR's server-side
// provenance refusal). Looked up under importTokenMu.
func (s *Store) resolveImportToken(token string) (string, bool) {
	s.importTokenMu.Lock()
	defer s.importTokenMu.Unlock()
	entry, ok := s.importTokens[token]
	return entry.sourcePath, ok
}

// guardAuthority returns serverrole.ErrAttachedAuthorityRefused when this store
// runs in an Attached Origin Node for the given authority face,
// and nil on a Main server. It is the one gate the catalog-identity and
// engagement-projection author methods consult.
func (s *Store) guardAuthority(face serverrole.Face) error {
	return s.role.GuardAuthority(face)
}

func (s *Store) serverNow() time.Time {
	if s.now != nil {
		if now := s.now(); !now.IsZero() {
			return now
		}
	}
	return time.Now()
}

// guardLocked reports ErrClosed; callers must hold s.mu (read or write).
func (s *Store) guardLocked() error {
	if s.closed {
		return ErrClosed
	}
	return nil
}

type catalogPlane struct {
	libraryID LibraryID
	encrypted bool
	writer    *sql.DB
	reader    *sql.DB
}

func (s *Store) catalogPlaneForLibraryLocked(ctx context.Context, libraryID LibraryID) (catalogPlane, error) {
	var encrypted int
	err := s.catalogR.QueryRowContext(ctx, `SELECT encrypted FROM library WHERE id = ?`, libraryID).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return catalogPlane{}, errors.New("library not found")
	}
	if err != nil {
		return catalogPlane{}, err
	}
	if encrypted == 0 {
		return catalogPlane{libraryID: libraryID, writer: s.catalogW, reader: s.catalogR}, nil
	}
	db := s.encryptedDBs[libraryID]
	if db == nil || db.state != "unsealed-registered" || db.writer == nil || db.reader == nil {
		return catalogPlane{}, ErrEncryptedLibraryDBDisabled
	}
	return catalogPlane{libraryID: libraryID, encrypted: true, writer: db.writer, reader: db.reader}, nil
}

// MediaDerivativeKey is one Encrypted-Library Media's full derivative-address
// footprint, handed to the reconciler at the unseal→register flip. MediaID keys
// the per-Media derivative classes (artwork under {dataDir}/artwork/<mediaID>/,
// precompute renditions named precompute:<mediaID>:…); Fingerprints keys the
// content-addressed classes (generated assets under {dataDir}/generated/<fp>/,
// subtitle sidecars {dataDir}/subtitles/<fp>.<idx>.vtt). Both are needed: an
// Encrypted-Library Media ID is random (media-enc-…, never derived from its
// fingerprint), and the content fingerprint lives only in the sealed metadata
// DB, so the reconciler — which must not call back into the store — cannot
// otherwise learn which content-addressed derivative dirs belong to this
// Library. The store is the sole owner of this confidential join; it materializes
// it under its own lock so the fingerprint↔Media map never crosses a store
// callback.
type MediaDerivativeKey struct {
	MediaID      MediaID
	Fingerprints []string
}

// EncryptedDerivativeReconciler is the app-layer seam that re-establishes
// protected-plane derivative invariants for an Encrypted Library at the exact
// moment its media inventory becomes enumerable (ADR-0045 §reconcile). The store
// enumerates the inventory (snapshot-before-flip) and hands the live derivative
// key set to the reconciler; the reconciler owns the derivative filesystem layout
// (thumbnails, HLS, artwork, renditions) and purges any orphaned plaintext
// derivative — for an Encrypted Library, generation is fail-closed,
// so ANY plaintext derivative on the shared plane is a crash-stranded leak and
// must be removed. It runs while the store holds its lock, so the snapshot cannot
// drift under it; it must not call back into the store (a re-entrant read would
// deadlock on the held write lock).
type EncryptedDerivativeReconciler interface {
	ReconcileEncryptedDerivatives(ctx context.Context, libraryID LibraryID, live []MediaDerivativeKey) error
}

// SetEncryptedDerivativeReconciler installs the unseal→register derivative
// reconcile seam (ADR-0045 §reconcile). Injected once at wiring time, mirroring
// SetSealed / SetEncryptedBytes. Nil (the default) makes the unseal lifecycle a
// no-op for derivative reconcile, so the store primitive is safe to land ahead of
// the app-layer purge implementation.
func (s *Store) SetEncryptedDerivativeReconciler(r EncryptedDerivativeReconciler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encDerivativeReconciler = r
}

// SetSnapshotSealer installs the Backup Set encryption and public-structure
// verification seam. The composition root supplies it once during startup.
func (s *Store) SetSnapshotSealer(sealer SnapshotSealer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotSealer = sealer
}

// snapshotEncryptedLibraryMediaLocked enumerates the non-trashed Media of a
// freshly-unsealed Encrypted Library — each Media's ID plus every content
// fingerprint of its files — directly from its own (not-yet-published) reader:
// the snapshot-before-flip taken at the instant the inventory becomes enumerable.
// Because the caller holds s.mu and the reader is not yet reachable through
// catalogPlaneForLibraryLocked, the returned set is a stable inventory the
// derivative reconciler can trust. Media come back in stable rowid order; a Media
// with no files (or empty fingerprints) still appears so its per-Media derivative
// classes are reconciled. The (Media ↔ fingerprint) join is materialized here,
// under the store lock, and never crosses a store callback — the reconciler needs
// both keys but must not query the store.
func (s *Store) snapshotEncryptedLibraryMediaLocked(ctx context.Context, db *encryptedLibraryDB) ([]MediaDerivativeKey, error) {
	rows, err := db.reader.QueryContext(ctx, `
		SELECT m.id, f.content_fingerprint
		FROM media m
		LEFT JOIN media_file f ON f.media_id = m.id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
		ORDER BY m.rowid, f.rowid
	`, db.libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []MediaDerivativeKey{}
	index := map[MediaID]int{}
	for rows.Next() {
		var id MediaID
		var fingerprint sql.NullString
		if err := rows.Scan(&id, &fingerprint); err != nil {
			return nil, err
		}
		pos, seen := index[id]
		if !seen {
			pos = len(keys)
			index[id] = pos
			keys = append(keys, MediaDerivativeKey{MediaID: id})
		}
		if fingerprint.Valid && fingerprint.String != "" {
			keys[pos].Fingerprints = append(keys[pos].Fingerprints, fingerprint.String)
		}
	}
	return keys, rows.Err()
}

func (s *Store) catalogPlaneForMediaLocked(ctx context.Context, id MediaID) (catalogPlane, error) {
	var libraryID LibraryID
	err := s.catalogR.QueryRowContext(ctx, `SELECT library_id FROM media WHERE id = ?`, id).Scan(&libraryID)
	if err == nil {
		return catalogPlane{libraryID: libraryID, writer: s.catalogW, reader: s.catalogR}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return catalogPlane{}, err
	}
	for libraryID, db := range s.encryptedDBs {
		if db == nil || db.state != "unsealed-registered" || db.reader == nil || db.writer == nil {
			continue
		}
		err = db.reader.QueryRowContext(ctx, `SELECT library_id FROM media WHERE id = ?`, id).Scan(&libraryID)
		if err == nil {
			return catalogPlane{libraryID: libraryID, encrypted: true, writer: db.writer, reader: db.reader}, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return catalogPlane{}, err
		}
	}
	return catalogPlane{}, ValidationError("media not found")
}

func (s *Store) catalogPlaneForEntityLocked(ctx context.Context, id EntityID) (catalogPlane, error) {
	var libraryID LibraryID
	err := s.catalogR.QueryRowContext(ctx, `SELECT library_id FROM entity WHERE id = ?`, id).Scan(&libraryID)
	if err == nil {
		return catalogPlane{libraryID: libraryID, writer: s.catalogW, reader: s.catalogR}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return catalogPlane{}, err
	}
	for libraryID, db := range s.encryptedDBs {
		if db == nil || db.state != "unsealed-registered" || db.reader == nil || db.writer == nil {
			continue
		}
		err = db.reader.QueryRowContext(ctx, `SELECT library_id FROM entity WHERE id = ?`, id).Scan(&libraryID)
		if err == nil {
			return catalogPlane{libraryID: libraryID, encrypted: true, writer: db.writer, reader: db.reader}, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return catalogPlane{}, err
		}
	}
	return catalogPlane{}, errors.New("entity not found")
}

// EnrollDevice inserts an enrolled Device and its initial hidden Session atomically.
func (s *Store) EnrollDevice(ctx context.Context, device Device, session Session) error {
	if device.ID == "" {
		return errors.New("device ID is required")
	}
	if device.Name == "" {
		return errors.New("device name is required")
	}
	if device.Role == "" {
		return errors.New("device role is required")
	}
	distributionClass, err := NormalizeDeviceDistributionClass(device.DistributionClass)
	if err != nil {
		return err
	}
	device.DistributionClass = distributionClass
	deviceSurface, err := NormalizeDeviceSurface(device.Surface)
	if err != nil {
		return err
	}
	device.Surface = deviceSurface
	if device.SecretHash == "" {
		return errors.New("device secret hash is required")
	}
	if session.ID == "" {
		return errors.New("session ID is required")
	}
	if session.DeviceID != device.ID {
		return errors.New("session device mismatch")
	}
	if session.ProfileID == "" {
		return errors.New("session profile is required")
	}
	if session.Kind != "" && session.Kind != SessionKindEnrolled {
		return errors.New("pairing may mint only enrolled sessions")
	}
	session.Kind = SessionKindEnrolled

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if disabled, err := profileDisabled(ctx, s.catalogR, session.ProfileID); err != nil {
		return err
	} else if disabled {
		return ErrProfileDisabled
	}
	return s.insertDeviceSession(ctx, device, session)
}

// VerifyDeviceSecret returns a Device only when the provided one-time-visible secret matches.
func (s *Store) VerifyDeviceSecret(ctx context.Context, id DeviceID, secret string) (Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Device{}, err
	}
	device, err := s.deviceSQL(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if device.SecretHash == "" || secret == "" {
		return Device{}, errors.New("invalid device credential")
	}
	if subtle.ConstantTimeCompare([]byte(device.SecretHash), []byte(HashDeviceSecret(secret))) != 1 {
		return Device{}, errors.New("invalid device credential")
	}
	return device, nil
}

// Device returns a Device by identifier.
func (s *Store) Device(ctx context.Context, id DeviceID) (Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Device{}, err
	}
	return s.deviceSQL(ctx, id)
}

// RevokeDevice marks a Device revoked.
func (s *Store) RevokeDevice(ctx context.Context, id DeviceID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.updateDeviceRevoked(ctx, id, true)
}

// SetDeviceRole changes an active Device's capability class.
func (s *Store) SetDeviceRole(ctx context.Context, id DeviceID, role DeviceRole) (Device, error) {
	if role != DeviceRoleViewer && role != DeviceRoleManager {
		return Device{}, ValidationError("device role must be viewer or manager")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Device{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		"UPDATE device SET role = ? WHERE id = ? AND revoked = 0",
		role, id,
	)
	if err != nil {
		return Device{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Device{}, err
	}
	if affected == 0 {
		return Device{}, ValidationError("device not found or revoked")
	}
	if role == DeviceRoleViewer {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_session SET admin_until_ms = NULL WHERE device_id = ?`, id); err != nil {
			return Device{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}
	return s.deviceSQL(ctx, id)
}

// ListDevices returns every enrolled Device (ADR-0081 device administration),
// ordered by name for a stable Console list. The SecretHash is loaded but the
// caller (the admin-gated `devices` resolver) never serializes it — it is a
// device credential.
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx,
		"SELECT id, name, role, distribution_class, device_surface, secret_hash, revoked FROM device ORDER BY name, id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var d Device
		var revoked int
		if err := rows.Scan(&d.ID, &d.Name, &d.Role, &d.DistributionClass, &d.Surface, &d.SecretHash, &revoked); err != nil {
			return nil, err
		}
		d.Revoked = revoked != 0
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// BindDeviceSurface promotes the migration/default core-web-ui placeholder to
// a catalog-verified native surface, or confirms an existing identical binding.
// A device already bound to another native surface cannot be rebound.
func (s *Store) BindDeviceSurface(ctx context.Context, id DeviceID, surface DeviceSurface) error {
	normalized, err := NormalizeDeviceSurface(surface)
	if err != nil {
		return err
	}
	if normalized == DeviceSurfaceCoreWebUI {
		return ValidationError("native device surface is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx, `
		UPDATE device SET device_surface = ?
		WHERE id = ? AND (device_surface = ? OR device_surface = ?)
	`, normalized, id, DeviceSurfaceCoreWebUI, normalized)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("device surface is already bound to another surface")
	}
	return nil
}

// CreatePairingCode stores a one-time enrollment grant.
func (s *Store) CreatePairingCode(ctx context.Context, code PairingCode) error {
	if code.Code == "" {
		return errors.New("pairing code is required")
	}
	if code.ProfileID == "" {
		return errors.New("pairing profile is required")
	}
	if code.DeviceRole == "" {
		return errors.New("pairing device role is required")
	}
	if code.ExpiresAt.IsZero() {
		return errors.New("pairing expiry is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if exists, err := s.profileExistsSQL(ctx, code.ProfileID); err != nil {
		return err
	} else if !exists {
		return ValidationError("profile not found")
	}
	if disabled, err := profileDisabled(ctx, s.catalogR, code.ProfileID); err != nil {
		return err
	} else if disabled {
		return ErrProfileDisabled
	}
	return s.insertPairingCode(ctx, code)
}

// ConsumePairingCode marks a one-time enrollment grant consumed.
func (s *Store) ConsumePairingCode(ctx context.Context, code string, now time.Time) (PairingCode, error) {
	if code == "" {
		return PairingCode{}, errors.New("pairing code is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PairingCode{}, err
	}
	return s.consumePairingCodeSQL(ctx, code, now.UTC())
}

// PairingCode returns an unconsumed, unexpired pairing code without consuming it.
func (s *Store) PairingCode(ctx context.Context, code string, now time.Time) (PairingCode, error) {
	if code == "" {
		return PairingCode{}, errors.New("pairing code is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PairingCode{}, err
	}
	return s.validPairingCode(ctx, code, now)
}

// RedeemPairingCode atomically consumes a pairing code and inserts the enrolled device/session.
func (s *Store) RedeemPairingCode(ctx context.Context, code string, now time.Time, device Device, session Session) (PairingCode, error) {
	if device.ID == "" {
		return PairingCode{}, errors.New("device ID is required")
	}
	if device.Name == "" {
		return PairingCode{}, errors.New("device name is required")
	}
	if device.Role == "" {
		return PairingCode{}, errors.New("device role is required")
	}
	if device.SecretHash == "" {
		return PairingCode{}, errors.New("device secret hash is required")
	}
	if session.ID == "" {
		return PairingCode{}, errors.New("session ID is required")
	}
	if session.DeviceID != device.ID {
		return PairingCode{}, errors.New("session device mismatch")
	}
	if session.Kind != "" && session.Kind != SessionKindEnrolled {
		return PairingCode{}, errors.New("pairing may mint only enrolled sessions")
	}
	session.Kind = SessionKindEnrolled

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PairingCode{}, err
	}
	return s.redeemPairingCode(ctx, code, now.UTC(), device, session)
}

// RedeemPairingCodeForDevice atomically consumes a pairing code granting the
// given role and applies that role to an already-enrolled, unrevoked device —
// the device-promotion path: per-device, deliberate, and still
// revocable through RevokeDevice. A code granting a different role is left
// unconsumed and the device untouched.
func (s *Store) RedeemPairingCodeForDevice(ctx context.Context, code string, now time.Time, deviceID DeviceID, role DeviceRole, oldSessionID SessionID, newSession Session, newSecretHash string) (PairingCode, error) {
	if code == "" {
		return PairingCode{}, errors.New("pairing code is required")
	}
	if deviceID == "" {
		return PairingCode{}, errors.New("device ID is required")
	}
	if role == "" {
		return PairingCode{}, errors.New("device role is required")
	}
	if oldSessionID == "" {
		return PairingCode{}, errors.New("old session ID is required")
	}
	if newSession.ID == "" {
		return PairingCode{}, errors.New("new session ID is required")
	}
	if newSession.DeviceID != deviceID {
		return PairingCode{}, errors.New("new session device mismatch")
	}
	if newSession.Kind != "" && newSession.Kind != SessionKindEnrolled {
		return PairingCode{}, errors.New("pairing may mint only enrolled sessions")
	}
	newSession.Kind = SessionKindEnrolled
	if newSession.ProfileID == "" {
		return PairingCode{}, errors.New("new session profile is required")
	}
	if newSecretHash == "" {
		return PairingCode{}, errors.New("device secret hash is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PairingCode{}, err
	}
	return s.redeemPairingCodeForDevice(ctx, code, now.UTC(), deviceID, role, oldSessionID, newSession, newSecretHash)
}

// RecordAuthAttempt appends an auth-related attempt used for throttling.
func (s *Store) RecordAuthAttempt(ctx context.Context, attempt AuthAttempt) error {
	if attempt.Action == "" {
		return errors.New("auth attempt action is required")
	}
	if attempt.Key == "" {
		return errors.New("auth attempt key is required")
	}
	if attempt.OccurredAt.IsZero() {
		return errors.New("auth attempt time is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	return s.insertAuthAttempt(ctx, attempt)
}

// Close releases the durable store handles; every later operation returns ErrClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	for _, db := range []*sql.DB{s.catalogR, s.eventsR, s.catalogW, s.eventsW} {
		if db != nil {
			err = errors.Join(err, db.Close())
		}
	}
	for _, db := range s.encryptedDBs {
		if db.reader != nil {
			err = errors.Join(err, db.reader.Close())
		}
		if db.writer != nil {
			err = errors.Join(err, db.writer.Close())
		}
		db.reader, db.writer = nil, nil
	}
	s.catalogW, s.catalogR, s.eventsW, s.eventsR = nil, nil, nil, nil
	return err
}

// Session returns a session by identifier.
func (s *Store) Session(ctx context.Context, id SessionID) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Session{}, err
	}
	return s.sessionSQL(ctx, id)
}

// CreateIntegrationSession inserts a keyless, non-renewable integration
// Session. Standing sessions require the Profile opt-in; bounded admin setup
// sessions use the same kind and ceiling but may be created before opt-in.
func (s *Store) CreateIntegrationSession(ctx context.Context, session Session, requireOptIn bool) error {
	if session.ID == "" || session.ProfileID == "" {
		return ValidationError("integration session identity is required")
	}
	if session.Kind != SessionKindIntegration || session.DeviceID != "" {
		return ValidationError("integration session must be keyless")
	}
	if session.ExpiresAt.IsZero() {
		return ValidationError("integration session expiry is required")
	}
	scopes, err := encodeIntegrationScopes(session.IntegrationScopes)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	profile, err := s.profileSQL(ctx, session.ProfileID)
	if err != nil {
		return err
	}
	if profile.Kind != ProfileAdult || profile.Disabled {
		return ErrProfileDisabled
	}
	if requireOptIn && !profile.IntegrationSignIn {
		return ValidationError("integration sign-in is disabled")
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, kind, integration_scopes)
		VALUES (?, NULL, ?, 0, ?, NULL, ?, ?)`,
		session.ID, session.ProfileID, timeToMillis(session.ExpiresAt), SessionKindIntegration, scopes)
	return err
}

// RevokeIntegrationSession deletes one integration Session for its Profile.
func (s *Store) RevokeIntegrationSession(ctx context.Context, id SessionID, profileID ProfileID) error {
	if id == "" || profileID == "" {
		return ValidationError("integration session identity is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx,
		`DELETE FROM auth_session WHERE id = ? AND profile_id = ? AND kind = ?`,
		id, profileID, SessionKindIntegration)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ValidationError("integration session not found")
	}
	return nil
}

// ProfileByName resolves an exact, case-insensitive Profile display name.
func (s *Store) ProfileByName(ctx context.Context, name string) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	var id ProfileID
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT id FROM profile WHERE lower(name) = lower(?) ORDER BY id LIMIT 1`, strings.TrimSpace(name)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ValidationError("profile not found")
	}
	if err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// OwnerProfile returns the single designated owner Profile.
func (s *Store) OwnerProfile(ctx context.Context) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	var id ProfileID
	if err := s.catalogR.QueryRowContext(ctx, `SELECT id FROM profile WHERE is_owner = 1 LIMIT 1`).Scan(&id); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// Profile returns a Profile by identifier.
func (s *Store) Profile(ctx context.Context, id ProfileID) (Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// Profiles returns every Profile in picker order (name, then id for stable
// ties). Disabled Profiles are included — hiding them from pickers is the
// caller's read policy, not the store's.
func (s *Store) Profiles(ctx context.Context) ([]Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM profile ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []ProfileID
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(ids))
	for _, id := range ids {
		profile, err := s.profileSQL(ctx, id)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

// SetSessionProfile rebinds a session to another Profile and always relocks it
// in the same write: every read re-derives scope from this row, so the switch
// takes effect immediately and neither Reveal nor Admin Session authority
// survives a Profile switch.
// Disabled Profiles refuse the rebind like they refuse new sessions.
func (s *Store) SetSessionProfile(ctx context.Context, id SessionID, profileID ProfileID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	profile, err := s.profileSQL(ctx, profileID)
	if err != nil {
		return err
	}
	if profile.Disabled {
		return ErrProfileDisabled
	}
	result, err := s.catalogW.ExecContext(ctx,
		`UPDATE auth_session SET profile_id = ?, revealed = 0, admin_until_ms = NULL WHERE id = ?`,
		profileID, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("session not found")
	}
	return nil
}

// Libraries returns Libraries visible under the provided filter.
func (s *Store) Libraries(ctx context.Context, filter LibraryFilter) ([]Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	all, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	libraries := make([]Library, 0, len(all))
	for _, library := range all {
		if !libraryVisible(library, filter) {
			continue
		}
		libraries = append(libraries, library)
	}
	return libraries, nil
}

// LibraryVisible reports whether the named Library is visible under filter (the
// single libraryVisible chokepoint: the AllowedIDs allow-list AND the
// restricted-Library reveal guard). It is the deletion-event delivery gate: a
// media.deleted carries its gone Media's LibraryID, and the SSE surface
// reveal-filters the event by that Library's visibility instead of a live Media
// read that would resolve the now-trashed row to not-found. An empty or unknown
// LibraryID is hidden fail-closed (no event reaches a session that cannot see
// the Library), so the deletion stream can never be an existence oracle for a
// restricted Library a session has not revealed.
func (s *Store) LibraryVisible(ctx context.Context, id LibraryID, filter LibraryFilter) (bool, error) {
	if id == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return false, err
	}
	for _, library := range libraries {
		if library.ID == id {
			return libraryVisible(library, filter), nil
		}
	}
	return false, nil // names a Library that no longer exists — fail closed
}

// LibraryPluginID returns the plugin a Library is bound to, or "" when the
// Library is unbound or unknown.
func (s *Store) LibraryPluginID(ctx context.Context, id LibraryID) (string, error) {
	if id == "" {
		return "", errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var pluginID sql.NullString
	err := s.catalogR.QueryRowContext(ctx, "SELECT plugin_id FROM library WHERE id = ?", id).Scan(&pluginID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return pluginID.String, nil
}

// LibraryFamilyScope returns the plugin-declared family scope a Library was
// created with, or "" when the Library is unknown or its scope
// is unset. Identification reads this to fix media classification
// deterministically for the Library; the value is opaque plugin data core
// never interprets.
func (s *Store) LibraryFamilyScope(ctx context.Context, id LibraryID) (string, error) {
	if id == "" {
		return "", errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var scope string
	err := s.catalogR.QueryRowContext(ctx, "SELECT family_scope FROM library WHERE id = ?", id).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return scope, nil
}

// LibraryRestricted reports whether a Library is flagged restricted.
func (s *Store) LibraryRestricted(ctx context.Context, id LibraryID) (bool, error) {
	if id == "" {
		return false, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var restricted int
	err := s.catalogR.QueryRowContext(ctx, "SELECT is_restricted FROM library WHERE id = ?", id).Scan(&restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("library not found")
	}
	if err != nil {
		return false, err
	}
	return restricted != 0, nil
}

// LibraryPreviewClips reports the Library's preview-clip toggle.
func (s *Store) LibraryPreviewClips(ctx context.Context, id LibraryID) (bool, error) {
	if id == "" {
		return false, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var enabled int
	err := s.catalogR.QueryRowContext(ctx, "SELECT preview_clips_enabled FROM library WHERE id = ?", id).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("library not found")
	}
	if err != nil {
		return false, err
	}
	return enabled != 0, nil
}

// LibraryEncrypted reports whether the Library is Encrypted,
// regardless of whether it is currently Sealed or Unsealed. A plaintext
// derivative generator (HLS precompute, managed convert) uses it to fail closed
// for Encrypted Libraries, since no encrypted-plane write path exists yet.
func (s *Store) LibraryEncrypted(ctx context.Context, id LibraryID) (bool, error) {
	if id == "" {
		return false, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var encrypted int
	err := s.catalogR.QueryRowContext(ctx, "SELECT encrypted FROM library WHERE id = ?", id).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("library not found")
	}
	if err != nil {
		return false, err
	}
	return encrypted != 0, nil
}

// SetLibraryPreviewClips flips the Library's preview-clip toggle.
func (s *Store) SetLibraryPreviewClips(ctx context.Context, id LibraryID, enabled bool) error {
	if id == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE library SET preview_clips_enabled = ? WHERE id = ?", boolToInt(enabled), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("library not found")
	}
	return nil
}

// SetLibraryAIIndexing flips the Library's AI-indexing opt-out. When
// disabled, the Enrichment Plugin must skip this Library and semantic search is
// never available for it. Operator-only; the graph layer gates the call.
func (s *Store) SetLibraryAIIndexing(ctx context.Context, id LibraryID, enabled bool) error {
	if id == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE library SET ai_indexing_enabled = ? WHERE id = ?", boolToInt(enabled), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("library not found")
	}
	return nil
}

// SetLibraryBacking sets the two orthogonal Library attributes:
// custodial (the backing-model axis — true means Beamfall holds the canonical
// copy and the durability contract applies, so the Library's trashed Media are
// never hard-purged) and encrypted (the at-rest regime is active). ENC-1 models
// the attributes and their invariants only — no key management, cipher, or
// sealing here (those are ENC-2/ENC-3). Operator-only; the graph layer gates
// the call.
func (s *Store) SetLibraryBacking(ctx context.Context, id LibraryID, custodial, encrypted bool) error {
	if id == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE library SET custodial = ?, encrypted = ? WHERE id = ?",
		boolToInt(custodial), boolToInt(encrypted), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("library not found")
	}
	return nil
}

// UpdateLibraryRestricted toggles a Library's restriction flag. The
// caller (graph.setLibraryRestricted) has already established that the acting
// session may see and reveal the Library; this is the bare write. An unknown
// (or already-purged) Library is a ValidationError, mirroring SetLibraryBacking.
func (s *Store) UpdateLibraryRestricted(ctx context.Context, id LibraryID, restricted bool) error {
	if id == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE library SET is_restricted = ? WHERE id = ?",
		boolToInt(restricted), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ValidationError("library not found")
	}
	return nil
}

// ProfilesGrantedLibrary lists the (enabled) Profiles that hold a grant to the
// Library. It is the fan-out set for a restriction-change
// invalidation: a restriction toggle changes the visible set for exactly these
// Profiles, so each gets one Profile-gated client.invalidate. Disabled Profiles
// are excluded — their sessions are auth-rejected anyway, so an event for them
// would only ever be dropped.
func (s *Store) ProfilesGrantedLibrary(ctx context.Context, libraryID LibraryID) ([]ProfileID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx,
		`SELECT pl.profile_id FROM profile_library pl
		   JOIN profile p ON p.id = pl.profile_id
		  WHERE pl.library_id = ? AND p.disabled = 0
		  ORDER BY pl.profile_id`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []ProfileID
	for rows.Next() {
		var id ProfileID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		profiles = append(profiles, id)
	}
	return profiles, rows.Err()
}

// LibraryIndexStatus is one Library's embedding/enrichment progress for a given
// embedding kind (ADR-0041 §enrichment + ADR-0042-style operator view). Indexed
// is the count of live, non-trashed Media in the Library that have an embedding
// of the requested kind; Total is the count of live Media. AIIndexing reflects
// the per-Library opt-out — when false the Library is excluded from indexing
// regardless of counts. State is a derived label (off/empty/partial/complete).
type LibraryIndexStatus struct {
	LibraryID  LibraryID
	Title      string
	Indexed    int
	Total      int
	Failures   int
	AIIndexing bool
	State      string
}

// Index-status state labels (ADR-0041 §search-mode 4-state UX). off = the
// operator opted the Library out; empty = no Media indexed yet; partial = some
// but not all indexed; complete = every live Media has an embedding.
const (
	IndexStateOff      = "off"
	IndexStateEmpty    = "empty"
	IndexStatePartial  = "partial"
	IndexStateComplete = "complete"
)

// LibraryIndexStatuses reports per-Library embedding coverage for the given
// kind, reveal-and-Library filtered like every other read. Only Libraries
// visible under filter are returned, so a restricted Library's index progress
// is never an existence oracle for an unrevealed caller. Counts are computed in
// SQL; the candidate scope is the visible set established BEFORE counting.
func (s *Store) LibraryIndexStatuses(ctx context.Context, filter LibraryFilter, kind EmbeddingKind) ([]LibraryIndexStatus, error) {
	if kind == "" {
		return nil, ValidationError("embedding kind is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	out := []LibraryIndexStatus{}
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		var total, indexed, failures int
		if err := s.catalogR.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM media WHERE library_id = ? AND trashed_at_ms IS NULL`,
			library.ID).Scan(&total); err != nil {
			return nil, err
		}
		if err := s.catalogR.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT m.id)
			FROM media m
			JOIN media_file mf ON mf.media_id = m.id
			JOIN media_embedding e ON e.media_fingerprint = mf.content_fingerprint
			WHERE m.library_id = ?
			  AND m.trashed_at_ms IS NULL
			  AND mf.missing_at_ms IS NULL
			  AND e.embedding_kind = ?`,
			library.ID, string(kind)).Scan(&indexed); err != nil {
			return nil, err
		}
		if err := s.catalogR.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM failed_enrichment f
			JOIN media m ON m.id = f.media_id
			WHERE m.library_id = ?
			  AND m.trashed_at_ms IS NULL
			  AND f.embedding_kind = ?`,
			library.ID, string(kind)).Scan(&failures); err != nil {
			return nil, err
		}
		out = append(out, LibraryIndexStatus{
			LibraryID:  library.ID,
			Title:      library.Title,
			Indexed:    indexed,
			Total:      total,
			Failures:   failures,
			AIIndexing: library.AIIndexing,
			State:      indexState(library.AIIndexing, indexed, total),
		})
	}
	return out, nil
}

// HasHiddenRestrictedLibraries reports whether any Library is hidden from the
// caller by the reveal contract (a restricted Library the filter excludes). It
// is the leak-safe signal behind search's restrictedResultsPresent (ADR-0041
// §106 / #106): a plain boolean meaning "you have locked content," never a
// title, id, or count. It says nothing about whether the current query would
// have matched hidden Media — only that hidden restricted Libraries exist.
func (s *Store) HasHiddenRestrictedLibraries(ctx context.Context, filter LibraryFilter) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return false, err
	}
	for _, library := range libraries {
		if library.IsRestricted && !libraryVisible(library, filter) {
			return true, nil
		}
	}
	return false, nil
}

// indexState derives the 4-state label from the opt-out flag and coverage.
func indexState(aiIndexing bool, indexed, total int) string {
	if !aiIndexing {
		return IndexStateOff
	}
	if indexed == 0 {
		return IndexStateEmpty
	}
	if indexed >= total {
		return IndexStateComplete
	}
	return IndexStatePartial
}

// Media returns a Media row only when it is visible under the provided filter.
func (s *Store) Media(ctx context.Context, id MediaID, filter LibraryFilter) (Media, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Media{}, err
	}
	return s.visibleMediaSQL(ctx, id, filter)
}

// MediaByIDs resolves a batch of Media ids through the caller's visibility
// filter, returning only the ids the caller can currently see, keyed by id.
// Hidden / restricted-unrevealed / out-of-filter ids are simply absent from the
// map (no error, no oracle). Used by the Station generator to resolve Collection
// seed members through the sealed-free music filter so non-music or hidden
// members never enter the candidate pool.
func (s *Store) MediaByIDs(ctx context.Context, ids []MediaID, filter LibraryFilter) (map[MediaID]Media, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.visibleMediaBatchSQL(ctx, ids, filter)
}

// MediaLibrary returns the Library a Media row belongs to, unfiltered.
// Server-internal callers only (identification's entity hydration scopes
// hydrated Entities to the identified Media's Library); reveal-filtered
// reads go through Media/SourceItems.
func (s *Store) MediaLibrary(ctx context.Context, id MediaID) (LibraryID, error) {
	if id == "" {
		return "", errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		if errors.Is(err, ValidationError("media not found")) {
			return "", errors.New("media not found")
		}
		return "", err
	}
	return plane.libraryID, nil
}

// MediaClass returns a Media row's playback class without reveal filtering.
// Server-internal callers use it when they already resolved authority through a
// separate path; reveal-filtered reads go through Media/SourceItems.
func (s *Store) MediaClass(ctx context.Context, id MediaID) (MediaClass, error) {
	if id == "" {
		return "", errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return "", err
	}
	var class MediaClass
	err = plane.reader.QueryRowContext(ctx, `SELECT media_class FROM media WHERE id = ?`, id).Scan(&class)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("media not found")
	}
	return class, err
}

// MediaLibraryID returns the Library a Media belongs to, without reveal
// filtering. The Migration Wizard uses it to scope a path-matched item to the
// operator-chosen target Library (ADR-0037 §2.2 Profile/Library mapping): a
// match whose owning Library differs from the chosen one is treated as
// unmatched for that run, so an operator can route one server's history to one
// Library rather than the whole catalog. Server-internal, like MediaClass.
func (s *Store) MediaLibraryID(ctx context.Context, id MediaID) (LibraryID, error) {
	if id == "" {
		return "", errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	var libraryID LibraryID
	err := s.catalogR.QueryRowContext(ctx, `SELECT library_id FROM media WHERE id = ?`, id).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("media not found")
	}
	return libraryID, err
}

// MediaFileExistsByFingerprint reports whether a live (non-missing) Media File
// already carries the given content fingerprint, returning the owning Media ID.
// Server-internal read for the ingest exact-duplicate skip: a
// byte-identical payload already in the catalog is never re-linked. Visibility
// gating is the caller's concern — this is a fingerprint identity check, not a
// content read.
func (s *Store) MediaFileExistsByFingerprint(ctx context.Context, fingerprint string) (MediaID, bool, error) {
	if fingerprint == "" {
		return "", false, errors.New("content fingerprint is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", false, err
	}
	var mediaID MediaID
	err := s.catalogR.QueryRowContext(ctx,
		"SELECT media_id FROM media_file WHERE content_fingerprint = ? AND missing_at_ms IS NULL",
		fingerprint,
	).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return mediaID, true, nil
}

// SetMatchPinned sets the ADR-0009 §7 import match-pin on the
// media_file identified by content fingerprint (the file's stable identity,
// catalog 0053). A pinned match is operator/import-authoritative: a later rescan
// must leave it alone rather than re-identify it. This is the minimal set path
// of the schema reservation — the column (catalog 0055 / match_pinned) is SHARED
// with PIPE-1, which wires the "Fix match" mutation on top of it; FREEZE-3 owns
// the column and this set/read pair, PIPE-1 just consumes them. The bool reports
// whether a media_file carried the fingerprint (false = no such file, nothing
// updated), mirroring MediaFileExistsByFingerprint's found-convention.
func (s *Store) SetMatchPinned(ctx context.Context, fingerprint string, pinned bool) (bool, error) {
	if fingerprint == "" {
		return false, errors.New("content fingerprint is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	result, err := s.catalogW.ExecContext(ctx,
		"UPDATE media_file SET match_pinned = ? WHERE content_fingerprint = ?",
		boolToInt(pinned), fingerprint,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// SetMediaFileDecodeBroken records the result of a decode-verify pass over one
// Media item's available bytes (FEAT-8 / ADR-0009 §9 / catalog 0064). broken=1
// means the decode-verify sampling job hit a decode error or truncation on this
// Media's playable file(s); broken=0 clears the flag when a later pass decodes
// cleanly (self-healing after a re-encode/repair). decode_checked_at_ms is set
// to checkedAt on every pass so the Console can distinguish "checked, sound"
// from "never checked" (NULL). The flag is written to every available
// (non-missing) file of the Media — a Media with a missing file is already
// broken on the "bytes are gone" branch, so a stale flag on a missing row never
// matters. Returns whether any file row was updated (false = no available file).
func (s *Store) SetMediaFileDecodeBroken(ctx context.Context, id MediaID, broken bool, checkedAt time.Time) (bool, error) {
	if id == "" {
		return false, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return false, err
	}
	result, err := plane.writer.ExecContext(ctx,
		"UPDATE media_file SET decode_broken = ?, decode_checked_at_ms = ? WHERE media_id = ? AND missing_at_ms IS NULL",
		boolToInt(broken), checkedAt.UTC().UnixMilli(), id,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// MatchPinned reports whether the media_file identified by content fingerprint
// carries the FREEZE-3 import match-pin (catalog 0055). Read side of the
// reservation PIPE-1's "Fix match" mutation consumes. The second bool reports
// whether a media_file carried the fingerprint (false = no such file).
func (s *Store) MatchPinned(ctx context.Context, fingerprint string) (bool, bool, error) {
	if fingerprint == "" {
		return false, false, errors.New("content fingerprint is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, false, err
	}
	var pinned int
	err := s.catalogR.QueryRowContext(ctx,
		"SELECT match_pinned FROM media_file WHERE content_fingerprint = ?",
		fingerprint,
	).Scan(&pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return pinned != 0, true, nil
}

// AllMediaIDs returns every Media ID, unfiltered. Server-internal callers only
// (background generation); reveal-filtered reads go through Media/SourceItems.
func (s *Store) AllMediaIDs(ctx context.Context) ([]MediaID, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `SELECT id FROM media ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MediaIDsAfter returns at most limit Media IDs after the previous checkpoint
// ID. It is the keyset-paginated form used by resumable background sweeps.
func (s *Store) MediaIDsAfter(ctx context.Context, after MediaID, limit int) ([]MediaID, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id FROM media
		WHERE (? = '' OR id > ?)
		ORDER BY id
		LIMIT ?
	`, string(after), string(after), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]MediaID, 0, limit)
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MediaIDsForLibrary lists every Media id in one Library from the Library's
// own catalog plane: the global catalog for a plaintext
// Library, or the per-Library encrypted DB when the Library is Custodial +
// Encrypted and Unsealed. It is a server-internal full-Library walk (the bulk
// photo-export job, ADR-0045 §2.9 mode 2), so it carries no reveal filtering —
// the caller owns visibility. A Sealed Encrypted Library has no readable plane
// and returns ErrEncryptedLibraryDBDisabled, which the export treats as "absent
// until Unsealed". IDs are returned in stable rowid order so re-runs and
// checkpoints see a deterministic sequence.
func (s *Store) MediaIDsForLibrary(ctx context.Context, libraryID LibraryID) ([]MediaID, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT id FROM media
		WHERE library_id = ? AND trashed_at_ms IS NULL
		ORDER BY rowid
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []MediaID{}
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetPrimaryMediaFile records the current file path backing a Media item.
func (s *Store) SetPrimaryMediaFile(ctx context.Context, id MediaID, path string) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if path == "" {
		return errors.New("media file path is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.catalogPlaneForMediaLocked(ctx, id); err != nil {
		return err
	}
	fingerprint, err := HashMediaFileContent(path)
	if err != nil {
		return err
	}
	file := MediaFile{MediaID: id, Path: path, ContentFingerprint: fingerprint}
	return s.upsertMediaFile(ctx, file, time.Now().UTC())
}

// StoredFingerprint is the cheap-triple identity a scan reuses when a file is
// unchanged: the content fingerprint last computed for the file at Path, plus
// the (size, mtime) that hash was taken at. OK is false when no available
// (non-missing) row exists for the path.
type StoredFingerprint struct {
	ContentFingerprint string
	SizeBytes          int64
	ModTime            time.Time
	OK                 bool
}

// MediaFileFingerprintByPath returns the stored content fingerprint and the
// (size, mtime) it was hashed at for an available file at path (the scan
// fast-path). A scan that stat()s an unchanged (path, size, mtime) reuses the
// stored fingerprint instead of re-hashing the whole file — ADR-0009 content
// identity is unaffected (a changed size/mtime makes OK false on the triple
// check, forcing a re-hash). Server-internal, like the other file-plane reads.
func (s *Store) MediaFileFingerprintByPath(ctx context.Context, path string) (StoredFingerprint, error) {
	if path == "" {
		return StoredFingerprint{}, errors.New("path is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return StoredFingerprint{}, err
	}
	var fp StoredFingerprint
	var mtimeMS int64
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT content_fingerprint, size_bytes, mtime_ms
		FROM media_file
		WHERE path = ? AND missing_at_ms IS NULL
	`, path).Scan(&fp.ContentFingerprint, &fp.SizeBytes, &mtimeMS)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredFingerprint{}, nil
	}
	if err != nil {
		return StoredFingerprint{}, err
	}
	fp.ModTime = millisToTime(mtimeMS)
	fp.OK = true
	return fp, nil
}

// PrimaryMediaFile returns the file backing a visible Media item. With 1:N
// files (ADR-0009 §2 Versions/Parts) this is the first available file in
// (VersionLabel, PartOrdinal) order — the default Version's first Part. It is
// the backstop for callers that don't negotiate a Version (fingerprint lookups,
// asset routing); playback uses MediaFilesForMedia to pick the best Version.
func (s *Store) PrimaryMediaFile(ctx context.Context, id MediaID, filter LibraryFilter) (MediaFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaFile{}, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return MediaFile{}, err
	}
	return s.primaryMediaFileSQL(ctx, id)
}

// MediaFilesForMedia returns every available file backing a visible Media item,
// ordered by (VersionLabel, PartOrdinal) — so files group into Versions and the
// Parts of each Version are in timeline order. A single-file
// Media returns one file (default Version "", PartOrdinal 0), identical to
// PrimaryMediaFile. Playback negotiates the best Version for the client over
// this set before deciding direct-play vs transcode.
func (s *Store) MediaFilesForMedia(ctx context.Context, id MediaID, filter LibraryFilter) ([]MediaFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	if _, err := s.visibleMediaSQL(ctx, id, filter); err != nil {
		return nil, err
	}
	return s.mediaFilesForMediaSQL(ctx, id)
}

// MediaFilesForLibrary returns every live file in a Library, unfiltered. This
// is a server-internal scan/maintenance read; caller-owned reveal-filtered
// reads go through SourceItems/MediaFilesForMedia.
func (s *Store) MediaFilesForLibrary(ctx context.Context, libraryID LibraryID) ([]MediaFile, error) {
	if libraryID == "" {
		return nil, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	return s.mediaFilesForLibrarySQL(ctx, plane.reader, libraryID)
}

// ForEachMediaFileForLibrary streams every live file in a Library without
// materializing the whole result set.
func (s *Store) ForEachMediaFileForLibrary(ctx context.Context, libraryID LibraryID, yield func(MediaFile) error) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if yield == nil {
		return errors.New("yield is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return err
	}
	return s.forEachMediaFileForLibrarySQL(ctx, plane.reader, libraryID, yield)
}

// MarkMissingMediaFilesNotSeenSince marks live files under root missing when a
// completed full scan did not refresh them.
func (s *Store) MarkMissingMediaFilesNotSeenSince(ctx context.Context, libraryID LibraryID, root string, since time.Time) error {
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	if root == "" {
		return errors.New("scan root is required")
	}
	if since.IsZero() {
		return errors.New("scan start is required")
	}
	root = filepath.Clean(root)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return err
	}
	prefix := root + string(filepath.Separator)
	likePrefix := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	_, err = plane.writer.ExecContext(ctx, `
		UPDATE media_file
		SET missing_at_ms = ?
		WHERE missing_at_ms IS NULL
		  AND last_seen_at_ms < ?
		  AND (path = ? OR path LIKE ? ESCAPE '\')
		  AND media_id IN (SELECT id FROM media WHERE library_id = ?)
	`, timeToMillis(time.Now().UTC()), timeToMillis(since), root, likePrefix, libraryID)
	return err
}

// MediaFingerprints returns the content-identity values for one Media item's
// primary file (scraper variable substitution, ADR-0027 D2 §7). This is a
// server-internal read like MediaLibrary — visibility gating belongs to the
// caller. A Media item whose file went missing still resolves, with empty
// hashes.
func (s *Store) MediaFingerprints(ctx context.Context, id MediaID) (MediaFingerprints, error) {
	if id == "" {
		return MediaFingerprints{}, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return MediaFingerprints{}, err
	}
	return s.mediaFingerprintsSQL(ctx, id)
}

// MediaFileStreams returns the persisted per-stream inventory for one content
// fingerprint, in ffprobe stream order. The inventory is a
// property of the bytes, keyed by fingerprint, so this is a server-internal
// read like MediaFingerprints — visibility gating belongs to the caller. An
// empty fingerprint, or one with no recorded streams, returns an empty slice.
func (s *Store) MediaFileStreams(ctx context.Context, fingerprint string) ([]MediaFileStream, error) {
	if fingerprint == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.mediaFileStreamsSQL(ctx, fingerprint)
}

// MediaFileSidecarCaptions returns the sidecar caption tracks recorded for one
// content fingerprint, ordered by relative path so the
// track list is stable across reads. Like MediaFileStreams, the sidecars are a
// property of the bytes keyed by fingerprint, so this is a server-internal read
// — visibility gating belongs to the caller. An empty fingerprint, or one with
// no recorded sidecars, returns an empty slice.
func (s *Store) MediaFileSidecarCaptions(ctx context.Context, fingerprint string) ([]MediaFileSidecarCaption, error) {
	if fingerprint == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	return s.mediaFileSidecarCaptionsSQL(ctx, fingerprint)
}

// ImportMediaFile upserts a Media row and primary Media File by content fingerprint.
func (s *Store) ImportMediaFile(ctx context.Context, imported ImportMediaFile) (Media, MediaFile, error) {
	// Authority face: an Attached node identifies its bytes
	// locally but never authors the catalog Media row — it index-pushes instead.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return Media{}, MediaFile{}, err
	}
	if err := validateImportMediaFile(imported); err != nil {
		return Media{}, MediaFile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Media{}, MediaFile{}, err
	}
	now := time.Now().UTC()
	plane, err := s.catalogPlaneForLibraryLocked(ctx, imported.LibraryID)
	if err != nil {
		return Media{}, MediaFile{}, err
	}
	media, file, created, err := s.importMediaFileSQL(ctx, plane, imported, now)
	if err != nil {
		return Media{}, MediaFile{}, err
	}
	if plane.encrypted {
		return media, file, nil
	}
	// Emit the realtime event AFTER the catalog write committed (cross-DB
	// ordering: see appendMediaEventLocked). A failed outbox write must not fail
	// the import — the Media is already persisted; the worst case is a missed
	// live push that the next read reconciles.
	kind := MediaEventUpdated
	if created {
		kind = MediaEventCreated
	}
	if eventErr := s.appendMediaEventLocked(ctx, kind, media.ID, nil, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", kind, "media", media.ID, "error", eventErr)
	}
	return media, file, nil
}

// ImportMediaFileParts upserts one logical Media backed by many ordered Parts
// in the default Version. Server-internal scan path only: parser-confirmed
// grouping resolves the authority; caller-owned reads remain reveal-filtered.
func (s *Store) ImportMediaFileParts(ctx context.Context, parts []ImportMediaFilePart) (Media, []MediaFile, error) {
	// Authority face: catalog Media-row authoring is main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return Media{}, nil, err
	}
	if len(parts) == 0 {
		return Media{}, nil, errors.New("media file parts are required")
	}
	ordinals := map[int]bool{}
	for _, part := range parts {
		if part.SourceOrdinal <= 0 {
			return Media{}, nil, errors.New("part source ordinal is required")
		}
		if ordinals[part.SourceOrdinal] {
			return Media{}, nil, errors.New("part source ordinals must be unique")
		}
		ordinals[part.SourceOrdinal] = true
		if err := validateImportMediaFile(part.File); err != nil {
			return Media{}, nil, err
		}
		if part.File.LibraryID != parts[0].File.LibraryID {
			return Media{}, nil, errors.New("media file parts must belong to one library")
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Media{}, nil, err
	}
	now := time.Now().UTC()
	plane, err := s.catalogPlaneForLibraryLocked(ctx, parts[0].File.LibraryID)
	if err != nil {
		return Media{}, nil, err
	}
	media, files, created, err := s.importMediaFilePartsSQL(ctx, plane, parts, now)
	if err != nil {
		return Media{}, nil, err
	}
	if plane.encrypted {
		return media, files, nil
	}
	kind := MediaEventUpdated
	if created {
		kind = MediaEventCreated
	}
	if eventErr := s.appendMediaEventLocked(ctx, kind, media.ID, nil, now); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", kind, "media", media.ID, "error", eventErr)
	}
	return media, files, nil
}

func validateImportMediaFile(imported ImportMediaFile) error {
	if imported.LibraryID == "" {
		return errors.New("library ID is required")
	}
	if imported.Path == "" {
		return errors.New("media file path is required")
	}
	if imported.ContentFingerprint == "" {
		return errors.New("content fingerprint is required")
	}
	if imported.Title == "" {
		return errors.New("media title is required")
	}
	if imported.Container == "" {
		return errors.New("media container is required")
	}
	// A still image has no video/audio codec — it is served as direct
	// bytes, never probed for streams — so an empty codec is valid for it. Every
	// other class must carry a codec.
	if imported.Codec == "" && imported.Class != MediaClassImage {
		return errors.New("media codec is required")
	}
	return nil
}

// AddMediaFileVersion attaches an additional file to an existing Media as a
// Version and/or Part: file.VersionLabel names the alternate
// encode (a non-"" label is a distinct Version to negotiate), file.PartOrdinal
// orders it within that Version's timeline. The default (VersionLabel "",
// PartOrdinal 0) slot belongs to ImportMediaFile's primary file; this is the
// write path for the second-and-later files of a 1:N Media. streams is the
// optional per-stream inventory for the file's bytes (nil leaves none).
// Server-internal like ImportMediaFile: the caller resolves visibility.
func (s *Store) AddMediaFileVersion(ctx context.Context, mediaID MediaID, file MediaFile, streams []MediaFileStream) error {
	if mediaID == "" {
		return errors.New("media ID is required")
	}
	if file.Path == "" {
		return errors.New("media file path is required")
	}
	if file.ContentFingerprint == "" {
		return errors.New("content fingerprint is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	file.MediaID = mediaID
	if file.LastSeenAt.IsZero() {
		file.LastSeenAt = time.Now().UTC()
	}
	return s.addMediaFileVersionSQL(ctx, file, streams)
}

// UpdateMediaMetadata applies a partial descriptive-metadata write to one
// Media row. Server-internal (like ImportMediaFile): visibility gating
// belongs to the calling surface, which must have already resolved the Media
// through a reveal-filtered read.
func (s *Store) UpdateMediaMetadata(ctx context.Context, id MediaID, metadata MediaMetadata) error {
	if id == "" {
		return errors.New("media ID is required")
	}
	if metadata.Title == nil && metadata.Summary == nil && metadata.Year == nil {
		return errors.New("no metadata fields to update")
	}
	if metadata.Title != nil && strings.TrimSpace(*metadata.Title) == "" {
		return errors.New("media title cannot be empty")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if err := s.updateMediaMetadataSQL(ctx, id, metadata); err != nil {
		return err
	}
	// Emit AFTER the catalog write committed (cross-DB ordering: see
	// appendMediaEventLocked). The write already succeeded; a failed outbox
	// append must not surface as a metadata-update failure.
	if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, id, nil, time.Now().UTC()); eventErr != nil {
		slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", id, "error", eventErr)
	}
	return nil
}

// SourceItems returns visible Media rows for a bounded manifest Source.
func (s *Store) SourceItems(ctx context.Context, filter LibraryFilter, query SourceQuery) ([]SourceItem, error) {
	switch query.Kind {
	case "", "recently-added", "all-items", "on-this-day", "continue-watching", "next-up", "recently-played", "most-played", "top-tracks", "top-artists", "top-albums", "listening-summary", "queue", "entity-items", "entity-media", "random", "resume-progress", "availability", "watched-by-any-profile", "directory", "smart-collection":
	default:
		return []SourceItem{}, nil
	}

	// A library-scoped request narrows the visible set to that Library; a
	// Library outside the session's allowlist yields nothing.
	if query.LibraryID != "" {
		// A zero-grant filter (empty allow-list, not the all-Libraries sentinel)
		// admits nothing — a library-scoped query must not widen it (H4).
		if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
			return []SourceItem{}, nil
		}
		if len(filter.AllowedIDs) > 0 && !slices.Contains(filter.AllowedIDs, query.LibraryID) {
			return []SourceItem{}, nil
		}
		filter.AllowedIDs = []LibraryID{query.LibraryID}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	items, err := s.sourceItemsForKind(ctx, filter, query, limit, offset)
	if err != nil {
		return nil, err
	}
	// Per-Profile Mute: an implicit negative Source predicate
	// applied here, at Source evaluation, so every passive-discovery rail above
	// is filtered uniformly. It is deliberately NOT applied in SearchMedia or
	// any direct media fetch — a muted target stays searchable and playable
	// (§2.2). A profile-less rail (e.g. an unauthenticated all-items scan) has no
	// mute list to apply, so it passes through unchanged.
	if query.ProfileID != "" && strings.TrimSpace(query.Query) == "" {
		items, err = s.applyMuteFilterLocked(ctx, query.ProfileID, filter, items)
		if err != nil {
			return nil, err
		}
	}
	if err := s.annotateRestrictedLocked(ctx, nil, items); err != nil {
		return nil, err
	}
	if !filter.PlaybackGate {
		if err := s.annotateLockedLocked(ctx, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// sourceItemsForKind dispatches one bounded manifest Source kind to its SQL
// projection. It runs under the read lock SourceItems holds; the per-Profile
// Mute predicate is layered on its result by SourceItems, not here.
func (s *Store) sourceItemsForKind(ctx context.Context, filter LibraryFilter, query SourceQuery, limit, offset int) ([]SourceItem, error) {
	switch query.Kind {
	case "smart-collection":
		return s.smartCollectionSourceItemsLocked(ctx, filter, query, limit, offset)
	case "continue-watching":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.continueWatchingSQL(ctx, query.ProfileID, filter, limit, offset)
	case "next-up":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.nextUpSQL(ctx, query.ProfileID, filter, limit, offset)
	case "recently-played":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.playedItemsSQL(ctx, query.ProfileID, filter, "last_played_at_ms DESC, media_id", limit, offset)
	case "most-played":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.playedItemsSQL(ctx, query.ProfileID, filter, "play_count DESC, last_played_at_ms DESC, media_id", limit, offset)
	case "top-tracks", "top-artists", "top-albums", "listening-summary":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.listeningStatsSourceItems(ctx, query.ProfileID, filter, query, limit, offset)
	case "queue":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.queueItemsSQL(ctx, query.ProfileID, filter, limit, offset)
	case "all-items":
		if text := strings.TrimSpace(query.Query); text != "" {
			return s.archiveSearchMediaLocked(ctx, filter, text, limit, offset)
		}
		return s.allItemsSQL(ctx, filter, query.FieldFilters, query.EntityFilter, limit, offset)
	case "on-this-day":
		return s.onThisDayItemsSQL(ctx, filter, limit, offset)
	case "random":
		return s.randomItemsSQL(ctx, filter, query.Seed, limit, offset)
	case "resume-progress":
		if query.ProfileID == "" {
			return []SourceItem{}, nil
		}
		return s.resumeProgressSQL(ctx, query.ProfileID, query.Value, filter, limit, offset)
	case "availability":
		return s.availabilitySQL(ctx, query.Value, filter, limit, offset)
	case "watched-by-any-profile":
		return s.watchedByAnyProfileSQL(ctx, query.Value, filter, limit, offset)
	case "directory":
		return []SourceItem{}, nil
	case "entity-items":
		if query.EntityKind == "" {
			return []SourceItem{}, nil
		}
		return s.entityItemsFilteredSQL(ctx, filter, query.EntityKind, query.EntityFieldFilters, limit, offset)
	case "entity-media":
		if query.EntityID == "" {
			return []SourceItem{}, nil
		}
		return s.mediaForEntitySQL(ctx, query.EntityID, filter, limit, offset)
	}
	return s.sourceItemsSQL(ctx, filter, query.FieldFilters, query.EntityFilter, limit, offset)
}

func (s *Store) smartCollectionSourceItemsLocked(ctx context.Context, filter LibraryFilter, query SourceQuery, limit, offset int) ([]SourceItem, error) {
	id := SmartCollectionID(strings.TrimSpace(query.Value))
	if id == "" {
		return []SourceItem{}, nil
	}
	var collection SmartCollection
	var createdAtMS int64
	err := s.catalogR.QueryRowContext(ctx, `
		SELECT id, library_id, name, source_expr, sort_expr, created_at_ms
		FROM smart_collection WHERE id = ?
	`, id).Scan(&collection.ID, &collection.LibraryID, &collection.Name, &collection.SourceExpr, &collection.SortExpr, &createdAtMS)
	if err != nil {
		return []SourceItem{}, nil
	}
	if len(filter.AllowedIDs) == 0 && !filter.AllLibraries {
		return []SourceItem{}, nil
	}
	if len(filter.AllowedIDs) > 0 && !slices.Contains(filter.AllowedIDs, collection.LibraryID) {
		return []SourceItem{}, nil
	}
	filter.AllowedIDs = []LibraryID{collection.LibraryID}
	filter.AllLibraries = false
	source, err := decodeSmartCollectionSourceExpr(collection.SourceExpr)
	if err != nil {
		return nil, err
	}
	if source.LibraryID != "" && source.LibraryID != collection.LibraryID {
		return nil, ValidationError("smart collection source libraryID must match collection library")
	}
	source.LibraryID = collection.LibraryID
	source.ProfileID = query.ProfileID
	return s.sourceItemsForKind(ctx, filter, source, limit, offset)
}

// libraryVisible is the single Library-visibility chokepoint. Two independent
// gates, evaluated in order:
//
//  1. AllowedIDs allow-list: a populated slice admits only its members. An
//     empty/nil AllowedIDs admits nothing UNLESS AllLibraries is set (the
//     explicit "every Library" sentinel — see the LibraryFilter type doc). This
//     gate fails CLOSED on an empty, non-sentinel filter (H4): a zero-grant
//     session sees no Libraries.
//  2. restricted-Library guard: a restricted Library is hidden unless the
//     filter is Revealed. This gate is fail-CLOSED and is the actual reveal
//     boundary; it holds independently of gate 1.
//  3. Sealed-Library guard: a Library named in SealedIDs is hidden
//     unconditionally (ADR-0098 area 2 — "a Sealed Library serves NOTHING").
//     This gate is fail-CLOSED and independent of gates 1 and 2: it hides a
//     sealed Library even when AllowedIDs/AllLibraries admit it, even when the
//     filter is Revealed, and even when the Library is NOT restricted (Sealed
//     and IsRestricted are orthogonal). Sealed state is runtime/in-memory, so
//     the caller stamps SealedIDs from the encryption seam before the read.
func libraryVisible(library Library, filter LibraryFilter) bool {
	if len(filter.AllowedIDs) == 0 {
		if !filter.AllLibraries {
			return false
		}
	} else if !slices.Contains(filter.AllowedIDs, library.ID) {
		return false
	}
	if library.IsRestricted && !filter.Revealed {
		return false
	}
	if len(filter.SealedIDs) > 0 && slices.Contains(filter.SealedIDs, library.ID) {
		return false
	}
	return true
}

// restrictedLibraryIDsLocked returns the set of restricted-Library IDs. Library
// metadata (including is_restricted) lives in the catalog plane even for
// encrypted Libraries, so a single catalog read is authoritative.
func (s *Store) restrictedLibraryIDsLocked(ctx context.Context) (map[LibraryID]bool, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	out := make(map[LibraryID]bool)
	for _, library := range libraries {
		if library.IsRestricted {
			out[library.ID] = true
		}
	}
	return out, nil
}

// annotateRestrictedLocked stamps SourceItem.Restricted from the owning
// Library's is_restricted flag. It is the single shared tail-pass used by every
// store method that returns []SourceItem to a browse/search path
// (SourceItems, SearchMedia, SearchEntities, MediaForEntity), so no emitter is
// missed. Items are resolved to their Library across ALL planes (catalog +
// encrypted) in at most two queries per plane (one entity batch plus one media
// batch), so encrypted-Library items are covered without a per-item plane-router
// lookup. It is additive: it never changes which items are returned (reveal
// filtering already happened upstream in libraryVisible) — it only annotates
// items the caller is already lawfully serving. Pass a pre-loaded restricted
// set when the caller already has one; pass nil to load it here.
func (s *Store) annotateRestrictedLocked(ctx context.Context, restricted map[LibraryID]bool, items []SourceItem) error {
	if len(items) == 0 {
		return nil
	}
	if restricted == nil {
		var err error
		restricted, err = s.restrictedLibraryIDsLocked(ctx)
		if err != nil {
			return err
		}
	}
	if len(restricted) == 0 {
		return nil
	}
	pending := make(map[sourceItemIdentity][]int, len(items))
	for i := range items {
		kind := "media"
		if items[i].Kind == "entity" {
			kind = "entity"
		}
		key := sourceItemIdentity{kind: kind, id: items[i].ID}
		pending[key] = append(pending[key], i)
	}
	annotateRestrictedFromPlane(ctx, s.catalogR, restricted, items, pending)
	for _, db := range s.encryptedDBs {
		if len(pending) == 0 {
			break
		}
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		annotateRestrictedFromPlane(ctx, db.reader, restricted, items, pending)
	}
	return nil
}

type sourceItemIdentity struct {
	kind string
	id   MediaID
}

type sourceItemLibraryQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// annotateRestrictedFromPlane resolves every still-pending item against one
// catalog plane. Query/scan failures deliberately remain non-fatal: the old
// per-item router treated an unresolvable item as Restricted=false, and this
// batch pass preserves that defensive contract.
func annotateRestrictedFromPlane(ctx context.Context, db sourceItemLibraryQueryer, restricted map[LibraryID]bool, items []SourceItem, pending map[sourceItemIdentity][]int) {
	for _, kind := range []string{"entity", "media"} {
		ids := make([]MediaID, 0, len(pending))
		for key := range pending {
			if key.kind == kind {
				ids = append(ids, key.id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		placeholders := make([]string, len(ids))
		args := make([]any, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := db.QueryContext(ctx,
			`SELECT id, library_id FROM `+kind+` WHERE id IN (`+strings.Join(placeholders, ", ")+`)`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id MediaID
			var libraryID LibraryID
			if err := rows.Scan(&id, &libraryID); err != nil {
				break
			}
			key := sourceItemIdentity{kind: kind, id: id}
			for _, index := range pending[key] {
				if restricted[libraryID] {
					items[index].Restricted = true
				}
			}
			delete(pending, key)
		}
		_ = rows.Close()
	}
}

func mediaDurationSummary(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	minutes := seconds / 60
	if seconds%60 != 0 {
		minutes++
	}
	if minutes <= 1 {
		return "1 min"
	}
	return strconv.Itoa(minutes) + " min"
}

// mediaIDForFingerprint derives the durable Media ID from a content fingerprint.
// The fingerprint is "sha256:<hex>"; after the separator is mapped to "-" the
// 24-character truncation keeps "media-sha256-" plus 17 hex digits (~68 bits of
// the digest). Birthday collisions at 68 bits are ~n^2/2^69 — about 1 in 3e7
// even at ten million Media items — which is acceptable for a per-server
// catalog. The truncated form is identity-bearing and persisted: do not widen
// or change it, or existing Media IDs would silently fork.
func mediaIDForFingerprint(fingerprint string) MediaID {
	safe := strings.ToLower(fingerprint)
	safe = strings.NewReplacer(":", "-", "/", "-", "\\", "-", " ", "-").Replace(safe)
	if len(safe) > 24 {
		safe = safe[:24]
	}
	return MediaID("media-" + safe)
}

func (s *Store) allocateEncryptedMediaIDLocked(ctx context.Context) (MediaID, error) {
	for attempt := 0; attempt < 32; attempt++ {
		id, err := s.randomEncryptedMediaIDLocked()
		if err != nil {
			return "", err
		}
		exists, err := s.mediaIDExistsAnywhereLocked(ctx, id)
		if err != nil {
			return "", err
		}
		if !exists {
			return id, nil
		}
	}
	return "", errors.New("could not allocate encrypted media ID")
}

func (s *Store) randomEncryptedMediaIDLocked() (MediaID, error) {
	if s.testEncryptedMediaID != "" {
		id := s.testEncryptedMediaID
		s.testEncryptedMediaID = ""
		return id, nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return MediaID("media-enc-" + hex.EncodeToString(raw[:])), nil
}

func (s *Store) mediaIDExistsAnywhereLocked(ctx context.Context, id MediaID) (bool, error) {
	var one int
	err := s.catalogR.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, id).Scan(&one)
	if err == nil {
		return true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	for _, db := range s.encryptedDBs {
		if db == nil || db.state != "unsealed-registered" || db.reader == nil {
			continue
		}
		err = db.reader.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, id).Scan(&one)
		if err == nil {
			return true, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

// HashDeviceSecret converts a device secret into its stored verifier.
func HashDeviceSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// HashMediaFilePath provides a deterministic placeholder fingerprint until import hashing lands.
func HashMediaFilePath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashMediaFileContent returns the content fingerprint used for Media identity.
func HashMediaFileContent(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// SearchMedia returns Media whose titles match the FTS query, reveal-filtered
// (ADR-0011: FTS results never name restricted content to unrevealed scopes).
// The query is matched as a prefix of each word so type-ahead search works.
func (s *Store) SearchMedia(ctx context.Context, filter LibraryFilter, query string, limit int) ([]SourceItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []SourceItem{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	items, err := s.fuzzySearchMediaLocked(ctx, filter, query, limit)
	if err != nil {
		return nil, err
	}
	if err := s.annotateRestrictedLocked(ctx, nil, items); err != nil {
		return nil, err
	}
	if !filter.PlaybackGate {
		if err := s.annotateLockedLocked(ctx, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// CreateLibrary inserts a Library and grants it to the creating Profile so it
// appears in that Profile's scope immediately. The identifier derives from
// the title; collisions get a numeric suffix.
func (s *Store) CreateLibrary(ctx context.Context, title string, restricted bool, pluginID string, grantTo ProfileID) (Library, error) {
	return s.CreateLibraryWithRoots(ctx, title, restricted, pluginID, grantTo, nil, "")
}

// CreateLibraryWithRoots inserts a Library plus its persisted scan roots in
// one catalog transaction. Roots are manager-only state and never ride the
// Navigation Manifest. familyScope is the plugin-declared family scope chosen
// at creation, stored verbatim — "" leaves it unset.
func (s *Store) CreateLibraryWithRoots(ctx context.Context, title string, restricted bool, pluginID string, grantTo ProfileID, roots []string, familyScope string) (Library, error) {
	// Authority face: Library catalog rows are main-only (a Library
	// is bound to its home node, but the binding is main-authored — decision 4).
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return Library{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Library{}, ValidationError("library title is required")
	}
	if grantTo == "" {
		return Library{}, ValidationError("profile ID is required")
	}
	normalizedRoots, err := normalizeLibraryRoots(roots)
	if err != nil {
		return Library{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Library{}, err
	}
	if exists, err := s.profileExistsSQL(ctx, grantTo); err != nil {
		return Library{}, err
	} else if !exists {
		return Library{}, ValidationError("profile not found")
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	base := libraryIDForTitle(title)
	id := base
	for suffix := 2; ; suffix++ {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM library WHERE id = ?`, id).Scan(&count); err != nil {
			return Library{}, err
		}
		if count == 0 {
			break
		}
		id = LibraryID(string(base) + "-" + strconv.Itoa(suffix))
	}
	var pluginValue any
	if pluginID != "" {
		pluginValue = pluginID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO library(id, title, plugin_id, is_restricted, family_scope) VALUES (?, ?, ?, ?, ?)`,
		id, title, pluginValue, boolToInt(restricted), familyScope); err != nil {
		return Library{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO profile_library(profile_id, library_id, position)
		VALUES (?, ?, COALESCE((SELECT MAX(position) + 1 FROM profile_library WHERE profile_id = ?), 0))
	`, grantTo, id, grantTo); err != nil {
		return Library{}, err
	}
	for _, root := range normalizedRoots {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_roots(library_id, root) VALUES (?, ?)`, id, root); err != nil {
			return Library{}, err
		}
	}
	schedule := defaultScanSchedule(id, time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schedules(id, job_kind, trigger_kind, every_n, every_unit, enabled, orphaned, last_completed_enqueue_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.ID, schedule.JobKind, schedule.Trigger.Kind, schedule.Trigger.N, schedule.Trigger.Unit,
		boolToInt(schedule.Enabled), boolToInt(schedule.Orphaned), timeToMillis(schedule.LastCompletedEnqueue)); err != nil {
		return Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	// PreviewClips echoes the schema default (ON, ADR-0033 §4); FamilyScope
	// echoes the value just written.
	library := Library{ID: id, Title: title, IsRestricted: restricted, PreviewClips: true, FamilyScope: familyScope}
	if pluginID != "" {
		library.PluginID = &pluginID
	}
	return library, nil
}

// DeleteLibrary soft-deletes a Library by stamping trashed_at_ms (master #42).
// The Library is hidden from all loadLibraries queries immediately; its media
// rows, scan schedules, and profile grants are preserved for a future purge.
// Returns an error when the library does not exist or is already trashed.
func (s *Store) DeleteLibrary(ctx context.Context, id LibraryID) error {
	if id == "" {
		return ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	res, err := s.catalogW.ExecContext(ctx,
		`UPDATE library SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ValidationError("library not found or already deleted")
	}
	return nil
}

// libraryIDForTitle slugs a title into a stable library identifier.
func libraryIDForTitle(title string) LibraryID {
	var b strings.Builder
	b.WriteString("library-")
	lastDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return LibraryID(strings.TrimSuffix(b.String(), "-"))
}

// CreateProfile inserts a Profile and grants it Libraries from the creating
// scope: adult Profiles inherit every Library the creator can currently see;
// managed Profiles get only the unrestricted ones (ADR-0004 — no reveal path
// means restricted Libraries must never be granted to managed Profiles).
func (s *Store) CreateProfile(ctx context.Context, name string, kind ProfileKind, creator LibraryFilter) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Profile{}, ValidationError("profile name is required")
	}
	if kind != ProfileAdult && kind != ProfileManaged {
		return Profile{}, ValidationError("profile kind must be adult or managed")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return Profile{}, err
	}
	var grants []LibraryID
	for _, library := range libraries {
		if !libraryVisible(library, creator) {
			continue
		}
		if kind == ProfileManaged && library.IsRestricted {
			continue
		}
		grants = append(grants, library.ID)
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	base := profileIDForName(name)
	id := base
	for suffix := 2; ; suffix++ {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM profile WHERE id = ?`, id).Scan(&count); err != nil {
			return Profile{}, err
		}
		if count == 0 {
			break
		}
		id = ProfileID(string(base) + "-" + strconv.Itoa(suffix))
	}
	createdAt := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO profile(id, name, kind, created_at_ms) VALUES (?, ?, ?, ?)`, id, name, kind, createdAt); err != nil {
		return Profile{}, err
	}
	for position, libraryID := range grants {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO profile_library(profile_id, library_id, position) VALUES (?, ?, ?)
		`, id, libraryID, position); err != nil {
			return Profile{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	// Echo the schema defaults so callers see the stored truth without a re-read.
	return Profile{ID: id, Name: name, Kind: kind, Library: grants, TranscodeAllowed: true, SubtitleMode: SubtitleModeOff, CreatedAtMs: createdAt}, nil
}

// SetProfileOwner designates id as the single owner Profile: it
// clears is_owner on every other Profile and sets it on id, atomically, so the
// single-owner invariant holds through a transfer. id must name an existing
// adult Profile — managed Profiles have no admin path and can never own. Both
// first-run designation (pairing.Bootstrap) and ownership transfer route here;
// the operation is idempotent.
func (s *Store) SetProfileOwner(ctx context.Context, id ProfileID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind ProfileKind
	err = tx.QueryRowContext(ctx, `SELECT kind FROM profile WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ValidationError("profile not found")
	}
	if err != nil {
		return err
	}
	if kind != ProfileAdult {
		return ValidationError("only an adult profile may be the owner")
	}
	// Clear the prior owner first so setting id never collides with the
	// partial-unique index within the transaction.
	if _, err := tx.ExecContext(ctx, `UPDATE profile SET is_owner = 0 WHERE is_owner = 1 AND id <> ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profile SET is_owner = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ProfileIsOwner reports whether id is the designated owner Profile. An unknown
// Profile reports false (not an error): the caller is the admin gate, which
// must fail closed on a missing Profile.
func (s *Store) ProfileIsOwner(ctx context.Context, id ProfileID) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var isOwner int
	err := s.catalogR.QueryRowContext(ctx, `SELECT is_owner FROM profile WHERE id = ?`, id).Scan(&isOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isOwner != 0, nil
}

// OwnerDesignated reports whether any Profile holds the owner flag. The admin
// gate uses this for its lockout-safety fallback: a server with
// no designated owner — e.g. one upgraded across migration 0069 — admits any
// adult Profile + admin password exactly as before, so the upgrade can never
// brick Admin access.
func (s *Store) OwnerDesignated(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var count int
	if err := s.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM profile WHERE is_owner = 1`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateProfileSettings applies a partial Profile policy write (admission,
// transcode, and track-preference fields) and returns the updated Profile.
// Disabling a Profile never touches its engagement history — only future
// session admission and pickers are affected.
func (s *Store) UpdateProfileSettings(ctx context.Context, id ProfileID, settings ProfileSettings) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	if settings.empty() {
		return Profile{}, ValidationError("no profile settings to update")
	}
	if err := settings.validate(); err != nil {
		return Profile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	profile, err := s.profileSQL(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if settings.Name != nil {
		profile.Name = strings.TrimSpace(*settings.Name)
	}
	if settings.Kind != nil {
		if *settings.Kind != ProfileAdult && profile.IsOwner {
			return Profile{}, ValidationError("cannot change owner profile to managed; transfer ownership to another adult profile first")
		}
		profile.Kind = *settings.Kind
	}
	if settings.MaxConcurrentStreams != nil {
		if *settings.MaxConcurrentStreams == 0 {
			profile.MaxConcurrentStreams = nil // 0 clears the cap back to unlimited
		} else {
			limit := *settings.MaxConcurrentStreams
			profile.MaxConcurrentStreams = &limit
		}
	}
	if settings.TranscodeAllowed != nil {
		profile.TranscodeAllowed = *settings.TranscodeAllowed
	}
	if settings.Disabled != nil {
		// Lockout-safety: the owner Profile is the only Profile that
		// can open an Admin Session once one is designated; a disabled Profile
		// refuses new sessions, so disabling the owner would strand Admin once its
		// sessions lapse (no profile delete exists, and the gate still denies
		// non-owner adults). Refuse — ownership must be transferred first.
		if *settings.Disabled && profile.IsOwner {
			return Profile{}, ValidationError("cannot disable the owner profile; transfer ownership to another adult profile first")
		}
		profile.Disabled = *settings.Disabled
	}
	if settings.AudioLanguages != nil {
		profile.AudioLanguages = trimmedLanguages(*settings.AudioLanguages)
	}
	if settings.SubtitleLanguages != nil {
		profile.SubtitleLanguages = trimmedLanguages(*settings.SubtitleLanguages)
	}
	if settings.SubtitleMode != nil {
		profile.SubtitleMode = *settings.SubtitleMode
	}
	if settings.PreferSDH != nil {
		profile.PreferSDH = *settings.PreferSDH
	}
	if settings.IntegrationSignIn != nil {
		profile.IntegrationSignIn = *settings.IntegrationSignIn
	}
	if err := s.updateProfileSettingsSQL(ctx, profile); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// SetProfileLibraries replaces a Profile's Library grants. Managed Profiles
// cannot receive restricted Libraries because they have no Reveal path.
func (s *Store) SetProfileLibraries(ctx context.Context, id ProfileID, libraryIDs []LibraryID) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	profile, err := s.profileSQL(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return Profile{}, err
	}
	byID := make(map[LibraryID]Library, len(libraries))
	for _, library := range libraries {
		byID[library.ID] = library
	}
	for _, libraryID := range libraryIDs {
		library, ok := byID[libraryID]
		if !ok {
			return Profile{}, ValidationError("library not found")
		}
		if profile.Kind == ProfileManaged && library.IsRestricted {
			return Profile{}, ValidationError("managed profile cannot be granted restricted libraries")
		}
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_library WHERE profile_id = ?`, id); err != nil {
		return Profile{}, err
	}
	seen := map[LibraryID]bool{}
	position := 0
	for _, libraryID := range libraryIDs {
		if seen[libraryID] {
			continue
		}
		seen[libraryID] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO profile_library(profile_id, library_id, position) VALUES (?, ?, ?)`,
			id, libraryID, position); err != nil {
			return Profile{}, err
		}
		position++
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// trimmedLanguages normalizes a validated language list; empty lists store as nil.
func trimmedLanguages(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	trimmed := make([]string, len(list))
	for i, language := range list {
		trimmed[i] = strings.TrimSpace(language)
	}
	return trimmed
}

// profileIDForName slugs a display name into a stable profile identifier.
func profileIDForName(name string) ProfileID {
	return ProfileID("profile-" + strings.TrimPrefix(string(libraryIDForTitle(name)), "library-"))
}
