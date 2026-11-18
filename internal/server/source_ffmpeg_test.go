package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const testFFmpegSourceSum = "38fff90f73b3c4f9c3c7270711411a4ec3cbe63b205d4b4a5525bcc532d3d31f"

func writeFFmpegSource(t *testing.T, sums string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if sums != "" {
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func getFFmpegSource(t *testing.T, dir string) *httptest.ResponseRecorder {
	t.Helper()
	srv := newTestServer(t)
	srv.ffmpegSourceDir = dir
	rec := httptest.NewRecorder()
	// Deliberately omit credentials: the GPL source offer is public.
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/source/ffmpeg", nil))
	return rec
}

func TestFFmpegSourceServesBundledArchiveWithChecksum(t *testing.T) {
	name := "beamfall-ffmpeg-7.1.4-3-linux-amd64-source.tar"
	dir := writeFFmpegSource(t, testFFmpegSourceSum+"  "+name+"\n", map[string]string{name: "archive"})
	rec := getFFmpegSource(t, dir)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /source/ffmpeg status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Checksum-SHA256"); got != testFFmpegSourceSum {
		t.Fatalf("X-Checksum-SHA256 = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="`+name+`"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-tar" {
		t.Fatalf("Content-Type = %q", got)
	}
	if rec.Body.String() != "archive" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestFFmpegSourceIsNotFoundWithoutAValidBundle(t *testing.T) {
	name := "beamfall-ffmpeg-7.1.4-3-linux-amd64-source.tar"
	cases := map[string]string{
		"no directory":       filepath.Join(t.TempDir(), "absent"),
		"no SHA256SUMS":      writeFFmpegSource(t, "", map[string]string{name: "archive"}),
		"archive missing":    writeFFmpegSource(t, testFFmpegSourceSum+"  "+name+"\n", nil),
		"malformed digest":   writeFFmpegSource(t, "XYZ  "+name+"\n", map[string]string{name: "archive"}),
		"path escape":        writeFFmpegSource(t, testFFmpegSourceSum+"  ../beamfall-ffmpeg-x.tar\n", nil),
		"other archive name": writeFFmpegSource(t, testFFmpegSourceSum+"  ffmpeg.tar.gz\n", map[string]string{"ffmpeg.tar.gz": "archive"}),
	}
	for label, dir := range cases {
		t.Run(label, func(t *testing.T) {
			if rec := getFFmpegSource(t, dir); rec.Code != http.StatusNotFound {
				t.Fatalf("GET /source/ffmpeg status = %d, want 404", rec.Code)
			}
		})
	}
}
