package scan

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/beamfall/core/internal/store"
)

// sidecarCaptionExts is the closed sidecar caption format vocabulary (FEAT-4 /
// ADR-0005 §4, widened by ADR-0194 §5): the lowercase extension maps to the
// stored format string. Only these external subtitle formats are discovered as
// sidecar tracks.
//
// Tiers: Tier 1 adds .ass/.ssa (served flattened to WebVTT for
// non-libass clients, raw-passthrough to libass engines). Tier 2 adds bitmap
// sidecars — .sup (PGS) here, and the .idx/.sub VobSub pair handled separately
// in discover (a lone .sub is invalid, never silently converted). Tier 3
// (.smi/.ttml) stays deferred and is intentionally absent. MicroDVD/.txt is
// rejected by omission — framerate-relative timing has no format signature.
var sidecarCaptionExts = map[string]string{
	"srt": "srt",
	"vtt": "vtt",
	"ass": "ass",
	"ssa": "ssa",
	"sup": "sup",
}

// isVobSubExt reports whether an extension is one of the two halves of a VobSub
// subtitle. NEITHER half is recorded as a caption sidecar today (ADR-0194 §5
// Tier 2).
//
// The .sub carries the bitmaps and the .idx is only its index, so an .idx alone
// renders nothing. There is no byte route for the payload — a .sub never becomes
// a sidecar row, so it has no URL — which made a recorded .idx a PHANTOM track:
// a caption the menu offers and no client can draw. A lone .sub is invalid in
// its own right (it could be MicroDVD, which the format whitelist rejects) and
// must never be silently converted. Record the .idx again when the payload route
// lands, not before: an advertised track with no payload is worse than none.
func isVobSubExt(ext string) bool {
	return ext == "idx" || ext == "sub"
}

type sidecarDiscovery struct {
	interactive map[string]bool
	dirs        map[string][]os.DirEntry
}

func newSidecarDiscovery(interactiveExts []string) *sidecarDiscovery {
	interactive := map[string]bool{}
	for _, ext := range interactiveExts {
		interactive[strings.ToLower(strings.TrimPrefix(ext, "."))] = true
	}
	return &sidecarDiscovery{
		interactive: interactive,
		dirs:        map[string][]os.DirEntry{},
	}
}

func (d *sidecarDiscovery) discover(mediaPath string) []store.MediaFileSidecarCaption {
	out := []store.MediaFileSidecarCaption{}
	dir := filepath.Dir(mediaPath)
	entries, ok := d.dirs[dir]
	if !ok {
		var err error
		entries, err = os.ReadDir(dir)
		if err != nil {
			return out
		}
		d.dirs[dir] = entries
	}
	mediaStem := strings.ToLower(titleFromPath(mediaPath))
	if mediaStem == "" {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		if isVobSubExt(ext) {
			continue
		}
		captionFormat, isCaption := sidecarCaptionExts[ext]
		if !isCaption && !d.interactive[ext] {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		// The sidecar belongs to this media file when its stem is the media stem
		// optionally followed by dot-separated hint segments ("title" itself, or
		// "title.en.forced"). A stem that merely shares a prefix without the dot
		// boundary ("title2") is a different file's sidecar.
		hint := strings.TrimPrefix(stem, mediaStem)
		if stem != mediaStem && !strings.HasPrefix(hint, ".") {
			continue
		}
		if isCaption {
			language, forced, sdh := parseSidecarHints(strings.TrimPrefix(hint, "."))
			out = append(out, store.MediaFileSidecarCaption{
				RelPath:  name,
				Kind:     store.SidecarKindCaption,
				Format:   captionFormat,
				Language: language,
				Forced:   forced,
				SDH:      sdh,
			})
			continue
		}
		// Interactive sidecar: raw bytes, no caption hints. Format is the bare
		// extension so the byte route can serve it without interpreting the body.
		out = append(out, store.MediaFileSidecarCaption{
			RelPath: name,
			Kind:    store.SidecarKindInteractive,
			Format:  ext,
		})
	}
	return out
}

// parseSidecarHints parses the dot-separated hint segments of a sidecar stem
// (the part after the media stem, e.g. "en.forced" or "en.sdh") into the
// generic player track hints (invariant #1): an ISO-639 language tag, a forced
// flag, and an SDH/hearing-impaired flag. "forced", "sdh", "hi", and "cc" are
// the recognised flag segments (case-insensitive); the first non-flag segment
// is taken as the language tag. Order-independent, so "en.forced" and
// "forced.en" both parse, and an empty hint ("title.srt") yields no hints.
func parseSidecarHints(hint string) (language string, forced, sdh bool) {
	if hint == "" {
		return "", false, false
	}
	for _, seg := range strings.Split(hint, ".") {
		switch seg {
		case "":
			continue
		case "forced":
			forced = true
		case "sdh", "hi", "cc":
			sdh = true
		default:
			if language == "" {
				language = seg
			}
		}
	}
	return language, forced, sdh
}
