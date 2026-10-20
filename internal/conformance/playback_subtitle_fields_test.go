package conformance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/tools/wire-contract/contract"
)

// AC-2: "Subtitles unavailable on this device" is a DISTINCT
// additive playbackInfo field, NOT the existing whole-media decision.verdict
// enum — reusing verdict would wrongly fail the entire playback decision when
// only subtitles are unavailable. These fixtures are that acceptance criterion.

// subtitleUnavailableReason is the ADR's own worked example of the free-text
// reason convention the subtitle-unavailable field follows.
const subtitleUnavailableReason = "burn-in-capacity-exhausted"

// wholeMediaReason is a whole-media Reason that differs from the subtitle one,
// so a fixture that aliased the two fields onto one value would fail.
const wholeMediaReason = "media-file-unavailable"

// decisionVerdictVocabulary is the whole-media verdict enum
// (internal/playback/playback.go, decision.verdict). The subtitle-unavailable
// field must stay orthogonal to EVERY member of it, not just the common one.
func decisionVerdictVocabulary() []string {
	return []string{"direct-play", "direct-stream", "precomputed-rendition", "transcode", "unavailable"}
}

func marshalInfoResponse(t *testing.T, response playback.InfoResponse) map[string]any {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal InfoResponse: %v", err)
	}
	return decodeJSON(t, raw)
}

// TestSubtitleUnavailableIsIndependentOfDecisionVerdict is AC-2's core proof:
// across the WHOLE verdict enum, a subtitle-unavailable reason rides alongside
// the verdict without changing it and without collapsing into it. A field that
// reused the verdict — or that aliased the whole-media reason — fails here.
func TestSubtitleUnavailableIsIndependentOfDecisionVerdict(t *testing.T) {
	for _, verdict := range decisionVerdictVocabulary() {
		t.Run(verdict, func(t *testing.T) {
			doc := marshalInfoResponse(t, playback.InfoResponse{
				MediaID:                    "media-alpha",
				Decision:                   verdict,
				Reason:                     wholeMediaReason,
				SubtitlesUnavailableReason: subtitleUnavailableReason,
			})
			if doc["decision"] != verdict {
				t.Fatalf("decision = %v, want %q — subtitle unavailability must never change the whole-media verdict", doc["decision"], verdict)
			}
			if doc["reason"] != wholeMediaReason {
				t.Fatalf("reason = %v, want %q — the whole-media reason must not be overwritten by the subtitle one", doc["reason"], wholeMediaReason)
			}
			got, present := doc["subtitlesUnavailableReason"]
			if !present {
				t.Fatalf("subtitlesUnavailableReason is absent for verdict %q — it collapsed into the verdict instead of standing beside it", verdict)
			}
			if got != subtitleUnavailableReason {
				t.Fatalf("subtitlesUnavailableReason = %v, want %q", got, subtitleUnavailableReason)
			}
			if slices.Contains(decisionVerdictVocabulary(), got.(string)) {
				t.Fatalf("subtitlesUnavailableReason = %v, which is a decision verdict — the field is free text, never the enum", got)
			}
		})
	}
}

// TestSubtitleFieldsAreOmittedFromAHealthyResponse is the additivity proof: an
// item and a client this contract does not touch see a response with neither
// new key, so nothing that consumed playbackInfo before sees any change.
func TestSubtitleFieldsAreOmittedFromAHealthyResponse(t *testing.T) {
	srv := newPlaybackContractServer(t)
	body := `{"mediaID":"media-alpha","capabilityProfile":{"client":"conformance","containers":["mp4"],"directCodecs":["h264"]}}`
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

	doc := decodeJSON(t, rec.Body.Bytes())
	for _, key := range []string{"selectedSubtitleTrack", "subtitlesUnavailableReason"} {
		if _, present := doc[key]; present {
			t.Fatalf("%s is present on a healthy response with nothing to state", key)
		}
	}
}

// TestSubtitleFieldsArePublishedBesideDecision proves the schema slot exists on
// the WIRE, not merely in Go: both fields reach the newest published artifact as
// top-level siblings of decision, which is what makes them independently
// consumable by a client that ignores one and reads the other.
func TestSubtitleFieldsArePublishedBesideDecision(t *testing.T) {
	published := publishedPlaybackInfoContract(t)
	fields := topLevelWireFieldNames(published.Response)
	for _, name := range []string{"decision", "reason", "selectedSubtitleTrack", "subtitlesUnavailableReason"} {
		if !slices.Contains(fields, name) {
			t.Fatalf("published playbackInfo response is missing top-level %q; has %v", name, fields)
		}
	}
}

// TestPublishedPrecedenceRuleLivesInTheWireSpec pins ADR-0194 §3's requirement
// that precedence is documented in the wire spec rather than in comments: the
// rules ship inside the published artifact every consumer pins.
func TestPublishedPrecedenceRuleLivesInTheWireSpec(t *testing.T) {
	notes := strings.Join(publishedPlaybackInfoContract(t).Notes, "\n")
	for _, rule := range []string{
		"server-authoritative at playback START only",
		"superseded and is not re-applied",
		"NOT the request-side selectedSubtitleIndex",
		"roles is a SET",
		"closed vocabulary muxed|sidecar",
		"INDEPENDENT of decision",
	} {
		if !strings.Contains(notes, rule) {
			t.Fatalf("published playbackInfo notes do not state %q; notes = %q", rule, notes)
		}
	}
}

// TestSubtitleTrackKindVocabularyIsClosed pins the literal set reflection cannot
// express — shapeOf publishes kind as a generic string. Growing the vocabulary
// is a deliberate wire-contract change, and editing this list is that act.
func TestSubtitleTrackKindVocabularyIsClosed(t *testing.T) {
	want := []string{"muxed", "sidecar"}
	if got := playback.SubtitleTrackKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("subtitle track kinds = %v, want %v", got, want)
	}
	notes := strings.Join(publishedPlaybackInfoContract(t).Notes, "\n")
	for _, kind := range want {
		if !strings.Contains(notes, kind) {
			t.Fatalf("published playbackInfo notes do not enumerate kind %q", kind)
		}
	}
}

// publishedPlaybackInfoContract reads the playbackInfo contract out of the
// newest published wire-contract artifact — the client-pinnable truth, not the
// live in-process build.
func publishedPlaybackInfoContract(t *testing.T) contract.PlaybackInfoContract {
	t.Helper()
	artifact, err := newestWireContractArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot contract.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("unmarshal %s: %v", artifact.Path, err)
	}
	return snapshot.PlaybackInfo
}

func topLevelWireFieldNames(shape contract.WireShape) []string {
	names := make([]string, 0, len(shape.Fields))
	for _, field := range shape.Fields {
		names = append(names, field.Name)
	}
	return names
}
