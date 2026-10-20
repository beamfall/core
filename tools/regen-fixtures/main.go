// Command regen-fixtures refreshes the committed FFprobe fixture manifest.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

const manifestPath = "internal/ffmpeg/testdata/probe/manifest.json"

type manifest struct {
	FFmpegVersion string    `json:"ffmpeg_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Fixtures      []fixture `json:"fixtures"`
}

type fixture struct {
	Path          string   `json:"path"`
	SHA256        string   `json:"sha256"`
	Mode          string   `json:"mode"`
	SynthesisArgs []string `json:"synthesis_args"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "regen-fixtures:", err)
		os.Exit(1)
	}
}

func run() error {
	got, err := ffmpeg.New().Version(context.Background())
	if err != nil {
		return err
	}
	if want := ffmpeg.PinnedVersion(); got != want {
		return fmt.Errorf("ffmpeg version %q does not match pin %q", got, want)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for i := range m.Fixtures {
		path, err := fixturePath("internal/ffmpeg/testdata", m.Fixtures[i].Path)
		if err != nil {
			return err
		}
		// #nosec G304 -- fixturePath rejects non-basename and escaping paths.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m.Fixtures[i].SHA256 = hex.EncodeToString(sum[:])
	}
	m.FFmpegVersion = ffmpeg.PinnedVersion()
	m.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(manifestPath), ".manifest-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, manifestPath)
}

func fixturePath(root, name string) (string, error) {
	if name == "" || name != filepath.Base(name) {
		return "", fmt.Errorf("unsafe fixture path %q", name)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("fixture path %q escapes %q", name, root)
	}
	return path, nil
}
