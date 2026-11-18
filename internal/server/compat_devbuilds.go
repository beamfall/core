package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/capabilities"
)

// devBuildsFileEnv names the operator's runtime dev-build catalog file
// (CORE-2396). It is opt-in: unset, the server admits only the builds its
// release linked in, exactly as before.
const devBuildsFileEnv = "BEAMFALL_DEV_BUILDS_FILE"

// devBuildsRecheckInterval bounds how often a handshake or enrollment stats the
// dev-build file, so a busy server does not stat it on every request.
const devBuildsRecheckInterval = 2 * time.Second

// maxDevBuildsFileBytes bounds the dev-build file read.
const maxDevBuildsFileBytes = 1 << 20

// devBuildsFile keeps the compatibility policy's runtime dev records in step
// with an operator-owned file on the server's disk. The file is the operator
// gate: only someone who can write the server's data volume and set its
// environment can add a record, and every load is logged with the build ids it
// admits. Records are parsed by capabilities.ParseDevBuildManifest, so only
// "-dev" build ids with direct distribution and viewer or manager roles load,
// and a record never replaces a release record. A missing, unreadable or
// invalid file installs an empty dev catalog: it fails closed.
type devBuildsFile struct {
	path   string
	compat *compatState
	now    func() time.Time

	// nextCheck is the unix-nano time of the next re-read, so the per-request
	// fast path skips the lock between checks.
	nextCheck atomic.Int64

	mu      sync.Mutex
	loaded  bool
	outcome string // content digest, or the read/parse error, of the last install
}

func newDevBuildsFile(path string, compat *compatState) *devBuildsFile {
	path = strings.TrimSpace(path)
	if path == "" || compat == nil {
		return nil
	}
	return &devBuildsFile{path: path, compat: compat, now: time.Now}
}

// refresh re-reads the file at most once per devBuildsRecheckInterval and
// reinstalls the dev catalog when its content or readability changed. It runs
// on every compatibility authorization, handshake and enrollment, so adding,
// changing, removing or making the file unreadable takes effect within the
// interval for every request, not only at the next handshake.
func (f *devBuildsFile) refresh() {
	if f == nil {
		return
	}
	if f.now().UnixNano() < f.nextCheck.Load() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.loaded && now.UnixNano() < f.nextCheck.Load() {
		return
	}
	f.nextCheck.Store(now.Add(devBuildsRecheckInterval).UnixNano())
	data, err := readBoundedFile(f.path, maxDevBuildsFileBytes)
	var catalog capabilities.BuildCatalog
	outcome := ""
	if err == nil {
		digest := sha256.Sum256(data)
		outcome = "sha256:" + hex.EncodeToString(digest[:])
		catalog, err = capabilities.ParseDevBuildManifest(data)
	}
	if err != nil {
		outcome = "error:" + err.Error()
	}
	if f.loaded && outcome == f.outcome {
		return
	}
	f.loaded, f.outcome = true, outcome
	f.install(catalog, err)
}

// install hands the parsed catalog (or, on any error, an empty one) to the
// compatibility policy and logs the outcome. Callers hold f.mu.
func (f *devBuildsFile) install(catalog capabilities.BuildCatalog, err error) {
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			slog.Warn("dev build catalog file not found; no dev builds admitted", "path", f.path)
		} else {
			slog.Error("dev build catalog file refused; no dev builds admitted", "path", f.path, "err", err)
		}
		f.compat.setDevBuilds(capabilities.BuildCatalog{})
		return
	}
	ids := make([]string, 0, len(catalog.Builds))
	for _, record := range catalog.Builds {
		ids = append(ids, record.BuildID+" "+string(record.Surface)+" "+artifactHashPrefix(record.ArtifactHash))
	}
	slog.Info("dev build catalog loaded", "path", f.path, "count", len(ids), "builds", ids)
	f.compat.setDevBuilds(catalog)
}

// artifactHashPrefix is the part of an artifact digest the logs carry: enough
// to tell builds apart, never the whole value a client presents.
func artifactHashPrefix(hash string) string {
	const n = 12
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-set BEAMFALL_DEV_BUILDS_FILE path.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("dev build catalog file exceeds %d bytes", limit)
	}
	return data, nil
}
