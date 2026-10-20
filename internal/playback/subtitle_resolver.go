package playback

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Server subtitle pre-selection. The server resolves and stamps
// the authoritative-at-start SelectedSubtitleTrack from the item's caption
// tracks, the Profile's track-preference policy, the audio track language, and
// any pinned series-scoped choice record. The client-local resolver remains the
// sole authority for mid-session switches and offline playback — this value is a
// starting recommendation, not a lock.
//
// Precedence (highest first): the pinned series choice record beats a later
// profile-pref change (semantic 4); absent a pin, the Profile's SubtitleMode
// policy resolves. preferSDH is an orthogonal bool, never a mode (semantic 5).

// subtitleResolution is the resolver's output: the pre-selected track (nil when
// no subtitle is selected) and a non-blocking notice reason when subtitles the
// viewer would want are not present (e.g. forced-only with no matching forced
// track — semantic 3).
type subtitleResolution struct {
	selection         *SelectedSubtitleTrack
	unavailableReason string
}

// resolveSubtitleTrack applies the ADR-0194 §3 selection semantics. tracks are
// the item's caption tracks (muxed + sidecar), prefs the Profile policy,
// audioTrackLang the language of the audio being played (mandatory — the
// wrong-language-forced case cannot be expressed without it), and pinned the
// series-scoped choice record when one exists.
func resolveSubtitleTrack(tracks []CaptionTrack, prefs TrackPreferences, audioTrackLang string, pinned *store.SubtitleChoiceRecord) subtitleResolution {
	if pinned != nil {
		return resolvePinned(tracks, *pinned)
	}
	return resolveByPolicy(tracks, prefs, audioTrackLang)
}

// resolvePinned honours a series-scoped pinned choice (semantic 1/4). A pin to
// "subtitles off" selects none; a pinned track selects the best available match
// of its language+kind+role tuple. When this item genuinely lacks the pinned
// track (a match on language cannot be found), no track is forced — the pin
// cannot conjure a track that is not present.
func resolvePinned(tracks []CaptionTrack, pinned store.SubtitleChoiceRecord) subtitleResolution {
	if !pinned.Selected {
		return subtitleResolution{}
	}
	if track, ok := matchPinnedTrack(tracks, pinned); ok {
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: "pinned choice for this series"}}
	}
	return subtitleResolution{}
}

// matchPinnedTrack finds the caption track that best matches a pinned tuple:
// first an exact ref match (language + kind + all roles), then a language+SDH
// match (a rendition may differ in kind or forced flag), then a language match.
//
// Only the EXACT tier may land on a commentary or visual-impaired track, because
// there the pin names those roles — it is the viewer's own choice. The two
// fallback tiers match on language alone, so they are auto-selection under
// another name: pinning a plain English track on one item in a group must not
// hand the viewer a commentary track on the next item merely because it is that
// item's only English caption.
func matchPinnedTrack(tracks []CaptionTrack, pinned store.SubtitleChoiceRecord) (CaptionTrack, bool) {
	for _, t := range tracks {
		if langEq(t.Language, pinned.SubLanguage) && string(trackKindOf(t)) == pinned.SubKind &&
			t.Forced == pinned.SubForced && t.SDH == pinned.SubSDH &&
			t.VisualImpaired == pinned.SubVisualImpaired && t.Commentary == pinned.SubCommentary {
			return t, true
		}
	}
	for _, t := range tracks {
		if autoSelectable(t) && langEq(t.Language, pinned.SubLanguage) && t.SDH == pinned.SubSDH {
			return t, true
		}
	}
	for _, t := range tracks {
		if autoSelectable(t) && langEq(t.Language, pinned.SubLanguage) {
			return t, true
		}
	}
	return CaptionTrack{}, false
}

// autoSelectable reports whether a track may be chosen without the viewer having
// named it. Commentary and visual-impaired tracks are excluded everywhere
// auto-selection happens: presenting a director's commentary as if it were the
// subtitle track is worse than presenting none.
func autoSelectable(t CaptionTrack) bool {
	return !t.Commentary && !t.VisualImpaired
}

// resolveByPolicy resolves from the Profile's SubtitleMode when no pin exists.
func resolveByPolicy(tracks []CaptionTrack, prefs TrackPreferences, audioTrackLang string) subtitleResolution {
	switch store.SubtitleMode(prefs.SubtitleMode) {
	case store.SubtitleModeOff:
		return subtitleResolution{}
	case store.SubtitleModeForcedOnly:
		return resolveForcedOnly(tracks, prefs.SubtitleLanguages)
	case store.SubtitleModeSmart:
		return resolveSmart(tracks, prefs, audioTrackLang)
	case store.SubtitleModeAlways:
		return resolveAlways(tracks, prefs)
	default:
		// An unset/unknown mode defaults to the conservative "off" — the server
		// never forces subtitles a Profile did not ask for.
		return subtitleResolution{}
	}
}

// resolveForcedOnly selects a forced track in a preferred language (semantic 3).
// With no preferred-language forced track, it selects none and surfaces a subtle
// non-blocking notice — never a wrong-language track, never silent.
func resolveForcedOnly(tracks []CaptionTrack, langPrefs []string) subtitleResolution {
	if track, ok := bestForced(tracks, langPrefs); ok {
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: "forced subtitles in your preferred language"}}
	}
	return subtitleResolution{unavailableReason: "no-forced-subtitle-in-preferred-language"}
}

// resolveSmart shows subtitles only when the audio is NOT in a preferred
// language (foreign-audio comprehension), the "smart" competitor behaviour — but
// it never kills subtitles merely because a second audio track exists: the
// decision keys on the audio being PLAYED, not on track count. When the audio is
// already preferred, forced-only signage still applies for foreign inserts.
func resolveSmart(tracks []CaptionTrack, prefs TrackPreferences, audioTrackLang string) subtitleResolution {
	if audioTrackLang != "" && languagePreferred(audioTrackLang, prefs.SubtitleLanguages) {
		return resolveForcedOnly(tracks, prefs.SubtitleLanguages)
	}
	if track, ok := bestFull(tracks, prefs); ok {
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: "subtitles for foreign-language audio"}}
	}
	return resolveForcedOnly(tracks, prefs.SubtitleLanguages)
}

// resolveAlways always selects the best full subtitle track (semantic: always
// on), falling back to a forced track and finally to any available track.
func resolveAlways(tracks []CaptionTrack, prefs TrackPreferences) subtitleResolution {
	if track, ok := bestFull(tracks, prefs); ok {
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: subtitleReason(track, prefs)}}
	}
	if track, ok := bestForced(tracks, prefs.SubtitleLanguages); ok {
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: "forced subtitles (no full subtitles available)"}}
	}
	// Last resort: any dialogue track, in any language. Commentary and
	// visual-impaired tracks stay excluded even here — auto-selecting a director's
	// commentary as if it were subtitles is worse than showing none.
	for _, track := range tracks {
		if !autoSelectable(track) {
			continue
		}
		return subtitleResolution{selection: &SelectedSubtitleTrack{Track: trackRefOf(track), Reason: "only available subtitle track"}}
	}
	return subtitleResolution{}
}

// bestFull selects the best NON-forced subtitle track for full-dialogue display:
// preferred-language order first, preferSDH breaking ties toward SDH. Commentary
// and visual-impaired tracks are never auto-selected.
func bestFull(tracks []CaptionTrack, prefs TrackPreferences) (CaptionTrack, bool) {
	return pickBest(tracks, prefs.SubtitleLanguages, prefs.PreferSDH, false)
}

// bestForced selects the best forced track in a preferred language.
func bestForced(tracks []CaptionTrack, langPrefs []string) (CaptionTrack, bool) {
	return pickBest(tracks, langPrefs, false, true)
}

// pickBest ranks candidate tracks and returns the top one. forced selects the
// forced/non-forced family; a preferred-language track always beats a
// non-preferred one, and within a language preferSDH pulls an SDH track ahead.
// Commentary and visual-impaired tracks are excluded from auto-selection.
func pickBest(tracks []CaptionTrack, langPrefs []string, preferSDH, forced bool) (CaptionTrack, bool) {
	best := CaptionTrack{}
	bestRank := struct {
		found   bool
		langIdx int
		sdh     bool
	}{}
	for _, t := range tracks {
		if !autoSelectable(t) || t.Forced != forced {
			continue
		}
		langIdx := languageRank(t.Language, langPrefs)
		if langIdx < 0 && forced {
			continue // a forced track only auto-selects in a preferred language
		}
		candidate := struct {
			found   bool
			langIdx int
			sdh     bool
		}{true, langIdx, t.SDH}
		if !bestRank.found || betterRank(candidate.langIdx, candidate.sdh, bestRank.langIdx, bestRank.sdh, preferSDH) {
			best, bestRank = t, candidate
		}
	}
	return best, bestRank.found
}

// betterRank reports whether candidate (langIdx a, sdh sa) outranks the current
// best (langIdx b, sdh sb). A lower language index wins first; -1 (not in the
// preference list) ranks after every listed language. Within the same language,
// preferSDH pulls an SDH track ahead, otherwise a non-SDH track leads.
func betterRank(a int, sa bool, b int, sb bool, preferSDH bool) bool {
	if normLangIdx(a) != normLangIdx(b) {
		return normLangIdx(a) < normLangIdx(b)
	}
	if sa == sb {
		return false
	}
	if preferSDH {
		return sa // sa==true beats sb==false
	}
	return sb // non-SDH (sa==false) beats SDH (sb==true)
}

// normLangIdx maps the "not preferred" sentinel (-1) to a large index so it
// sorts after every listed language.
func normLangIdx(idx int) int {
	if idx < 0 {
		return 1 << 30
	}
	return idx
}

// subtitleReason describes why a full track was auto-selected, for the wire's
// human-readable Reason field.
func subtitleReason(track CaptionTrack, prefs TrackPreferences) string {
	if prefs.PreferSDH && track.SDH {
		return "preferred-language SDH subtitles"
	}
	if languageRank(track.Language, prefs.SubtitleLanguages) >= 0 {
		return "preferred-language subtitles"
	}
	return "default subtitles"
}

// languageRank returns the 0-based index of lang in prefs (lower = stronger
// preference), or -1 when it is not a preferred language. Matching is
// case-insensitive and prefix-tolerant so "en" matches "en-US".
func languageRank(lang string, prefs []string) int {
	for i, p := range prefs {
		if langEq(lang, p) {
			return i
		}
	}
	return -1
}

// languagePreferred reports whether lang is in the preference list.
func languagePreferred(lang string, prefs []string) bool {
	return languageRank(lang, prefs) >= 0
}

// langEq compares two language tags case-insensitively and prefix-tolerantly, so
// "en" matches "en", "EN", and "en-US" (and vice versa).
func langEq(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return a == b
	}
	return a == b || strings.HasPrefix(a, b+"-") || strings.HasPrefix(b, a+"-")
}

// resolveSubtitles loads the decision's pinned series choice (when the item has
// one) and resolves the server pre-selection. A store failure is never fatal to
// playback: the pre-selection is an additive recommendation, so an unreadable
// pin degrades to policy resolution rather than failing the whole Info call.
func (s *Service) resolveSubtitles(ctx context.Context, scope auth.Scope, dec decision, prefs TrackPreferences) subtitleResolution {
	resolved := subtitleResolution{}
	var pinned *store.SubtitleChoiceRecord
	hasCaptionCandidates := len(dec.captionTracks) > 0 || dec.unrenderableCaptions > 0
	if hasCaptionCandidates {
		pinned = s.pinnedSubtitleChoice(ctx, scope, dec.media.ID)
	}
	if len(dec.captionTracks) > 0 {
		resolved = resolveSubtitleTrack(dec.captionTracks, prefs, dec.audioLanguage, pinned)
	}
	return noticeUnrenderableCaptions(resolved, dec.unrenderableCaptions, subtitlesRequested(prefs, pinned))
}

func subtitlesRequested(prefs TrackPreferences, pinned *store.SubtitleChoiceRecord) bool {
	if pinned != nil {
		return pinned.Selected
	}
	switch store.SubtitleMode(prefs.SubtitleMode) {
	case store.SubtitleModeForcedOnly, store.SubtitleModeSmart, store.SubtitleModeAlways:
		return true
	default:
		return false
	}
}

// noticeUnrenderableCaptions carries the §3 "never silent" property across the
// capability filter. When an item's only caption tracks were withheld because
// this client cannot render their format, and nothing else was selected, the
// viewer is told why rather than being shown an empty caption menu with no
// explanation. Deliberately disabled subtitles produce no notice, and a more
// specific reason already resolved is never overwritten.
func noticeUnrenderableCaptions(resolved subtitleResolution, unrenderable int, requested bool) subtitleResolution {
	if !requested {
		return resolved
	}
	if unrenderable == 0 {
		return resolved
	}
	if resolved.selection != nil {
		return resolved
	}
	if resolved.unavailableReason != "" {
		return resolved
	}
	resolved.unavailableReason = "no-client-renderable-subtitle-track"
	return resolved
}

// pinnedSubtitleChoice returns the series-scoped pinned choice record for this
// Profile and item, or nil when none is pinned (or it cannot be read).
func (s *Service) pinnedSubtitleChoice(ctx context.Context, scope auth.Scope, mediaID store.MediaID) *store.SubtitleChoiceRecord {
	if mediaID == "" || scope.ProfileID == "" {
		return nil
	}
	groupKey, err := s.store.MediaSubtitleGroup(ctx, mediaID)
	if err != nil {
		return nil
	}
	record, found, err := s.store.SubtitleChoice(ctx, scope.ProfileID, groupKey)
	if err != nil || !found {
		return nil
	}
	return &record
}

// firstAudioLanguage returns the language tag of the first audio stream in the
// chosen file — the audio the viewer hears absent a client-side switch, and the
// audioTrackLang input to the resolver. Empty when the file has no audio stream
// or the probe recorded no language.
func firstAudioLanguage(streams []store.MediaFileStream) string {
	for _, stream := range streams {
		if stream.Type == "audio" {
			return stream.Language
		}
	}
	return ""
}

// trackKindOf reports a caption track's format-independent kind: muxed when it
// carries a stream index, sidecar otherwise.
func trackKindOf(t CaptionTrack) string {
	if t.StreamIndex != nil {
		return SubtitleTrackKindMuxed
	}
	return SubtitleTrackKindSidecar
}

// trackRefOf projects a caption track onto its format-independent
// language+kind+role identity.
func trackRefOf(t CaptionTrack) SubtitleTrackRef {
	return SubtitleTrackRef{
		Language: t.Language,
		Kind:     trackKindOf(t),
		Roles: SubtitleTrackRoles{
			Forced:         t.Forced,
			SDH:            t.SDH,
			VisualImpaired: t.VisualImpaired,
			Commentary:     t.Commentary,
		},
	}
}
