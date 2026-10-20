package conformance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/tools/wire-contract/contract"
)

// quirkedClient is the client identity the fixture entries below select. The
// conformance corpus's own golden request declares client "conformance", which
// matches nothing — that is what keeps every existing fixture byte-identical.
const quirkedClient = "quirk-tv"

// playbackInfoWithQuirks posts one playbackInfo through the real HTTP stack
// with the given quirk entries installed, and returns the decoded body.
func playbackInfoWithQuirks(t *testing.T, client string, entries []playback.QuirkEntry) map[string]any {
	t.Helper()
	srv, _ := newPlaybackContractServerWithQuirks(t, entries)
	body := `{"mediaID":"media-alpha","capabilityProfile":{"client":"` + client +
		`","tier":"gen1","firmware":"2.4.0","containers":["mp4"],"directCodecs":["h264"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/playback-info", strings.NewReader(body))
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d: %s", rec.Code, rec.Body.String())
	}
	return decodeJSON(t, rec.Body.Bytes())
}

func stringSlice(t *testing.T, doc map[string]any, key string) ([]string, bool) {
	t.Helper()
	raw, ok := doc[key]
	if !ok {
		return nil, false
	}
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want an array", key, raw)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("%s contains %#v, want strings", key, value)
		}
		out = append(out, text)
	}
	return out, true
}

// TestBehaviorFlagRoundTripsPlaybackInfo proves a flag reaches the wire in the
// closed vocabulary's sorted, deduplicated form.
func TestBehaviorFlagRoundTripsPlaybackInfo(t *testing.T) {
	doc := playbackInfoWithQuirks(t, quirkedClient, []playback.QuirkEntry{{
		ID:       "quirk-tv-engine-and-seek",
		Note:     "fixture: this engine restarts the session on seek",
		Selector: playback.QuirkSelector{Client: quirkedClient},
		Flags:    []string{"seek-restarts-session", "avoid-engine:web-mse"},
	}})
	flags, ok := stringSlice(t, doc, "behaviorFlags")
	if !ok {
		t.Fatal("behaviorFlags is absent for a matched quirk")
	}
	if !reflect.DeepEqual(flags, []string{"avoid-engine:web-mse", "seek-restarts-session"}) {
		t.Fatalf("behaviorFlags = %v, want the sorted vocabulary pair", flags)
	}
}

// TestBehaviorFlagVocabularyIsClosed pins the literal. Growing the vocabulary is
// a deliberate wire-contract change, and editing this list is the deliberate act.
func TestBehaviorFlagVocabularyIsClosed(t *testing.T) {
	want := []string{
		"avoid-engine:avplayer",
		"avoid-engine:libmpv",
		"avoid-engine:media3",
		"avoid-engine:web-mse",
		"seek-restarts-session",
	}
	if got := playback.BehaviorFlagVocabulary(); !reflect.DeepEqual(got, want) {
		t.Fatalf("behavior flag vocabulary = %v, want %v — a quirk expressible as neither an override nor one of these is a contract gap to fix in ADR-0161/ADR-0138, never an inline client hack", got, want)
	}
}

// TestBehaviorFlagRejectsUnknownName is the RED-proving fixture: an unknown
// flag NAME and an unknown avoid-engine PARAMETER are both load-time errors.
func TestBehaviorFlagRejectsUnknownName(t *testing.T) {
	for _, flag := range []string{"seek-restarts-everything", "avoid-engine:vlc"} {
		data := `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"flags":["` + flag + `"]}]}`
		_, err := playback.ParseQuirkDB([]byte(data))
		if err == nil {
			t.Fatalf("ParseQuirkDB accepted flag %q", flag)
		}
		if !strings.Contains(err.Error(), flag) {
			t.Fatalf("error %q does not name the rejected flag %q", err, flag)
		}
	}
}

// TestBehaviorFlagsAbsentWithoutQuirk is what keeps every committed playback
// fixture byte-identical: an unmatched device's response is exactly what it was
// before this contract existed.
func TestBehaviorFlagsAbsentWithoutQuirk(t *testing.T) {
	doc := playbackInfoWithQuirks(t, "conformance", []playback.QuirkEntry{{
		ID:       "quirk-tv-engine",
		Note:     "fixture",
		Selector: playback.QuirkSelector{Client: quirkedClient},
		Flags:    []string{"seek-restarts-session"},
	}})
	for _, key := range []string{"behaviorFlags", "appliedQuirks"} {
		if _, present := doc[key]; present {
			t.Fatalf("%s is present for an unmatched device", key)
		}
	}
}

// TestAppliedQuirksExcludesFlagOnlyEntries pins the no-false-attribution rule:
// the two fields are reported separately so neither can be mistaken for the
// other, and a flags-only entry is never blamed for a verdict.
func TestAppliedQuirksExcludesFlagOnlyEntries(t *testing.T) {
	flagsOnly := playbackInfoWithQuirks(t, quirkedClient, []playback.QuirkEntry{{
		ID:       "quirk-tv-flags-only",
		Note:     "fixture",
		Selector: playback.QuirkSelector{Client: quirkedClient},
		Flags:    []string{"seek-restarts-session"},
	}})
	if flags, ok := stringSlice(t, flagsOnly, "behaviorFlags"); !ok || len(flags) != 1 {
		t.Fatalf("behaviorFlags = %v, want the single raised flag", flags)
	}
	if _, present := flagsOnly["appliedQuirks"]; present {
		t.Fatal("appliedQuirks names a flags-only entry")
	}

	overrideOnly := playbackInfoWithQuirks(t, quirkedClient, []playback.QuirkEntry{{
		ID:       "quirk-tv-override-only",
		Note:     "fixture",
		Selector: playback.QuirkSelector{Client: quirkedClient},
		Override: playback.QuirkOverride{ForceTranscode: true},
	}})
	if ids, ok := stringSlice(t, overrideOnly, "appliedQuirks"); !ok || !reflect.DeepEqual(ids, []string{"quirk-tv-override-only"}) {
		t.Fatalf("appliedQuirks = %v, want the override-bearing entry", ids)
	}
	if _, present := overrideOnly["behaviorFlags"]; present {
		t.Fatal("behaviorFlags is present for an entry that raises none")
	}
}

// TestPlaybackInfoQuirkFieldsInPublishedContract asserts PLACEMENT, not mere
// presence. wireShapeHasField recurses, so a bare scan would also pass on a
// field buried at the wrong nesting level: behaviorFlags/appliedQuirks must be
// DIRECT members of the response shape, while tier/firmware live inside the
// nested capabilityProfile sub-shape of the request.
func TestPlaybackInfoQuirkFieldsInPublishedContract(t *testing.T) {
	published, err := newestWireContractArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot contract.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"behaviorFlags", "appliedQuirks"} {
		if !wireShapeHasDirectField(snapshot.PlaybackInfo.Response, field) {
			t.Fatalf("published playbackInfo response shape has no top-level %q", field)
		}
	}
	profile, ok := wireShapeField(snapshot.PlaybackInfo.Request, "capabilityProfile")
	if !ok {
		t.Fatal("published playbackInfo request shape has no capabilityProfile sub-shape")
	}
	for _, field := range []string{"tier", "firmware"} {
		if !wireShapeHasDirectField(profile, field) {
			t.Fatalf("published capabilityProfile sub-shape has no %q — a quirk selector cannot address the device without it", field)
		}
	}
}

func wireShapeField(shape contract.WireShape, name string) (contract.WireShape, bool) {
	for _, field := range shape.Fields {
		if field.Name == name {
			return field.Shape, true
		}
	}
	return contract.WireShape{}, false
}

func wireShapeHasDirectField(shape contract.WireShape, name string) bool {
	_, ok := wireShapeField(shape, name)
	return ok
}
