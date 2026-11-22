package app

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/subtitlesearch"
)

type noSubtitleKey struct{}

func (noSubtitleKey) Secret(string) (string, error) { return "", fs.ErrNotExist }

func TestSubtitleSearchIsOffUnlessAProviderIsConfigured(t *testing.T) {
	if got := newSubtitleSearcher(nil, noSubtitleKey{}); got != nil {
		t.Fatalf("searcher with no providers = %v, want nil", got)
	}
}

func TestSubtitleSearchWithoutKeyNeverDials(t *testing.T) {
	searcher := newSubtitleSearcher([]string{subtitlesearch.OpenSubtitlesID}, noSubtitleKey{})
	if searcher == nil {
		t.Fatal("configured provider produced no searcher")
	}
	status := searcher.Status(context.Background())
	if len(status) != 1 || status[0].ID != "opensubtitles" || status[0].Ready {
		t.Fatalf("status = %+v", status)
	}
	if _, err := searcher.Search(context.Background(), mediahttp.SubtitleSearchQuery{Title: "x"}); !errors.Is(err, mediahttp.ErrSubtitleSearchNoKey) {
		t.Fatalf("search err = %v, want ErrSubtitleSearchNoKey", err)
	}
	if _, err := searcher.Download(context.Background(), "elsewhere", "1"); !errors.Is(err, mediahttp.ErrSubtitleSearchUnknownProvider) {
		t.Fatalf("download err = %v, want ErrSubtitleSearchUnknownProvider", err)
	}
}

func TestSubtitleSearchPolicyNamesOnlyProviderOrigins(t *testing.T) {
	policy := subtitleSearchPolicy{origins: map[string][]string{"opensubtitles": {subtitlesearch.OpenSubtitlesAPIOrigin}}}
	if _, ok, _ := policy.OutboundPolicy(context.Background(), "other"); ok {
		t.Fatal("policy granted an unconfigured provider")
	}
	got, ok, err := policy.OutboundPolicy(context.Background(), "opensubtitles")
	if err != nil || !ok || len(got.Approved) != 1 || got.Approved[0] != subtitlesearch.OpenSubtitlesAPIOrigin {
		t.Fatalf("policy = %+v ok=%v err=%v", got, ok, err)
	}
	if ua := subtitleSearchUserAgent(); !strings.HasPrefix(ua, "Beamfall ") {
		t.Fatalf("user agent = %q", ua)
	}
}

// TestSubtitleSearchIgnoresBundledCredentials: a release-injected key under
// the OpenSubtitles ref never reaches the searcher (owner decision 2026-11-21).
func TestSubtitleSearchIgnoresBundledCredentials(t *testing.T) {
	files := secretFiles{dir: t.TempDir()}
	bundled := withBundledCredentials(files, subtitlesearch.OpenSubtitlesKeyRef+"=bundled-test-value")
	if got, err := bundled.Secret(subtitlesearch.OpenSubtitlesKeyRef); err != nil || got == "" {
		t.Fatalf("precondition: bundled source = %q, %v", got, err)
	}
	// wireSubtitleSearch accepts only secretFiles, so production passes the
	// operator's files; the searcher built from them must not see the key.
	searcher := newSubtitleSearcher([]string{subtitlesearch.OpenSubtitlesID}, files)
	if status := searcher.Status(context.Background()); len(status) != 1 || status[0].Ready {
		t.Fatalf("status with only a bundled key = %+v, want not ready", status)
	}
}
