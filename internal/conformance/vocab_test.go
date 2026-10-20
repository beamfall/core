package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVocab(t *testing.T) {
	// Client repos carry their own equivalent gate (e.g. VocabGateTests in
	// beamfall/apple); this one covers the server tree only.
	roots := []string{"cmd", "internal"}
	scanned := 0
	for _, root := range roots {
		err := filepath.WalkDir(requireRoot(t, root), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if vocabExempt(filepath.ToSlash(path)) {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			rel := vocabRelPath(t, path)
			if strings.HasPrefix(rel, "internal/") {
				if identifier, ok := coreIdentifierViolation(string(data)); ok {
					t.Fatalf("%s contains banned domain-specific Core identifier %q", rel, identifier)
				}
			}
			for lineNo, line := range strings.Split(strings.ToLower(string(data)), "\n") {
				if word, ok := vocabLineViolation(rel, line); ok {
					t.Fatalf("%s:%d contains banned domain vocabulary %q", rel, lineNo+1, word)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned == 0 {
		t.Fatal("vocabulary gate scanned zero files")
	}
}

var bannedVocab = []string{
	"performer",
	"studio",
	"scene",
	"gallery",
	"movie",
	"show",
	"season",
	"episode",
	"podcast",
	"podcasts",
	"audiobook",
	"audiobooks",
	"funscript",
	"o-counter",
	"stashdb",
}

var bannedCoreIdentifiers = []string{
	"musicHydration",
	"musicHydrator",
	"hydrateMusic",
}

func coreIdentifierViolation(source string) (string, bool) {
	lower := strings.ToLower(source)
	for _, identifier := range bannedCoreIdentifiers {
		if strings.Contains(lower, strings.ToLower(identifier)) {
			return identifier, true
		}
	}
	return "", false
}

func vocabExempt(path string) bool {
	for _, prefix := range VocabExemptPrefixes {
		if strings.Contains(path, prefix) {
			return true
		}
	}
	return false
}

func vocabRelPath(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(repoRoot(t), path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

func vocabAllowedLine(path, line, word string) bool {
	for _, allowed := range vocabAllowedLines[path] {
		if allowed.word == word && strings.Contains(line, allowed.fragment) {
			return true
		}
	}
	return false
}

func vocabLineViolation(path, line string) (string, bool) {
	for _, word := range bannedVocab {
		if containsWord(line, word) && !vocabAllowedLine(path, line, word) {
			return word, true
		}
	}
	return "", false
}

type vocabAllowed struct {
	word     string
	fragment string
}

var vocabAllowedLines = map[string][]vocabAllowed{
	"cmd/beamfall/bundles.go": {
		{"audiobook", `github.com/beamfall/plugin-audiobook`},
		{"audiobook", `audiobook.bundle()`},
		{"podcasts", `github.com/beamfall/plugin-podcasts`},
		{"podcasts", `podcasts.bundle()`},
	},
	"internal/app/app.go": {
		{"podcast", `driven by the podcast plugin`},
		{"podcast", `podcast index is the optional richer directory`},
	},
	"cmd/beamfall/hooks_shared.go": {
		{"audiobook", `github.com/beamfall/plugin-audiobook`},
		{"podcasts", `github.com/beamfall/plugin-podcasts`},
		{"podcast", `podcast-index-key`},
		{"podcast", `podcast-index-secret`},
	},
	"internal/conductor/conductor.go": {
		{"podcast", `queue composition stays plugin-owned (music/podcast)`},
	},
	"internal/genassets/loudness.go": {
		{"podcast", `so a podcast, audiobook, or music track all get one`},
		{"audiobook", `so a podcast, audiobook, or music track all get one`},
	},
	"internal/genassets/silencemap.go": {
		{"podcast", `so a podcast player can skip`},
	},
	"internal/proxytee/proxytee.go": {
		{"podcast", `podcast vocabulary (invariant #1)`},
	},
	"internal/server/server.go": {
		{"podcast", `podcast-plugin-driven capability`},
	},
	"internal/skipdetect/skipdetect.go": {
		{"podcast", `podcast intro/ad skipping`},
	},
	"internal/store/engagement.go": {
		{"podcasts", `podcasts: audio resumes`},
	},
	"internal/store/foreignimport/foreignimporttest/foreignimporttest.go": {
		{"podcast", `and podcast export dbs`},
	},
	"internal/store/item_state_import.go": {
		{"podcast", `because podcast guids are only safe within a feed scope`},
	},
	"internal/store/provisional.go": {
		{"podcast", `no podcast vocabulary`},
		{"podcast", `podcast enclosures`},
	},
	"internal/store/schema_freeze.go": {
		{"podcast", `mute/retention/podcast predicates`},
	},
}

func TestVocabAllowedLinesStayNarrow(t *testing.T) {
	for path, allowed := range vocabAllowedLines {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(data))
		for _, item := range allowed {
			if !strings.Contains(lower, item.fragment) {
				t.Fatalf("%s allowlist fragment missing: %q", path, item.fragment)
			}
		}
	}
	if vocabAllowedLine("internal/app/app.go", "podcast should fail here", "podcast") {
		t.Fatal("vocab allowlist matched an unlisted line")
	}
}

func TestVocabBansPluginDomainResidue(t *testing.T) {
	for _, tc := range []struct {
		line string
		word string
	}{
		{`EventKinds: []string{"o-counter"}`, "o-counter"},
		{`SourceID: "stashdb"`, "stashdb"},
	} {
		word, ok := vocabLineViolation("internal/app/leak.go", strings.ToLower(tc.line))
		if !ok || word != tc.word {
			t.Fatalf("vocabLineViolation(%q) = %q, %v; want %q, true", tc.line, word, ok, tc.word)
		}
	}
}

func TestVocabBansDomainSpecificCoreHydrationIdentifiers(t *testing.T) {
	for _, source := range []string{
		"type MusicHydrator interface{}",
		"func HydrateMusic() {}",
		"func ScanWithMusicHydration() {}",
		"var musicHydrators = map[string]any{}",
	} {
		if identifier, ok := coreIdentifierViolation(source); !ok || identifier == "" {
			t.Fatalf("coreIdentifierViolation(%q) = %q, %v; want a violation", source, identifier, ok)
		}
	}
}

func containsWord(text, word string) bool {
	start := 0
	for {
		index := strings.Index(text[start:], word)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !isWordByte(text[index-1])
		after := index + len(word)
		afterOK := after == len(text) || !isWordByte(text[after])
		if beforeOK && afterOK {
			return true
		}
		start = after
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}
