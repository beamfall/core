package scan

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/plugin-filmtv"
)

// refreshParser parses each file by base name from a table the test swaps
// between scans, standing in for a parser release that reads names anew.
type refreshParser struct {
	mu     sync.Mutex
	byName map[string]refreshParse
}

type refreshParse struct {
	parsed ParsedMedia
	ok     bool
	fields []ParsedField
}

func (p *refreshParser) set(byName map[string]refreshParse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byName = byName
}

func (p *refreshParser) lookup(path string) refreshParse {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byName[filepath.Base(path)]
}

func (p *refreshParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	entry := p.lookup(path)
	return entry.parsed, entry.ok
}

func (p *refreshParser) ParseTypedMediaFields(path string, _ ParseProbe) []ParsedField {
	return p.lookup(path).fields
}

// TestRescanRefreshesParserOwnedFields: an unchanged file's rescan applies the
// parser's current output to the fields the parser wrote (a changed class, a
// class the parser no longer emits, a changed or dropped declared field) and
// leaves identified, NFO-imported and manual fields alone (CORE-2554).
func TestRescanRefreshesParserOwnedFields(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"toEpisode.mkv", "toNone.mkv", "identified.mkv", "nfo.mkv", "manual.mkv"} {
		paths[name] = writeScanFile(t, root, name, []byte(name))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Film TV", false, filmtv.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	movie := refreshParse{
		parsed: ParsedMedia{Title: "Title", Class: "movie"},
		ok:     true,
		fields: []ParsedField{{Name: "note", Text: "first"}},
	}
	parser := &refreshParser{}
	parser.set(map[string]refreshParse{
		"toEpisode.mkv": movie, "toNone.mkv": movie, "identified.mkv": movie, "nfo.mkv": movie, "manual.mkv": movie,
	})
	parsers := map[string]FilenameParser{filmtv.PluginID: parser}
	scan := func() map[string]store.MediaID {
		t.Helper()
		result, err := Scan(ctx, st, nil, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		ids := map[string]store.MediaID{}
		for _, file := range result.Files {
			ids[filepath.Base(file.Path)] = file.MediaID
		}
		return ids
	}
	ids := scan()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true}
	fieldsOf := func(name string) map[string]store.MediaField {
		t.Helper()
		fields, err := st.MediaFields(ctx, ids[name], filter)
		if err != nil {
			t.Fatalf("MediaFields(%s): %v", name, err)
		}
		byName := map[string]store.MediaField{}
		for _, field := range fields {
			byName[field.Name] = field
		}
		return byName
	}
	if got := fieldsOf("toEpisode.mkv")["class"].Text; got != "movie" {
		t.Fatalf("first scan class = %q, want movie", got)
	}

	// Identification and an NFO import both land at the operator tier; a
	// manual edit sits above both.
	if err := st.UpsertMediaFieldsWithSource(ctx, ids["identified.mkv"], []store.MediaField{
		{Name: "class", Type: store.EntityFieldText, Text: "movie"},
	}, store.MutationSourceOperator); err != nil {
		t.Fatalf("identify: %v", err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, ids["nfo.mkv"], []store.MediaField{
		{Name: "class", Type: store.EntityFieldText, Text: "movie"},
		{Name: "note", Type: store.EntityFieldText, Text: "from nfo"},
	}, store.MutationSourceOperator); err != nil {
		t.Fatalf("nfo import: %v", err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, ids["manual.mkv"], []store.MediaField{
		{Name: "class", Type: store.EntityFieldText, Text: "movie"},
	}, store.MutationSourceManual); err != nil {
		t.Fatalf("manual edit: %v", err)
	}

	episode := refreshParse{
		parsed: ParsedMedia{Title: "Show", Class: "episode", Sequence: "1", Index: 1},
		ok:     true,
		fields: []ParsedField{{Name: "note", Text: "second"}},
	}
	parser.set(map[string]refreshParse{
		"toEpisode.mkv":  episode,
		"toNone.mkv":     {parsed: ParsedMedia{Title: "Title"}, ok: true},
		"identified.mkv": episode,
		"nfo.mkv":        episode,
		"manual.mkv":     episode,
	})
	if rescanned := scan(); rescanned["toEpisode.mkv"] != ids["toEpisode.mkv"] {
		t.Fatalf("rescan moved the Media: %s -> %s", ids["toEpisode.mkv"], rescanned["toEpisode.mkv"])
	}

	toEpisode := fieldsOf("toEpisode.mkv")
	if toEpisode["class"].Text != "episode" || toEpisode["sequence"].Number != 1 || toEpisode["index"].Number != 1 {
		t.Fatalf("movie->episode fields = %#v, want class episode, sequence 1, index 1", toEpisode)
	}
	if toEpisode["note"].Text != "second" {
		t.Fatalf("declared field = %q, want the new parse's value", toEpisode["note"].Text)
	}
	toNone := fieldsOf("toNone.mkv")
	if _, ok := toNone["class"]; ok {
		t.Fatalf("movie->no class kept class %#v", toNone["class"])
	}
	if _, ok := toNone["note"]; ok {
		t.Fatalf("a declared field the parser no longer emits survived: %#v", toNone["note"])
	}
	for _, name := range []string{"identified.mkv", "nfo.mkv", "manual.mkv"} {
		fields := fieldsOf(name)
		if fields["class"].Text != "movie" {
			t.Fatalf("%s class = %q, want the curated movie", name, fields["class"].Text)
		}
	}
	if got := fieldsOf("nfo.mkv")["note"].Text; got != "from nfo" {
		t.Fatalf("NFO field = %q, want from nfo", got)
	}
	// The curated Media still take the parser's new fields no one else holds.
	if got := fieldsOf("identified.mkv")["index"].Number; got != 1 {
		t.Fatalf("identified index = %v, want the parsed 1", got)
	}
	for name, want := range map[string]store.MutationSource{
		"toEpisode.mkv": store.MutationSourceScan, "identified.mkv": store.MutationSourceOperator,
		"nfo.mkv": store.MutationSourceOperator, "manual.mkv": store.MutationSourceManual,
	} {
		got, err := st.MediaFieldProvenance(ctx, ids[name], "class")
		if err != nil || got != want {
			t.Fatalf("%s class provenance = %q (%v), want %q", name, got, err, want)
		}
	}
}

// videoProber reports every file as video.
type videoProber struct{}

func (videoProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{HasVideo: true, VideoCodec: "h264", DurationSeconds: 60}, nil
}

// probeClassParser classes a file by the probe, as a parser that tells video
// from audio does.
type probeClassParser struct{}

func (probeClassParser) Parse(_ string, probe ParseProbe) (ParsedMedia, bool) {
	if probe.HasVideo {
		return ParsedMedia{Title: "Song", Class: "video-track"}, true
	}
	return ParsedMedia{Title: "Song", Class: "audio-track"}, true
}

// TestRescanKeepsProbeDependentFieldsWithoutProbe: the fast path, or a scan
// with no prober, parses without a probe, so a class the probe decided stays
// as the probed parse left it (CORE-2554).
func TestRescanKeepsProbeDependentFieldsWithoutProbe(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeScanFile(t, root, "song.mkv", []byte("song"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Film TV", false, filmtv.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	parsers := map[string]FilenameParser{filmtv.PluginID: probeClassParser{}}
	var mediaID store.MediaID
	// Pass 0 probes; pass 1 takes the unprobed fast path; pass 2 scans with
	// no prober at all, which must not count as authoritative either.
	for pass := 0; pass < 3; pass++ {
		var prober Prober = videoProber{}
		if pass == 2 {
			prober = nil
		}
		result, err := Scan(ctx, st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
		if err != nil || len(result.Files) != 1 {
			t.Fatalf("Scan pass %d: files %d, error %v", pass, len(result.Files), err)
		}
		mediaID = result.Files[0].MediaID
		fields, err := st.MediaFields(ctx, mediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatalf("MediaFields: %v", err)
		}
		class := ""
		for _, field := range fields {
			if field.Name == "class" {
				class = field.Text
			}
		}
		if class != "video-track" {
			t.Fatalf("pass %d class = %q, want the probed video-track", pass, class)
		}
	}
}

// absentOnVideoParser emits no class when the probe shows video and an
// audio-track class otherwise, as a revised parser that leaves video out.
type absentOnVideoParser struct{}

func (absentOnVideoParser) Parse(_ string, probe ParseProbe) (ParsedMedia, bool) {
	if probe.HasVideo {
		return ParsedMedia{Title: "Song"}, true
	}
	return ParsedMedia{Title: "Song", Class: "audio-track"}, true
}

// TestRescanKeepsProbeDecidedAbsence: a class the probed parse leaves out is
// not added by a later unprobed parse on the fast path or without a prober
// (CORE-2554).
func TestRescanKeepsProbeDecidedAbsence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeScanFile(t, root, "song.mkv", []byte("song"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Film TV", false, filmtv.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	parsers := map[string]FilenameParser{filmtv.PluginID: absentOnVideoParser{}}
	for pass := 0; pass < 3; pass++ {
		var prober Prober = videoProber{}
		if pass == 2 {
			prober = nil
		}
		result, err := Scan(ctx, st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
		if err != nil || len(result.Files) != 1 {
			t.Fatalf("Scan pass %d: files %d, error %v", pass, len(result.Files), err)
		}
		fields, err := st.MediaFields(ctx, result.Files[0].MediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatalf("MediaFields: %v", err)
		}
		for _, field := range fields {
			if field.Name == "class" {
				t.Fatalf("pass %d class = %q, want none (the probe saw video)", pass, field.Text)
			}
		}
	}
}

// contextProbeClassParser is probeClassParser with folder context, so a
// multi-file folder is parsed once by the scan's preflight.
type contextProbeClassParser struct{ probeClassParser }

func (p contextProbeClassParser) ParseWithContext(path string, probe ParseProbe, _ ParseContext) (ParsedMedia, bool) {
	return p.Parse(path, probe)
}

// firstProbeFailsProber fails the first probe of one file and reports video
// otherwise: the worker's probe of that file fails, the folder preflight's
// succeeds.
type firstProbeFailsProber struct {
	mu     sync.Mutex
	target string
	failed bool
}

func (p *firstProbeFailsProber) Probe(ctx context.Context, path string) (ffmpeg.Probe, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if filepath.Base(path) == p.target && !p.failed {
		p.failed = true
		return ffmpeg.Probe{}, context.DeadlineExceeded
	}
	return videoProber{}.Probe(ctx, path)
}

// TestRescanOwnershipFollowsThePreflightProbe: when the folder preflight's
// probe decided the parse but the worker's probe failed, the class is still
// probe-dependent, so a scan without a prober leaves it (CORE-2554).
func TestRescanOwnershipFollowsThePreflightProbe(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeScanFile(t, root, "a.mkv", []byte("a"))
	writeScanFile(t, root, "b.mkv", []byte("b"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Film TV", false, filmtv.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	parsers := map[string]FilenameParser{filmtv.PluginID: contextProbeClassParser{}}
	for pass, prober := range []Prober{&firstProbeFailsProber{target: "a.mkv"}, nil} {
		result, err := Scan(ctx, st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
		if err != nil || len(result.Files) != 2 {
			t.Fatalf("Scan pass %d: files %d, error %v", pass, len(result.Files), err)
		}
		for _, file := range result.Files {
			if filepath.Base(file.Path) != "a.mkv" {
				continue
			}
			fields, err := st.MediaFields(ctx, file.MediaID, store.LibraryFilter{AllLibraries: true})
			if err != nil {
				t.Fatalf("MediaFields: %v", err)
			}
			class := ""
			for _, field := range fields {
				if field.Name == "class" {
					class = field.Text
				}
			}
			if class != "video-track" {
				t.Fatalf("pass %d a.mkv class = %q, want the preflight-probed video-track", pass, class)
			}
		}
	}
}

// TestParserFieldRefreshVouchesForLegacyScanValuesOnlyWithoutOtherWriters:
// older scan-tier values are the parser's own only where no hydration and no
// image local metadata could have written them at that tier (CORE-2554).
func TestParserFieldRefreshVouchesForLegacyScanValuesOnlyWithoutOtherWriters(t *testing.T) {
	parser := &refreshParser{}
	for _, tc := range []struct {
		name     string
		path     string
		hydrator MetadataHydrator
		want     bool
	}{
		{name: "video", path: "/media/show.mkv", want: true},
		{name: "image", path: "/media/photo.jpg", want: false},
		{name: "hydrated", path: "/media/track.mkv", hydrator: &fakeMetadataHydrator{}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &scanWalkState{parser: parser, metadataHydrator: tc.hydrator}
			refresh := s.parserFieldRefresh(tc.path, ParseProbe{}, ParsedMedia{}, false, nil, true)
			if refresh.LegacyScanOwned != tc.want {
				t.Fatalf("LegacyScanOwned = %v, want %v", refresh.LegacyScanOwned, tc.want)
			}
		})
	}
}
