package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed VERSION
var embeddedVersion string

var normalizedTime = time.Unix(0, 0).UTC()

type Config struct {
	Tier    string
	Profile string
	Seed    uint64
	Output  string
	Count   int // tests may override the tier count without adding a public tier
}

type Manifest struct {
	GeneratorVersion string        `json:"generator_version"`
	Seed             uint64        `json:"seed"`
	Tier             string        `json:"tier"`
	Profile          string        `json:"profile"`
	FileCount        int           `json:"file_count"`
	CorpusSHA256     string        `json:"corpus_sha256"`
	Stats            ManifestStats `json:"stats"`
}

type ManifestStats struct {
	BrokenFiles      int `json:"broken_files"`
	AdversarialNames int `json:"adversarial_names"`
	ZeroByteFiles    int `json:"zero_byte_files"`
	ImageFiles       int `json:"image_files"`
}

type Timings struct {
	GeneratorVersion     string `json:"generator_version"`
	Seed                 uint64 `json:"seed"`
	Tier                 string `json:"tier"`
	Profile              string `json:"profile"`
	FileCount            int    `json:"file_count"`
	ScanNanoseconds      int64  `json:"scan_nanoseconds"`
	SearchNanoseconds    int64  `json:"search_nanoseconds"`
	ThumbnailNanoseconds int64  `json:"thumbnail_nanoseconds"`
	SearchMatches        int    `json:"search_matches"`
	ThumbnailProbes      int    `json:"thumbnail_probes"`
}

type generatedFile struct {
	relPath     string
	data        []byte
	broken      bool
	adversarial bool
}

func generatorVersion() string { return strings.TrimSpace(embeddedVersion) }

func (c Config) Validate() error {
	if c.Output == "" {
		return errors.New("--output is required")
	}
	if _, err := tierCount(c.Tier); err != nil {
		return err
	}
	if c.Profile != "perf-realistic" && c.Profile != "messy-correctness" {
		return fmt.Errorf("unknown profile %q (want perf-realistic or messy-correctness)", c.Profile)
	}
	return nil
}

func tierCount(tier string) (int, error) {
	switch strings.ToLower(tier) {
	case "10k":
		return 10_000, nil
	case "100k":
		return 100_000, nil
	case "1m":
		return 1_000_000, nil
	default:
		return 0, fmt.Errorf("unknown tier %q (want 10k, 100k, or 1m)", tier)
	}
}

func (c Config) fileCount() int {
	if c.Count > 0 {
		return c.Count
	}
	count, _ := tierCount(c.Tier)
	return count
}

func Generate(cfg Config) (Manifest, error) {
	if err := cfg.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := requireEmptyTarget(cfg.Output); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.Output, "library"), 0o755); err != nil {
		return Manifest{}, err
	}

	corpusHash := sha256.New()
	manifest := Manifest{
		GeneratorVersion: generatorVersion(),
		Seed:             cfg.Seed,
		Tier:             strings.ToLower(cfg.Tier),
		Profile:          cfg.Profile,
		FileCount:        cfg.fileCount(),
	}
	for i := 0; i < manifest.FileCount; i++ {
		file := makeFile(cfg, i)
		fullPath := filepath.Join(cfg.Output, filepath.FromSlash(file.relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return Manifest{}, err
		}
		if err := os.WriteFile(fullPath, file.data, 0o644); err != nil {
			return Manifest{}, err
		}
		if err := os.Chtimes(fullPath, normalizedTime, normalizedTime); err != nil {
			return Manifest{}, err
		}
		digestEntry(corpusHash, file.relPath, file.data)
		if file.broken {
			manifest.Stats.BrokenFiles++
		}
		if file.adversarial {
			manifest.Stats.AdversarialNames++
		}
		if len(file.data) == 0 {
			manifest.Stats.ZeroByteFiles++
		}
		if isImage(file.relPath) {
			manifest.Stats.ImageFiles++
		}
	}
	manifest.CorpusSHA256 = hex.EncodeToString(corpusHash.Sum(nil))
	if err := writeJSON(filepath.Join(cfg.Output, "manifest.json"), manifest); err != nil {
		return Manifest{}, err
	}
	if err := normalizeDirectories(cfg.Output); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func makeFile(cfg Config, index int) generatedFile {
	value := splitmix64(cfg.Seed + uint64(index)*0x9e3779b97f4a7c15)
	extensions := []string{".mkv", ".mp4", ".mp3", ".jpg"}
	sizes := []int{0, 16, 64, 256}
	ext := extensions[int(value%uint64(len(extensions)))]
	size := sizes[int((value>>8)%uint64(len(sizes)))]
	name := fmt.Sprintf("%08d-title-%016x%s", index, value, ext)
	adversarial := false
	broken := false
	if cfg.Profile == "messy-correctness" {
		messySizes := []int{0, 1, 13, 257, 4096}
		size = messySizes[int((value>>8)%uint64(len(messySizes)))]
		switch {
		case index%43 == 0:
			name = fmt.Sprintf("%08d-CASE case Case%s", index, ext)
			adversarial = true
		case index%31 == 0:
			name = fmt.Sprintf("%08d-no-extension", index)
			adversarial = true
		case index%29 == 0:
			name = fmt.Sprintf("%08d-%s%s", index, strings.Repeat("very-long-name-", 10), ext)
			adversarial = true
		case index%17 == 0:
			name = fmt.Sprintf("%08d-  [é] 日本語 #frag %%25 %s", index, ext)
			adversarial = true
		}
		broken = index%37 == 0
	}
	groupA := index / 1_000_000
	groupB := (index / 1_000) % 1_000
	parts := []string{"library", fmt.Sprintf("%03d", groupA), fmt.Sprintf("%03d", groupB)}
	if cfg.Profile == "messy-correctness" && index%41 == 0 {
		parts = append(parts, "disc 01", "extras", "nested")
		adversarial = true
	}
	parts = append(parts, name)
	return generatedFile{
		relPath:     strings.Join(parts, "/"),
		data:        deterministicContent(cfg, index, size, broken),
		broken:      broken,
		adversarial: adversarial,
	}
}

func deterministicContent(cfg Config, index, size int, broken bool) []byte {
	if size == 0 {
		return []byte{}
	}
	seed := make([]byte, 24)
	binary.LittleEndian.PutUint64(seed[0:8], cfg.Seed)
	binary.LittleEndian.PutUint64(seed[8:16], uint64(index))
	binary.LittleEndian.PutUint64(seed[16:24], uint64(size))
	digest := sha256.Sum256(append(seed, cfg.Profile...))
	data := make([]byte, size)
	for i := range data {
		data[i] = digest[i%len(digest)]
	}
	if broken {
		copy(data, []byte{0x00, 0xff, 0x00, 0xff, 'B', 'R', 'O', 'K', 'E', 'N'})
	}
	return data
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func digestEntry(w io.Writer, relPath string, data []byte) {
	content := sha256.Sum256(data)
	fmt.Fprintf(w, "%s\x00%d\x00%x\n", relPath, len(data), content)
}

func requireEmptyTarget(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("output %s is not empty; refusing to mix corpus versions", path)
	}
	return nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return os.Chtimes(path, normalizedTime, normalizedTime)
}

func normalizeDirectories(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if err := os.Chtimes(directory, normalizedTime, normalizedTime); err != nil {
			return err
		}
	}
	return nil
}

func Measure(cfg Config) (Timings, error) {
	manifest, err := readManifest(cfg.Output)
	if err != nil {
		return Timings{}, err
	}
	if manifest.GeneratorVersion != generatorVersion() || manifest.Seed != cfg.Seed || manifest.Tier != strings.ToLower(cfg.Tier) || manifest.Profile != cfg.Profile {
		return Timings{}, errors.New("cached corpus tuple does not match generator-version, seed, tier, and profile")
	}
	library := filepath.Join(cfg.Output, "library")
	timings := Timings{GeneratorVersion: generatorVersion(), Seed: cfg.Seed, Tier: strings.ToLower(cfg.Tier), Profile: cfg.Profile}

	start := time.Now()
	err = filepath.WalkDir(library, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			timings.FileCount++
		}
		return err
	})
	timings.ScanNanoseconds = time.Since(start).Nanoseconds()
	if err != nil {
		return Timings{}, err
	}

	start = time.Now()
	err = filepath.WalkDir(library, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.Contains(strings.ToLower(entry.Name()), "title") {
			timings.SearchMatches++
		}
		return err
	})
	timings.SearchNanoseconds = time.Since(start).Nanoseconds()
	if err != nil {
		return Timings{}, err
	}

	start = time.Now()
	err = filepath.WalkDir(library, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !isImage(path) {
			return err
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		var sample [64]byte
		_, readErr := file.Read(sample[:])
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		timings.ThumbnailProbes++
		return nil
	})
	timings.ThumbnailNanoseconds = time.Since(start).Nanoseconds()
	if err != nil {
		return Timings{}, err
	}
	if timings.FileCount != manifest.FileCount {
		return Timings{}, fmt.Errorf("corpus has %d files; manifest requires %d", timings.FileCount, manifest.FileCount)
	}
	return timings, nil
}

func readManifest(root string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func isImage(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg") || strings.EqualFold(filepath.Ext(path), ".png")
}
