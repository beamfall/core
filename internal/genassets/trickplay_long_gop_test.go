package genassets

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Count populated tiles, not sheets: tile fills the unused cells with black,
// so one successful JPEG can conceal missing samples advertised by a manifest.
func TestTrickplayLongGOPMatchesManifest(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary trickplay test")
	}
	for _, gop := range []int{300, 600} {
		t.Run(fmt.Sprintf("gop-%d", gop), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			path := filepath.Join(t.TempDir(), "long.mp4")
			// #nosec G204 -- resolved FFmpeg, fixed lavfi input, table-owned GOP and t.TempDir output.
			cmd := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=40",
				"-c:v", "libx264", "-g", strconv.Itoa(gop), "-keyint_min", strconv.Itoa(gop), "-sc_threshold", "0", "-y", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("synthesize long GOP: %v\n%s", err, out)
			}
			st := storetest.Open(t)
			fingerprint, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: "Long GOP", Container: "mp4", Codec: "h264",
				Class: store.MediaClassVideo, ContentFingerprint: fingerprint, DurationSecondsHint: 40,
			})
			if err != nil {
				t.Fatal(err)
			}
			svc := NewService(st, ffmpeg.New(), t.TempDir())
			if err := svc.GenerateTrickplay(ctx, media.ID); err != nil {
				t.Fatal(err)
			}
			manifestPath := svc.ManifestPath(fingerprint)
			data, err := os.ReadFile(manifestPath) // #nosec G304 -- manifest produced by this test in t.TempDir.
			if err != nil {
				t.Fatal(err)
			}
			var manifest TrickplayManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.FrameCount != 4 {
				t.Fatalf("manifest frame count = %d, want 4", manifest.FrameCount)
			}
			populated := 0
			for _, name := range manifest.Sheets {
				file, err := os.Open(filepath.Join(filepath.Dir(manifestPath), name)) // #nosec G304 -- sheet named by the test-generated manifest.
				if err != nil {
					t.Fatal(err)
				}
				sheet, err := jpeg.Decode(file)
				closeErr := file.Close()
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil {
					t.Fatal(err)
				}
				for tile := 0; tile < manifest.Columns*manifest.Rows; tile++ {
					x0 := tile % manifest.Columns * manifest.TileWidth
					y0 := tile / manifest.Columns * manifest.TileHeight
					bright := false
					for y := 8; y < manifest.TileHeight-8; y += 16 {
						for x := 8; x < manifest.TileWidth-8; x += 16 {
							r, g, b, _ := sheet.At(x0+x, y0+y).RGBA()
							bright = bright || r+g+b > 3*0x2000
						}
					}
					if bright {
						populated++
					}
				}
			}
			t.Logf("40s long-GOP sample: populated tiles=%d, manifest frames=%d", populated, manifest.FrameCount)
			if populated != manifest.FrameCount {
				t.Fatalf("populated tiles = %d, manifest advertises %d", populated, manifest.FrameCount)
			}
		})
	}
}
