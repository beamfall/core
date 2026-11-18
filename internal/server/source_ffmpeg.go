package server

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// ffmpegSourceDir is where the container image keeps the GPL corresponding
// source for the ffmpeg build it ships (CORE-2497): one tar archive holding
// every input of tools/ffmpeg/build.sh for the image's architecture (the
// locked upstream tarballs, the Debian source of each bundled library at its
// exact version, and the build scripts), and a one-line SHA256SUMS naming it.
const ffmpegSourceDir = "/usr/share/beamfall/source/ffmpeg"

// ffmpegSourceSumsMax bounds the SHA256SUMS read; the file holds one line.
const ffmpegSourceSumsMax = 4 << 10

var ffmpegSourceSumLine = regexp.MustCompile(`^([0-9a-f]{64})  (beamfall-ffmpeg-[A-Za-z0-9._-]+\.tar)$`)

// ffmpegSource serves the bundled FFmpeg corresponding source archive with its
// SHA-256. Builds without a bundled archive (non-container releases, which do
// not ship FFmpeg) answer 404.
func (s *Server) ffmpegSource(w http.ResponseWriter, r *http.Request) {
	root, err := os.OpenRoot(s.ffmpegSourceDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	sum, name, ok := readFFmpegSourceSum(root)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Checksum-SHA256", sum)
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func readFFmpegSourceSum(root *os.Root) (sum, name string, ok bool) {
	f, err := root.Open("SHA256SUMS")
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, ffmpegSourceSumsMax))
	if !scanner.Scan() {
		return "", "", false
	}
	match := ffmpegSourceSumLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}
