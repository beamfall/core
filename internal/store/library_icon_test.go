package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLibraryIconVocabulary(t *testing.T) {
	icons := LibraryIcons()
	if len(icons) != 16 {
		t.Fatalf("len(LibraryIcons()) = %d, want 16", len(icons))
	}
	seen := map[string]bool{}
	for _, icon := range icons {
		if seen[icon] {
			t.Fatalf("icon %q listed twice", icon)
		}
		seen[icon] = true
		if !IsLibraryIcon(icon) {
			t.Fatalf("IsLibraryIcon(%q) = false", icon)
		}
	}
	for _, name := range []string{"", "spaceship", "Film", " film"} {
		if IsLibraryIcon(name) {
			t.Fatalf("IsLibraryIcon(%q) = true", name)
		}
	}
	// The returned slice is a copy: a caller cannot widen the vocabulary.
	icons[0] = "spaceship"
	if IsLibraryIcon("spaceship") {
		t.Fatal("mutating LibraryIcons() changed the vocabulary")
	}
}

func TestDefaultLibraryIcon(t *testing.T) {
	cases := []struct {
		name    string
		classes []string
		want    string
	}{
		{"video only", []string{"video"}, LibraryIconFilm},
		{"audio only", []string{"audio"}, LibraryIconNote},
		{"image only", []string{"image"}, LibraryIconImage},
		{"repeated class", []string{"audio", "audio"}, LibraryIconNote},
		{"video and audio", []string{"video", "audio"}, LibraryIconLayers},
		{"all three", []string{"video", "audio", "image"}, LibraryIconLayers},
		{"no plugin", nil, LibraryIconFolder},
		{"unknown class only", []string{"hologram"}, LibraryIconFolder},
		{"unknown class beside audio", []string{"hologram", "audio"}, LibraryIconNote},
	}
	for _, tc := range cases {
		if got := DefaultLibraryIcon(tc.classes); got != tc.want {
			t.Errorf("%s: DefaultLibraryIcon(%v) = %q, want %q", tc.name, tc.classes, got, tc.want)
		}
		if !IsLibraryIcon(tc.want) {
			t.Errorf("%s: default %q is not in the vocabulary", tc.name, tc.want)
		}
	}
}

func TestLibraryEffectiveIcon(t *testing.T) {
	audio := []string{"audio"}
	if got := (Library{}).EffectiveIcon(audio); got != LibraryIconNote {
		t.Fatalf("no choice: EffectiveIcon = %q, want %q", got, LibraryIconNote)
	}
	if got := (Library{Icon: LibraryIconHeadphones}).EffectiveIcon(audio); got != LibraryIconHeadphones {
		t.Fatalf("chosen: EffectiveIcon = %q, want %q", got, LibraryIconHeadphones)
	}
	// A stored name that a later build no longer knows falls back to the
	// default instead of reaching clients.
	if got := (Library{Icon: "spaceship"}).EffectiveIcon(audio); got != LibraryIconNote {
		t.Fatalf("stale choice: EffectiveIcon = %q, want %q", got, LibraryIconNote)
	}
}

func TestSetLibraryIcon(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	everything := LibraryFilter{Revealed: true, AllLibraries: true}

	iconOf := func(id LibraryID) string {
		t.Helper()
		libraries, err := st.Libraries(ctx, everything)
		if err != nil {
			t.Fatal(err)
		}
		for _, library := range libraries {
			if library.ID == id {
				return library.Icon
			}
		}
		t.Fatalf("library %q not found", id)
		return ""
	}

	if got := iconOf("library-local"); got != "" {
		t.Fatalf("icon before any choice = %q, want empty", got)
	}

	if err := st.SetLibraryIcon(ctx, "library-local", LibraryIconTV, now); err != nil {
		t.Fatalf("SetLibraryIcon returned error: %v", err)
	}
	if got := iconOf("library-local"); got != LibraryIconTV {
		t.Fatalf("icon after choosing = %q, want %q", got, LibraryIconTV)
	}
	if got := iconOf("library-archive"); got != "" {
		t.Fatalf("another library's icon = %q, want empty", got)
	}

	// Choosing again replaces the choice.
	if err := st.SetLibraryIcon(ctx, "library-local", LibraryIconStar, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := iconOf("library-local"); got != LibraryIconStar {
		t.Fatalf("icon after choosing again = %q, want %q", got, LibraryIconStar)
	}

	// An unknown name is refused and the choice is unchanged.
	err := st.SetLibraryIcon(ctx, "library-local", "spaceship", now)
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("unknown icon error = %v, want ValidationError", err)
	}
	if got := iconOf("library-local"); got != LibraryIconStar {
		t.Fatalf("icon after refused write = %q, want %q", got, LibraryIconStar)
	}

	// Empty clears the choice.
	if err := st.SetLibraryIcon(ctx, "library-local", "", now); err != nil {
		t.Fatal(err)
	}
	if got := iconOf("library-local"); got != "" {
		t.Fatalf("icon after clearing = %q, want empty", got)
	}

	err = st.SetLibraryIcon(ctx, "library-missing", LibraryIconTV, now)
	if !errors.As(err, &validation) {
		t.Fatalf("missing library error = %v, want ValidationError", err)
	}
	if err := st.SetLibraryIcon(ctx, "", LibraryIconTV, now); err == nil {
		t.Fatal("empty library ID accepted")
	}
}
