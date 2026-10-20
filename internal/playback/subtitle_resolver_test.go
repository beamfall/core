package playback

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func muxed(index int, lang string, forced, sdh bool) CaptionTrack {
	i := index
	return CaptionTrack{ID: "muxed", StreamIndex: &i, Language: lang, Forced: forced, SDH: sdh}
}

func sidecar(lang string, forced, sdh bool) CaptionTrack {
	return CaptionTrack{ID: "sidecar", URL: "/api/media/sidecar/t/0.vtt", Language: lang, Forced: forced, SDH: sdh}
}

func prefsFor(mode store.SubtitleMode, langs []string, preferSDH bool) TrackPreferences {
	return TrackPreferences{SubtitleLanguages: langs, SubtitleMode: string(mode), PreferSDH: preferSDH}
}

// Mode "off" never pre-selects, whatever tracks exist.
func TestResolveModeOffSelectsNothing(t *testing.T) {
	got := resolveSubtitleTrack([]CaptionTrack{muxed(2, "en", false, false)},
		prefsFor(store.SubtitleModeOff, []string{"en"}, false), "en", nil)
	if got.selection != nil {
		t.Fatalf("selection = %+v, want none for mode off", got.selection)
	}
}

// Mode "always" picks the preferred-language full track and stamps the
// format-independent language+kind+role ref (never a stream index).
func TestResolveAlwaysPicksPreferredLanguage(t *testing.T) {
	tracks := []CaptionTrack{
		muxed(2, "fr", false, false),
		muxed(3, "en", false, false),
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeAlways, []string{"en", "fr"}, false), "ja", nil)
	if got.selection == nil {
		t.Fatal("selection = nil, want the English track")
	}
	if got.selection.Track.Language != "en" {
		t.Fatalf("language = %q, want en", got.selection.Track.Language)
	}
	if got.selection.Track.Kind != SubtitleTrackKindMuxed {
		t.Fatalf("kind = %q, want muxed", got.selection.Track.Kind)
	}
	if got.selection.Reason == "" {
		t.Fatal("reason is empty, want a human-readable reason")
	}
}

// preferSDH is an orthogonal bool: within the same language it pulls the SDH
// track ahead, and it never becomes a mode of its own.
func TestResolvePreferSDHIsOrthogonal(t *testing.T) {
	tracks := []CaptionTrack{
		muxed(2, "en", false, false),
		muxed(3, "en", false, true),
	}
	sdhOn := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeAlways, []string{"en"}, true), "ja", nil)
	if sdhOn.selection == nil || !sdhOn.selection.Track.Roles.SDH {
		t.Fatalf("preferSDH=true selection = %+v, want the SDH track", sdhOn.selection)
	}
	sdhOff := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeAlways, []string{"en"}, false), "ja", nil)
	if sdhOff.selection == nil || sdhOff.selection.Track.Roles.SDH {
		t.Fatalf("preferSDH=false selection = %+v, want the non-SDH track", sdhOff.selection)
	}
}

// Semantic 3: forced-only with no preferred-language forced track selects NONE
// and surfaces a subtle non-blocking notice — never a wrong-language track.
func TestResolveForcedOnlyWithoutPreferredForcedSelectsNoneWithNotice(t *testing.T) {
	tracks := []CaptionTrack{
		muxed(2, "de", true, false),  // forced, but not a preferred language
		muxed(3, "en", false, false), // preferred language, but not forced
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeForcedOnly, []string{"en"}, false), "en", nil)
	if got.selection != nil {
		t.Fatalf("selection = %+v, want none (never a wrong-language forced track)", got.selection)
	}
	if got.unavailableReason == "" {
		t.Fatal("unavailableReason is empty, want a subtle non-blocking notice")
	}
}

func TestResolveForcedOnlyPicksPreferredForced(t *testing.T) {
	tracks := []CaptionTrack{muxed(2, "de", true, false), muxed(3, "en", true, false)}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeForcedOnly, []string{"en"}, false), "en", nil)
	if got.selection == nil || got.selection.Track.Language != "en" || !got.selection.Track.Roles.Forced {
		t.Fatalf("selection = %+v, want the forced English track", got.selection)
	}
	if got.unavailableReason != "" {
		t.Fatalf("unavailableReason = %q, want empty when a forced track was selected", got.unavailableReason)
	}
}

// Smart mode shows full subtitles for foreign-language audio, and falls back to
// forced-only signage when the audio is already in a preferred language. The
// presence of a second audio track never kills subtitles (competitor regression).
func TestResolveSmartKeysOnAudioLanguage(t *testing.T) {
	tracks := []CaptionTrack{muxed(2, "en", false, false), muxed(3, "en", true, false)}
	prefs := prefsFor(store.SubtitleModeSmart, []string{"en"}, false)

	foreign := resolveSubtitleTrack(tracks, prefs, "ja", nil)
	if foreign.selection == nil || foreign.selection.Track.Roles.Forced {
		t.Fatalf("foreign-audio selection = %+v, want the full English track", foreign.selection)
	}
	native := resolveSubtitleTrack(tracks, prefs, "en", nil)
	if native.selection == nil || !native.selection.Track.Roles.Forced {
		t.Fatalf("native-audio selection = %+v, want the forced signage track", native.selection)
	}
}

// Semantic 4: the pinned series record beats the profile-pref policy — here the
// pin selects a track the "off" mode would never have chosen.
func TestResolvePinnedRecordBeatsProfilePrefs(t *testing.T) {
	tracks := []CaptionTrack{muxed(2, "en", false, false), muxed(3, "fr", false, false)}
	pinned := &store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "fr", SubKind: SubtitleTrackKindMuxed,
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeOff, []string{"en"}, false), "en", pinned)
	if got.selection == nil || got.selection.Track.Language != "fr" {
		t.Fatalf("selection = %+v, want the pinned French track despite mode off", got.selection)
	}
}

// A pin to "subtitles off" selects none even when mode would have selected one.
func TestResolvePinnedOffSelectsNothing(t *testing.T) {
	tracks := []CaptionTrack{muxed(2, "en", false, false)}
	pinned := &store.SubtitleChoiceRecord{GroupKey: "entity:series-1", Selected: false}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeAlways, []string{"en"}, false), "ja", pinned)
	if got.selection != nil {
		t.Fatalf("selection = %+v, want none for a subtitles-off pin", got.selection)
	}
}

// The pin matches on the format-independent tuple, so it still resolves when the
// next episode carries the same language+role as a SIDECAR instead of a muxed
// stream (the identity is stable across renditions).
func TestResolvePinnedMatchesAcrossKindChange(t *testing.T) {
	tracks := []CaptionTrack{sidecar("fr", false, false)}
	pinned := &store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "fr", SubKind: SubtitleTrackKindMuxed,
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeOff, nil, false), "en", pinned)
	if got.selection == nil || got.selection.Track.Kind != SubtitleTrackKindSidecar {
		t.Fatalf("selection = %+v, want the sidecar French track", got.selection)
	}
}

// A pin cannot conjure a track the episode does not carry.
func TestResolvePinnedAbsentTrackSelectsNothing(t *testing.T) {
	tracks := []CaptionTrack{muxed(2, "en", false, false)}
	pinned := &store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "ja", SubKind: SubtitleTrackKindMuxed,
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeOff, nil, false), "en", pinned)
	if got.selection != nil {
		t.Fatalf("selection = %+v, want none when the pinned language is absent", got.selection)
	}
}

// Commentary and visual-impaired tracks are never auto-selected.
func TestResolveNeverAutoSelectsCommentaryOrVisualImpaired(t *testing.T) {
	tracks := []CaptionTrack{
		{ID: "a", Language: "en", Commentary: true},
		{ID: "b", Language: "en", VisualImpaired: true},
	}
	got := resolveSubtitleTrack(tracks, prefsFor(store.SubtitleModeAlways, []string{"en"}, false), "ja", nil)
	if got.selection != nil {
		t.Fatalf("selection = %+v, want none: commentary/VI are never auto-selected", got.selection)
	}
	// With a real dialogue track alongside them, that track is chosen.
	withDialogue := append(tracks, muxed(4, "en", false, false))
	picked := resolveSubtitleTrack(withDialogue, prefsFor(store.SubtitleModeAlways, []string{"en"}, false), "ja", nil)
	if picked.selection == nil || picked.selection.Track.Roles.Commentary || picked.selection.Track.Roles.VisualImpaired {
		t.Fatalf("selection = %+v, want the plain dialogue track", picked.selection)
	}
}

// Language matching is prefix-tolerant so "en" prefs match an "en-US" track.
func TestLangEqPrefixTolerant(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"en", "en", true},
		{"EN", "en", true},
		{"en-US", "en", true},
		{"en", "en-US", true},
		{"en", "fr", false},
		{"", "en", false},
	} {
		if got := langEq(tc.a, tc.b); got != tc.want {
			t.Errorf("langEq(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// A pin's fallback tiers match on LANGUAGE, not on role, so they are
// auto-selection under another name. Pinning a plain English track on one item
// must not hand the viewer a commentary track on the next item in the group just
// because it is that item's only English caption.
func TestResolvePinnedFallbackNeverSelectsCommentaryOrVisualImpaired(t *testing.T) {
	// Kind differs from the pin, so the exact tier misses and the fallback tiers
	// are the ones under test.
	plainEnglishPin := &store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "en", SubKind: SubtitleTrackKindMuxed,
	}
	for _, tc := range []struct {
		name  string
		track CaptionTrack
	}{
		{"commentary, tier 2 (SDH matches the pin)", CaptionTrack{ID: "c", Language: "en", Commentary: true}},
		{"visual-impaired, tier 2", CaptionTrack{ID: "v", Language: "en", VisualImpaired: true}},
		{"commentary, tier 3 (SDH differs from the pin)", CaptionTrack{ID: "c-sdh", Language: "en", Commentary: true, SDH: true}},
		{"visual-impaired, tier 3", CaptionTrack{ID: "v-sdh", Language: "en", VisualImpaired: true, SDH: true}},
	} {
		got := resolveSubtitleTrack([]CaptionTrack{tc.track}, prefsFor(store.SubtitleModeOff, nil, false), "en", plainEnglishPin)
		if got.selection != nil {
			t.Errorf("%s: selection = %+v, want none — a plain-language pin never falls back onto it", tc.name, got.selection)
		}
	}

	// A pin that NAMES the commentary role still resolves to it: that is the
	// viewer's own choice, matched by the exact tier, not an auto-selection.
	commentaryPin := &store.SubtitleChoiceRecord{
		GroupKey: "entity:series-1", Selected: true,
		SubLanguage: "en", SubKind: SubtitleTrackKindSidecar, SubCommentary: true,
	}
	commentary := CaptionTrack{ID: "c", URL: "/api/media/sidecar/t/0.vtt", Language: "en", Commentary: true}
	got := resolveSubtitleTrack([]CaptionTrack{commentary}, prefsFor(store.SubtitleModeOff, nil, false), "en", commentaryPin)
	if got.selection == nil || !got.selection.Track.Roles.Commentary {
		t.Fatalf("explicit commentary pin = %+v, want the commentary track", got.selection)
	}

	// With a plain dialogue track alongside the commentary, the fallback picks it.
	withDialogue := []CaptionTrack{{ID: "c", Language: "en", Commentary: true}, sidecar("en", false, false)}
	picked := resolveSubtitleTrack(withDialogue, prefsFor(store.SubtitleModeOff, nil, false), "en", plainEnglishPin)
	if picked.selection == nil || picked.selection.Track.Roles.Commentary {
		t.Fatalf("pin fallback = %+v, want the plain dialogue track", picked.selection)
	}
}

// A bitmap sidecar the client declares no renderer for is not a track it can
// use: text sidecars are projected to WebVTT for everyone, bitmap sidecars are
// served raw and need the family declared.
func TestSidecarRenderableGatesBitmapFormatsOnly(t *testing.T) {
	webProfile := []string{"vtt", "srt"}
	for _, format := range []string{"srt", "vtt", "ass", "ssa"} {
		if !sidecarRenderable(format, webProfile) {
			t.Errorf("sidecarRenderable(%q, web) = false, want true — text sidecars are projected to WebVTT", format)
		}
	}
	for _, format := range []string{"sup", "idx", "sub"} {
		if sidecarRenderable(format, webProfile) {
			t.Errorf("sidecarRenderable(%q, web) = true, want false — a web profile cannot draw a bitmap sidecar", format)
		}
	}
	if !sidecarRenderable("sup", []string{"vtt", "PGS"}) {
		t.Error("sidecarRenderable(sup, pgs-capable) = false, want true")
	}
	if !sidecarRenderable("idx", []string{"vobsub"}) {
		t.Error("sidecarRenderable(idx, vobsub-capable) = false, want true")
	}
	if sidecarRenderable("sup", []string{"vobsub"}) {
		t.Error("sidecarRenderable(sup, vobsub-only) = true, want false — the family must match")
	}
}

// When the capability filter withholds every caption track, the absence is
// STATED. The §3 property is "never silent": an empty caption menu with no
// reason leaves the viewer thinking the item has no subtitles at all.
func TestResolveSubtitlesStatesWithheldCaptions(t *testing.T) {
	withheld := noticeUnrenderableCaptions(subtitleResolution{}, 2, true)
	if withheld.unavailableReason != "no-client-renderable-subtitle-track" {
		t.Fatalf("unavailableReason = %q, want the withheld-caption notice", withheld.unavailableReason)
	}
	// Nothing withheld: no notice is manufactured.
	if got := noticeUnrenderableCaptions(subtitleResolution{}, 0, true); got.unavailableReason != "" {
		t.Fatalf("unavailableReason = %q, want empty when nothing was withheld", got.unavailableReason)
	}
	// A track was still selected: the notice would contradict it.
	selected := subtitleResolution{selection: &SelectedSubtitleTrack{Reason: "preferred-language subtitles"}}
	if got := noticeUnrenderableCaptions(selected, 1, true); got.unavailableReason != "" {
		t.Fatalf("unavailableReason = %q, want empty when a track was selected", got.unavailableReason)
	}
	// A more specific reason already resolved is never overwritten.
	forced := subtitleResolution{unavailableReason: "no-forced-subtitle-in-preferred-language"}
	if got := noticeUnrenderableCaptions(forced, 1, true); got.unavailableReason != "no-forced-subtitle-in-preferred-language" {
		t.Fatalf("unavailableReason = %q, want the more specific existing reason", got.unavailableReason)
	}
}

func TestResolveSubtitlesWithheldCaptionsHonorsOffState(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	ctx := context.Background()
	dec := decision{
		media:                store.Media{ID: "media-demo-1"},
		unrenderableCaptions: 1,
	}

	t.Run("mode off", func(t *testing.T) {
		got := service.resolveSubtitles(ctx, scope, dec, prefsFor(store.SubtitleModeOff, []string{"en"}, false))
		if got.unavailableReason != "" {
			t.Fatalf("unavailableReason = %q, want empty when subtitles are off", got.unavailableReason)
		}
	})

	t.Run("requested", func(t *testing.T) {
		got := service.resolveSubtitles(ctx, scope, dec, prefsFor(store.SubtitleModeAlways, []string{"en"}, false))
		if got.unavailableReason != "no-client-renderable-subtitle-track" {
			t.Fatalf("unavailableReason = %q, want the withheld-caption notice", got.unavailableReason)
		}
	})

	groupKey, err := service.store.MediaSubtitleGroup(ctx, dec.media.ID)
	if err != nil {
		t.Fatalf("MediaSubtitleGroup: %v", err)
	}
	if err := service.store.PutSubtitleChoice(ctx, scope.ProfileID, store.SubtitleChoiceRecord{
		GroupKey: groupKey,
		Selected: false,
	}, service.now()); err != nil {
		t.Fatalf("PutSubtitleChoice: %v", err)
	}

	t.Run("pinned off", func(t *testing.T) {
		got := service.resolveSubtitles(ctx, scope, dec, prefsFor(store.SubtitleModeAlways, []string{"en"}, false))
		if got.unavailableReason != "" {
			t.Fatalf("unavailableReason = %q, want empty for a subtitles-off pin", got.unavailableReason)
		}
	})
}
