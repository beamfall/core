package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

const (
	subtitleSelectionCorpusDir       = "testdata/subtitle_selection"
	subtitleSelectionManifest        = "testdata/subtitle_selection/manifest.sha256"
	subtitleSelectionCaseCount       = 15
	subtitleSelectionRegressionCount = 7
)

var subtitleSelectionRegressionIDs = map[string]bool{
	"regression-sdh-beats-default-and-forced":         true,
	"regression-forced-not-auto-enabling":             true,
	"regression-smart-mode-kills-subs-on-multi-audio": true,
	"regression-sidecars-ignore-mode":                 true,
	"regression-wrong-language-forced-fallback":       true,
	"regression-next-episode-only-memory":             true,
	"regression-selection-lost-on-abr-switch":         true,
}

type subtitleSelectionFixture struct {
	ID            string                       `json:"id"`
	Regression    bool                         `json:"regression"`
	EventSequence bool                         `json:"eventSequence,omitempty"`
	Notes         string                       `json:"notes"`
	Input         subtitleSelectionInput       `json:"input"`
	Expected      subtitleSelectionExpectation `json:"expected"`
}

type subtitleSelectionInput struct {
	Mode           string                   `json:"mode"`
	LangPrefs      []string                 `json:"langPrefs"`
	PreferSDH      bool                     `json:"preferSDH"`
	AudioTrackLang string                   `json:"audioTrackLang"`
	Tracks         []subtitleSelectionTrack `json:"tracks"`
	PerItemRecord  *subtitleSelectionChoice `json:"perItemRecord"`
}

type subtitleSelectionTrack struct {
	Language       string `json:"language"`
	Kind           string `json:"kind"`
	Forced         bool   `json:"forced"`
	SDH            bool   `json:"sdh"`
	VisualImpaired bool   `json:"visualImpaired"`
	Commentary     bool   `json:"commentary"`
}

type subtitleSelectionChoice struct {
	Selected       bool   `json:"selected"`
	Language       string `json:"language"`
	Kind           string `json:"kind"`
	Forced         bool   `json:"forced"`
	SDH            bool   `json:"sdh"`
	VisualImpaired bool   `json:"visualImpaired"`
	Commentary     bool   `json:"commentary"`
}

type subtitleSelectionExpectation struct {
	Selection         *playback.SelectedSubtitleTrack `json:"selection"`
	UnavailableReason string                          `json:"unavailableReason"`
}

func TestSubtitleSelectionCorpus(t *testing.T) {
	fixtures := loadSubtitleSelectionFixtures(t)
	if len(fixtures) != subtitleSelectionCaseCount {
		t.Fatalf("fixture count = %d, want %d", len(fixtures), subtitleSelectionCaseCount)
	}

	seenIDs := make(map[string]bool, len(fixtures))
	seenRegressions := make(map[string]bool, subtitleSelectionRegressionCount)
	for _, fixture := range fixtures {
		if fixture.ID == "" {
			t.Fatal("fixture has empty id")
		}
		if seenIDs[fixture.ID] {
			t.Fatalf("duplicate fixture id %q", fixture.ID)
		}
		seenIDs[fixture.ID] = true
		if strings.TrimSpace(fixture.Input.AudioTrackLang) == "" {
			t.Fatalf("fixture %q omits mandatory audioTrackLang", fixture.ID)
		}
		if fixture.Regression {
			seenRegressions[fixture.ID] = true
		}
	}
	if !reflect.DeepEqual(seenRegressions, subtitleSelectionRegressionIDs) {
		t.Fatalf("regression ids = %v, want %v", sortedKeys(seenRegressions), sortedKeys(subtitleSelectionRegressionIDs))
	}
}

func TestSubtitleSelectionResolverConformance(t *testing.T) {
	t.Parallel()
	for _, fixture := range loadSubtitleSelectionFixtures(t) {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			t.Parallel()
			service, scope, mediaID := subtitleSelectionService(t, fixture)
			assertSubtitleSelection(t, fixture, service, scope, mediaID)
			if fixture.EventSequence {
				assertSubtitleSelection(t, fixture, service, scope, mediaID)
			}
		})
	}
}

func loadSubtitleSelectionFixtures(t *testing.T) []subtitleSelectionFixture {
	t.Helper()
	pins, err := parseSHA256Manifest(subtitleSelectionManifest)
	if err != nil {
		t.Fatalf("read subtitle-selection manifest: %v", err)
	}
	if len(pins) != subtitleSelectionCaseCount {
		t.Fatalf("manifest entries = %d, want %d", len(pins), subtitleSelectionCaseCount)
	}

	names, err := filepath.Glob(filepath.Join(subtitleSelectionCorpusDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	if len(names) != subtitleSelectionCaseCount {
		t.Fatalf("JSON fixture files = %d, want %d", len(names), subtitleSelectionCaseCount)
	}

	fixtures := make([]subtitleSelectionFixture, 0, len(names))
	for _, path := range names {
		name := filepath.Base(path)
		want, ok := pins[name]
		if !ok {
			t.Fatalf("manifest does not pin %s", name)
		}
		raw, err := os.ReadFile(path) //nolint:gosec // repo-local corpus path from filepath.Glob of the testdata dir
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s sha256 = %s, want %s", name, got, want)
		}
		var fixture subtitleSelectionFixture
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		fixtures = append(fixtures, fixture)
	}
	return fixtures
}

func subtitleSelectionService(t *testing.T, fixture subtitleSelectionFixture) (*playback.Service, auth.Scope, string) {
	t.Helper()
	ctx := context.Background()
	st := storetest.OpenAt(t, seededDataDir(t))
	path := filepath.Join(t.TempDir(), fixture.ID+".mkv")
	if err := os.WriteFile(path, []byte("subtitle selection conformance"), 0o600); err != nil {
		t.Fatal(err)
	}

	streams := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 1, Type: "audio", Codec: "aac", Language: fixture.Input.AudioTrackLang, Channels: 2},
	}
	if fixture.ID == "regression-smart-mode-kills-subs-on-multi-audio" {
		streams = append(streams, store.MediaFileStream{
			Index: 2, Type: "audio", Codec: "aac", Language: fixture.Input.LangPrefs[0], Channels: 2,
		})
	}
	subtitleStreamIndex := len(streams)
	sidecars := make([]store.MediaFileSidecarCaption, 0)
	for index, track := range fixture.Input.Tracks {
		switch track.Kind {
		case playback.SubtitleTrackKindMuxed:
			streams = append(streams, store.MediaFileStream{
				Index: index + subtitleStreamIndex, Type: "subtitle", Codec: "subrip", Language: track.Language,
				Forced: track.Forced, HearingImpaired: track.SDH,
				VisualImpaired: track.VisualImpaired, Commentary: track.Commentary,
			})
		case playback.SubtitleTrackKindSidecar:
			if track.VisualImpaired || track.Commentary {
				t.Fatalf("fixture %q: sidecar cannot encode visualImpaired/commentary roles", fixture.ID)
			}
			sidecars = append(sidecars, store.MediaFileSidecarCaption{
				RelPath: fmt.Sprintf("%s.%d.srt", fixture.ID, index), Kind: store.SidecarKindCaption,
				Format: "srt", Language: track.Language, Forced: track.Forced, SDH: track.SDH,
			})
		default:
			t.Fatalf("fixture %q: unknown track kind %q", fixture.ID, track.Kind)
		}
	}

	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: fixture.ID, Container: "mkv", Codec: "h264",
		ContentFingerprint: "sha256:" + fixture.ID, Streams: streams, Sidecars: sidecars,
	})
	if err != nil {
		t.Fatalf("import fixture media: %v", err)
	}
	targetMedia := media
	if fixture.ID == "regression-next-episode-only-memory" {
		entityID := store.EntityID("entity-" + fixture.ID)
		if err := st.UpsertEntity(ctx, store.Entity{
			ID: entityID, LibraryID: "library-local", Kind: "container", Title: fixture.ID,
		}); err != nil {
			t.Fatalf("create fixture series: %v", err)
		}
		nextPath := filepath.Join(t.TempDir(), fixture.ID+"-next.mkv")
		if err := os.WriteFile(nextPath, []byte("subtitle selection conformance next item"), 0o600); err != nil {
			t.Fatal(err)
		}
		next, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: nextPath, Title: fixture.ID + "-next", Container: "mkv", Codec: "h264",
			ContentFingerprint: "sha256:next-" + fixture.ID, Streams: streams, Sidecars: sidecars,
		})
		if err != nil {
			t.Fatalf("import next fixture media: %v", err)
		}
		for order, mediaID := range []store.MediaID{media.ID, next.ID} {
			order := order
			if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
				FromEntityID: entityID,
				Name:         "children",
				ToMediaID:    mediaID,
				SortOrder:    &order,
			}); err != nil {
				t.Fatalf("group fixture media %s: %v", mediaID, err)
			}
		}
		targetMedia = next
	}
	mode := store.SubtitleMode(fixture.Input.Mode)
	preferSDH := fixture.Input.PreferSDH
	langs := append([]string(nil), fixture.Input.LangPrefs...)
	if _, err := st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{
		SubtitleLanguages: &langs, SubtitleMode: &mode, PreferSDH: &preferSDH,
	}); err != nil {
		t.Fatalf("set fixture preferences: %v", err)
	}

	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if pin := fixture.Input.PerItemRecord; pin != nil {
		groupKey, err := st.MediaSubtitleGroup(ctx, media.ID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Unix(100, 0).UTC()
		if err := st.PutSubtitleChoice(ctx, scope.ProfileID, store.SubtitleChoiceRecord{
			GroupKey: groupKey, Selected: pin.Selected, SubLanguage: pin.Language, SubKind: pin.Kind,
			SubForced: pin.Forced, SubSDH: pin.SDH, SubVisualImpaired: pin.VisualImpaired,
			SubCommentary: pin.Commentary,
		}, now); err != nil {
			t.Fatalf("set fixture pin: %v", err)
		}
	}

	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("subtitle-selection-conformance-key"), clock)
	return playback.NewService(st, tokens, clock), scope, string(targetMedia.ID)
}

func assertSubtitleSelection(
	t *testing.T,
	fixture subtitleSelectionFixture,
	service *playback.Service,
	scope auth.Scope,
	mediaID string,
) {
	t.Helper()
	response, err := service.Info(context.Background(), scope, playback.InfoRequest{
		MediaID: mediaID,
		CapabilityProfile: playback.CapabilityProfile{
			Client: "subtitle-selection-conformance", Containers: []string{"mkv"},
			DirectCodecs: []string{"h264"}, SubtitleFormats: []string{"srt", "webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("playback Info: %v", err)
	}
	if !reflect.DeepEqual(response.SelectedSubtitleTrack, fixture.Expected.Selection) {
		t.Fatalf("selection = %#v, want %#v", response.SelectedSubtitleTrack, fixture.Expected.Selection)
	}
	if response.SubtitlesUnavailableReason != fixture.Expected.UnavailableReason {
		t.Fatalf("unavailable reason = %q, want %q", response.SubtitlesUnavailableReason, fixture.Expected.UnavailableReason)
	}
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
