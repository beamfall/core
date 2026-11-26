package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

//go:embed migrate/catalog/*.sql
var catalogMigrations embed.FS

//go:embed migrate/events/*.sql
var eventsMigrations embed.FS

//go:embed migrate/encrypted_library/*.sql
var encryptedLibraryMigrations embed.FS

// seedArtworkFiles holds synthetic, license-clean placeholder artwork the
// conformance and demo seeds land on disk so the read-path contract serves real
// bytes for seeded Media. Files are named purely by Media id + artwork slot so
// no content-domain vocabulary enters Core (AGPL firewall — Core selects
// artwork by id/slot only).
//
//go:embed seedart/*.png
var seedArtworkFiles embed.FS

//go:embed seedmedia/acceptance-review-demo.mp4
var acceptanceReviewDemoMP4 []byte

// Options controls durable store opening.
type Options struct {
	SeedDevelopmentData bool
	SeedProfile         SeedProfile
	AllowNetworkDataDir bool
	AllowedRoots        []string
	// TrashDisabled turns the trash.enabled config toggle OFF for this store
	// (hard invariant 6: with trash disabled, autonomous destruction paths
	// skip). Inverted so the zero value keeps trash ON — the default.
	// Already-trashed items stay restorable and expired trash still purges.
	TrashDisabled bool
	// Logger receives the open/migration log lines (applying migrations,
	// pre-migration backups, store opened). A nil logger defaults to
	// slog.Default(), preserving the previous global-logger behavior; callers
	// that pass a quiet logger can silence migration noise.
	Logger *slog.Logger
	// EnableEncryptedLibraryDBLifecycle enables the ENC-DB-4 per-Encrypted-Library
	// SQLite DB lifecycle for tests and future routing work. Production app
	// composition keeps this disabled until encrypted-library backup/restore
	// manifest coverage is enabled.
	EnableEncryptedLibraryDBLifecycle bool
	// Role is the process authority posture. The zero value (Main)
	// keeps every authoring path open. An Attached Origin Node refuses
	// catalog-identity authoring and engagement-projection writes — it
	// index-pushes its locally-identified index instead (ADR-0123 decision 2/3).
	Role serverrole.Role
	// RequireExisting refuses to open a data directory that holds no Beamfall
	// database, and creates nothing there (CORE-1599). Offline commands that
	// only make sense against an existing server (pairing-code,
	// reset-admin-password, the capabilities overrides) set it so a mistyped --data-dir is
	// reported instead of silently becoming a new, empty server.
	RequireExisting bool
}

// ErrNoDataDir is returned by Open with Options.RequireExisting when the data
// directory holds no Beamfall database.
var ErrNoDataDir = errors.New("no Beamfall data")

// SeedProfile selects an optional boot-time catalog seed.
type SeedProfile string

const (
	SeedProfileDevelopment SeedProfile = "development"
	SeedProfileDemo        SeedProfile = "demo"
	// SeedProfileConformance seeds the deterministic conformance corpus world
	// (the development seed plus primary Media Files for media-alpha and
	// media-archive). It is the on-disk twin of the in-process world
	// internal/conformance/golden_corpus_test.go composes, so a real HTTP
	// listener can serve the corpus goldens byte-identically when paired with
	// the fixed clock + fixed Stream-Token key the app applies for this profile.
	// Test/dev only — never reached from a production serve (config rejects it
	// unless BEAMFALL_ALLOW_CONFORMANCE_SEED=1).
	SeedProfileConformance SeedProfile = "conformance"
	// SeedProfileAcceptance is the single owner of the deterministic fixture
	// state the Apple e2e test layers consume (apple-0001 D2): a pre-enrolled
	// device, N profiles, a library/detail matrix, one short CLEAR (H.264/AAC)
	// playable asset, a token-expiry/relock surface, a server-seeded admin
	// (Manager-role device + admin-credential + admin session) credential, and
	// the breadth states (search corpus, resume/progress, up-next queue) ATEST
	// -16/17 need. It plants publicly known PIN/secret/admin-password material,
	// so it is test/dev only — config rejects it (and --seed-development-data)
	// unless --allow-test-seed / BEAMFALL_ALLOW_TEST_SEED=1. It is versioned
	// (AcceptanceSeedVersion) so the Apple harness can pin it.
	SeedProfileAcceptance SeedProfile = "acceptance"
)

// AcceptanceSeedVersion is the version token of the SeedProfileAcceptance
// fixture state. The Apple e2e harness pins this in core-contract.lock and
// asserts it via /health. Bump it (by hand) whenever the
// acceptance fixture states change; the bump cadence/ownership is recorded per
// ATEST-GOV-1.
const AcceptanceSeedVersion = "acceptance-2"

// Open creates or opens the durable Beamfall store under dataDir.
func Open(ctx context.Context, dataDir string, opts Options) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("data-dir is required")
	}
	// SQLite opens a file: URI built from this path, and a relative path there
	// parses as a URI authority ("invalid uri authority"), so resolve it against
	// the working directory first (CORE-1599). Absolute paths are unchanged.
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data-dir: %w", err)
	}
	if opts.RequireExisting {
		if err := requireExistingDataDir(ctx, dataDir); err != nil {
			return nil, err
		}
	}
	if err := guardDataDirPlacement(dataDir, opts.AllowNetworkDataDir); err != nil {
		return nil, err
	}
	if err := recoverRestorePublish(dataDir); err != nil {
		return nil, fmt.Errorf("recover interrupted restore publish: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, err
	}

	catalogPath := filepath.Join(dbDir, "catalog.db")
	eventsPath := filepath.Join(dbDir, "events.db")
	backupDir := filepath.Join(dataDir, "backups", "pre-migration")

	st := &Store{
		dataDir:                    dataDir,
		catalogPath:                catalogPath,
		eventsPath:                 eventsPath,
		now:                        time.Now,
		trashDisabled:              opts.TrashDisabled,
		logger:                     logger,
		encryptedDBRoot:            filepath.Join(dbDir, "encrypted-library"),
		encryptedDBWorkingRoot:     filepath.Join(dbDir, "encrypted-library-working"),
		encryptedDBs:               map[LibraryID]*encryptedLibraryDB{},
		encryptedDBLifecycleEnable: opts.EnableEncryptedLibraryDBLifecycle,
		mountCapabilityProbe:       newMountCapabilityProbe(filepath.Join(dataDir, "cache", "mount-capabilities.json")),
		role:                       opts.Role,
	}
	allowedRoots, err := loadPathPickerAllowedRoots(dataDir, opts.AllowedRoots)
	if err != nil {
		return nil, err
	}
	st.allowedRoots = allowedRoots
	if err := st.sweepBackupIngestPlainScratch(); err != nil {
		return nil, err
	}
	if err := st.reconcileEncryptedLibraryDBs(ctx); err != nil {
		return nil, err
	}
	if st.catalogW, err = openSQLiteWriter(catalogPath); err != nil {
		return nil, err
	}
	if err := checkSQLiteIntegrity(ctx, st.catalogW, "catalog", catalogPath); err != nil {
		_ = st.Close()
		return nil, err
	}
	if st.eventsW, err = openSQLiteWriter(eventsPath); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := checkSQLiteIntegrity(ctx, st.eventsW, "events", eventsPath); err != nil {
		_ = st.Close()
		return nil, err
	}

	catalogApplied, err := migrateCounted(ctx, logger, st.catalogW, catalogPath, "catalog", catalogMigrations, "migrate/catalog", backupDir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	eventsApplied, err := migrateCounted(ctx, logger, st.eventsW, eventsPath, "events", eventsMigrations, "migrate/events", backupDir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// One summary line rather than one per migration (CORE-1603): a fresh
	// install applies well over a hundred, burying the first-run setup URL.
	if catalogApplied+eventsApplied > 0 {
		logger.InfoContext(ctx, "applied migrations", "catalog", catalogApplied, "events", eventsApplied)
	}
	if profile := opts.resolvedSeedProfile(); profile != "" {
		if err := st.seedData(ctx, profile); err != nil {
			_ = st.Close()
			return nil, err
		}
		if err := st.seedProfileEntryPINs(ctx); err != nil {
			_ = st.Close()
			return nil, err
		}
		st.registerSeedKindPolicies(profile)
	}
	if st.catalogR, err = openSQLiteReader(catalogPath); err != nil {
		_ = st.Close()
		return nil, err
	}
	if st.eventsR, err = openSQLiteReader(eventsPath); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.recoverVersionCopyEngagement(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("recover Version merge engagement: %w", err)
	}
	if err := st.importIntegrationAuthAttempts(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("import integration auth attempts: %w", err)
	}
	if err := st.resumeProfileFootprintDeletes(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("resume profile-footprint deletion: %w", err)
	}
	if err := recordSuccessfulMigrationBoot(dataDir); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("record successful migration boot: %w", err)
	}
	logger.InfoContext(ctx, "store opened", "dataDir", dataDir)
	return st, nil
}

// requireExistingDataDir reports ErrNoDataDir unless dataDir holds a Beamfall
// catalog database or an interrupted restore publish that Open will recover.
// It only reads: a catalog.db that is empty or is not a Beamfall catalog is
// refused before Open creates or modifies anything (CORE-1599).
func requireExistingDataDir(ctx context.Context, dataDir string) error {
	_, err := os.Stat(filepath.Join(dataDir, restorePublishTransactionName)) // #nosec G703 -- the data directory the operator named; fixed file name
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	catalogPath := filepath.Join(dataDir, "db", "catalog.db")
	info, err := os.Stat(catalogPath) // #nosec G703 -- the data directory the operator named; fixed file name
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w in %s", ErrNoDataDir, dataDir)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !isBeamfallCatalog(ctx, catalogPath) {
		return fmt.Errorf("%w in %s: %s is empty or is not a Beamfall catalog", ErrNoDataDir, dataDir, catalogPath)
	}
	return nil
}

// sqliteFileHeader opens every SQLite database file.
const sqliteFileHeader = "SQLite format 3\x00"

// catalogProbeCopyLimit caps the bytes isBeamfallCatalog copies to read a
// ledger that is still only in the WAL. Only a young catalog has never
// checkpointed its first migration into the main file, and it is small.
const catalogProbeCopyLimit = 64 << 20

// isBeamfallCatalog reports whether path is an SQLite database whose
// schema_migrations ledger records the first catalog migration with the
// checksum the migrator itself recorded for it, which a foreign database does
// not have by accident. It checks the file header itself first, so an empty or
// foreign file never reaches SQLite.
//
// The probe creates and writes no file beside the database, whatever the
// owner of the database does meanwhile (CORE-1599). SQLite opens the database
// itself only immutable, which reads the main file alone, takes no locks and
// never creates a -wal or -shm. A running server can still hold its whole
// ledger in the WAL (a fresh data dir does until its first checkpoint), so
// when the main file lacks it and a -wal is present the probe copies the main
// file and the -wal, read-only and at most catalogProbeCopyLimit bytes, into a
// private temporary directory and lets SQLite read the copy there. A database
// that changes while it is copied may be misread, which refuses it; the main
// file is probed once more afterwards in case a checkpoint landed meanwhile.
func isBeamfallCatalog(ctx context.Context, path string) bool {
	f, err := os.Open(path) // #nosec G304 G703 -- path is db/catalog.db under the data directory the operator named
	if err != nil {
		return false
	}
	header := make([]byte, len(sqliteFileHeader))
	_, err = io.ReadFull(f, header)
	_ = f.Close()
	if err != nil || string(header) != sqliteFileHeader {
		return false
	}
	return catalogLedgerMatches(ctx, path, "mode=ro&immutable=1") ||
		walCatalogCopyMatches(ctx, path) ||
		catalogLedgerMatches(ctx, path, "mode=ro&immutable=1")
}

// catalogLedgerMatches opens path with the given URI query and compares the
// ledger's first catalog migration checksum with the embedded one.
func catalogLedgerMatches(ctx context.Context, path, query string) bool {
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String())
	if err != nil {
		return false
	}
	defer db.Close()
	first, err := fs.ReadFile(catalogMigrations, "migrate/catalog/0001_initial.sql")
	if err != nil {
		return false
	}
	checksum, err := migrationChecksumInDB(ctx, db, "0001_initial.sql")
	return err == nil && checksum == migrationChecksum(first)
}

// walCatalogCopyMatches probes a private copy of path and its -wal, so that
// SQLite's -wal and -shm handling only ever touches the temporary directory.
func walCatalogCopyMatches(ctx context.Context, path string) bool {
	if wal, _ := pathExists(path + "-wal"); !wal {
		return false
	}
	dir, err := os.MkdirTemp("", "beamfall-catalog-probe-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "catalog.db")
	budget := int64(catalogProbeCopyLimit)
	for _, suffix := range []string{"", "-wal"} {
		n, err := copyFileBounded(path+suffix, copyPath+suffix, budget)
		if err != nil {
			return false
		}
		budget -= n
	}
	return catalogLedgerMatches(ctx, copyPath, "mode=ro")
}

// copyFileBounded copies src to a new 0600 file dst and fails when src holds
// more than limit bytes.
func copyFileBounded(src, dst string, limit int64) (int64, error) {
	in, err := os.Open(src) // #nosec G304 G703 -- src is the catalog or its -wal under the data directory the operator named
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- dst is inside a directory this process just created
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > limit {
		err = fmt.Errorf("%s exceeds %d bytes", src, limit)
	}
	return n, err
}

// registerSeedKindPolicies declares the kind postures of a seed's own
// Entities. The demo and acceptance worlds hold an ordered "container" Entity
// that no plugin declares, so the seed registers it with the Container posture
// that plugin activation gives a container-archetype kind; otherwise their
// next-up Source would stay empty (CORE-2822). Registered on every Open, since
// the policies live in memory while the seeded rows persist.
func (s *Store) registerSeedKindPolicies(profile SeedProfile) {
	if profile != SeedProfileDemo && profile != SeedProfileAcceptance {
		return
	}
	if s.entityKindPolicies == nil {
		s.entityKindPolicies = map[string]EntityKindPolicy{}
	}
	policy := s.entityKindPolicies["container"]
	policy.Kind, policy.Container = "container", true
	s.entityKindPolicies["container"] = policy
}

func (opts Options) resolvedSeedProfile() SeedProfile {
	if opts.SeedProfile != "" {
		return opts.SeedProfile
	}
	if opts.SeedDevelopmentData {
		return SeedProfileDevelopment
	}
	return ""
}

// seedData is the small domain-agnostic development catalog for the first app handshake.
type seedData struct {
	libraries   []Library
	profiles    []Profile
	devices     []Device
	sessions    []Session
	media       []Media
	mediaFiles  []MediaFile
	mediaFields []seedMediaFields
	entities    []Entity
	relations   []EntityRelation
	markers     []Marker
	progress    []PlaybackProgress
	// mediaFileStreams records the per-file stream inventory (video/audio/...)
	// for seeded Media Files whose playback-info surface must expose real track
	// data (acceptance profile). insertSeedCatalog replaces the inventory rows
	// for each fingerprint after the media_file rows land.
	mediaFileStreams []seedFileStreams
	// adminPassword, when non-empty, seeds the server admin-credential verifier
	// with adminSalt so the Console admin window is
	// reachable with a known password on a non-production host (acceptance
	// profile). adminSalt is a fixed value so the verifier is reproducible
	// across re-seeds — it is never minted via the random-salt SetAdminPassword.
	adminPassword string
	adminSalt     []byte
	// seedFileContent writes deterministic byte payloads for seeded Media Files
	// whose on-disk content must match a recorded fingerprint (conformance
	// profile). ensureSeedMediaFiles writes these before the empty-file default.
	seedFileContent []seedFile
	// artwork lands placeholder artwork bytes under <dataDir>/artwork so the
	// read-path contract serves real images for the corpus Media (conformance
	// profile). writeSeedArtwork writes these with a pinned modification time so
	// the artwork version token (and thus the artwork URL) is deterministic.
	artwork []seedArtwork
}

type seedFile struct {
	path    string
	content []byte
}

// seedArtwork is one placeholder artwork file to land for a Media item's slot.
// dst is the on-disk path the artwork serving path resolves (the artwork
// service stores Media artwork at <dataDir>/artwork/<base(id)>/<slot>.jpg); the
// store mirrors that layout here rather than importing the artwork package so
// the seed stays a leaf of the import graph.
type seedArtwork struct {
	dst     string
	content []byte
}

type seedMediaFields struct {
	mediaID MediaID
	fields  []MediaField
}

// seedFileStreams is the stream inventory for one seeded Media File, keyed by
// its content fingerprint (the same key media_file_stream rows carry).
type seedFileStreams struct {
	fingerprint string
	streams     []MediaFileStream
}

// seedRevealPINHash gives seeded adult profiles the development reveal PIN
// ("1234"); managed profiles have no reveal path.
func seedRevealPINHash(kind ProfileKind) any {
	if kind == ProfileAdult {
		return HashDeviceSecret("1234")
	}
	return nil
}

func developmentSeed() seedData {
	return seedData{
		libraries: []Library{
			{ID: "library-local", Title: "Local Library"},
			{ID: "library-archive", Title: "Archive", IsRestricted: true},
		},
		profiles: []Profile{
			{
				ID:      "profile-default",
				Name:    "Default",
				Kind:    ProfileAdult,
				Library: []LibraryID{"library-local", "library-archive"},
			},
			{
				ID:      "profile-managed",
				Name:    "Managed",
				Kind:    ProfileManaged,
				Library: []LibraryID{"library-local"},
			},
		},
		devices: []Device{
			{ID: "device-tv", Name: "TV", Role: DeviceRoleViewer, SecretHash: "131058435ad09141ee4bec6f4307e9809ea91260f6f0eab8d2daa517ed247c9f"},
			{ID: "device-mac", Name: "Manager", Role: DeviceRoleManager, SecretHash: "78ca3ded2d9bba1d0a8dbf437979474a6259ef14cd81949d398500b44c09e65b"},
			{ID: "device-revoked", Name: "Revoked", Role: DeviceRoleViewer, SecretHash: "d78c0f34114ca9047d03d9f0c3186fb45de4e460fdb6ed30a4b580e6002f919a", Revoked: true},
		},
		sessions: []Session{
			{ID: "session-default", DeviceID: "device-tv", ProfileID: "profile-default"},
			{ID: "session-revealed", DeviceID: "device-tv", ProfileID: "profile-default", Revealed: true},
			{ID: "session-manager", DeviceID: "device-mac", ProfileID: "profile-default"},
			{ID: "session-managed", DeviceID: "device-tv", ProfileID: "profile-managed"},
			{ID: "session-revoked", DeviceID: "device-revoked", ProfileID: "profile-default"},
		},
		media: []Media{
			{
				ID:              "media-alpha",
				LibraryID:       "library-local",
				Title:           "Sample Media",
				DurationSeconds: 120,
				Container:       "mp4",
				Codec:           "h264",
				VideoWidth:      1280,
				VideoHeight:     720,
			},
			{
				ID:              "media-archive",
				LibraryID:       "library-archive",
				Title:           "Archive Media",
				DurationSeconds: 90,
				Container:       "mp4",
				Codec:           "h264",
				VideoWidth:      1920,
				VideoHeight:     1080,
			},
		},
	}
}

// conformanceSeed is the development world plus primary Media Files for
// media-alpha and media-archive, written under <dataDir>/conformance-media as
// the single-byte payloads the in-process corpus (golden_corpus_test.go) uses
// (byte 0x00 for media-alpha, 0x01 for media-archive). With the app's fixed
// clock + fixed Stream-Token key for this profile, a real HTTP listener serves
// the corpus playback-info goldens byte-identically. The bytes never ride the
// wire contract (no fingerprint appears on the playback-info surface); all that
// matters is that media-alpha resolves to a playable file. The content
// fingerprint recorded in the seed matches the bytes on disk so the file plane
// stays internally consistent.
func conformanceSeed(dataDir string) seedData {
	seed := developmentSeed()
	mediaDir := filepath.Join(dataDir, "conformance-media")
	for _, f := range []struct {
		mediaID MediaID
		content byte
	}{
		{"media-alpha", 0x00},
		{"media-archive", 0x01},
	} {
		path := filepath.Join(mediaDir, string(f.mediaID)+".bin")
		sum := sha256.Sum256([]byte{f.content})
		seed.mediaFiles = append(seed.mediaFiles, MediaFile{
			MediaID:            f.mediaID,
			Path:               path,
			ContentFingerprint: "sha256:" + hex.EncodeToString(sum[:]),
		})
		seed.seedFileContent = append(seed.seedFileContent, seedFile{path: path, content: []byte{f.content}})
	}
	seed.artwork = conformanceArtwork(dataDir)
	return seed
}

// conformanceArtwork maps the embedded placeholder artwork onto the corpus
// Media's poster/backdrop slots. The embedded filenames carry the Media id and
// slot only ("<id>.<slot>.png") — Core never sees the artwork pack's content
// vocabulary. Each file lands at the path the artwork serving path resolves
// (<dataDir>/artwork/<base(id)>/<slot>.jpg); a missing or unreadable embed is a
// build/source error, so a read failure here is fatal to the seed.
func conformanceArtwork(dataDir string) []seedArtwork {
	artworkDir := filepath.Join(dataDir, "artwork")
	// Slot names are Core's fixed artwork-slot vocabulary (poster, backdrop);
	// the served file is always "<slot>.jpg" regardless of the source bytes'
	// container, matching the artwork service's on-disk layout.
	want := []struct {
		mediaID MediaID
		slot    string
	}{
		{"media-alpha", "poster"},
		{"media-alpha", "backdrop"},
		{"media-archive", "poster"},
		{"media-archive", "backdrop"},
	}
	out := make([]seedArtwork, 0, len(want))
	for _, w := range want {
		data, err := seedArtworkFiles.ReadFile("seedart/" + string(w.mediaID) + "." + w.slot + ".png")
		if err != nil {
			// Embedded at build time; an error means the asset set drifted.
			panic(fmt.Sprintf("seed artwork %s/%s missing from embed: %v", w.mediaID, w.slot, err))
		}
		dst := filepath.Join(artworkDir, filepath.Base(string(w.mediaID)), w.slot+".jpg")
		out = append(out, seedArtwork{dst: dst, content: data})
	}
	return out
}

// Acceptance-profile fixed, publicly known test credentials. These are safe
// ONLY because the acceptance profile hard-refuses to boot under a production
// config (config requires --allow-test-seed); they let the Apple e2e harness
// drive a known world deterministically.
const (
	// AcceptanceViewerSecret is the pre-enrolled viewer device's secret.
	AcceptanceViewerSecret = "acceptance-viewer-secret"
	// AcceptanceManagerSecret is the seeded admin (Manager-role) device secret.
	AcceptanceManagerSecret = "acceptance-manager-secret"
	// AcceptanceAdminPassword is the seeded server admin (Console) password.
	AcceptanceAdminPassword = "acceptance-admin-password"
	// AcceptanceRevealPIN is the adult-profile reveal PIN (matches the seed
	// default "1234" planted by seedRevealPINHash).
	AcceptanceRevealPIN = "1234"
)

// acceptanceSeed is the single owner of the deterministic fixture state the
// Apple e2e test layers consume (apple-0001 D2 / SeedProfileAcceptance). It is
// independent of the byte-locked conformance corpus so changes here never
// perturb internal/conformance goldens. Every value is fixed so the world is
// reproducible; the credentials are publicly known test values gated behind
// --allow-test-seed (never a production serve). Bump AcceptanceSeedVersion when
// any documented state below changes.
// acceptanceAdminWindowEnd is the seeded Console admin-session window end. The
// acceptance profile runs on a real wall clock (unlike conformance, which pins
// a fixed read-clock), so the Console admin window is evaluated against the
// server's actual time. It is pinned far in the future so the seeded admin
// authorization is always active whenever the Apple harness boots the world,
// while staying byte-stable (deterministic) across re-seeds.
var acceptanceAdminWindowEnd = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

func acceptanceSeed(dataDir string) seedData {
	seedTime := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	// (4) Short CLEAR (H.264/AAC) playable assets: real on-disk files whose
	// fingerprints match the recorded bytes, plus video+audio stream inventory
	// so playback-info exposes real track data. The resume shelf uses Aurora and
	// Harbor first, so those rows must be playable too.
	type playableSeedFile struct {
		mediaID     MediaID
		path        string
		content     []byte
		fingerprint string
	}
	playableFiles := []playableSeedFile{
		{mediaID: "media-acc-clear", path: filepath.Join(dataDir, "acceptance-media", "media-acc-clear.mp4"), content: append([]byte{}, acceptanceReviewDemoMP4...)},
		{mediaID: "media-acc-search-aurora", path: filepath.Join(dataDir, "acceptance-media", "media-acc-search-aurora.mp4"), content: append(append([]byte{}, acceptanceReviewDemoMP4...), 0x43)},
		{mediaID: "media-acc-search-harbor", path: filepath.Join(dataDir, "acceptance-media", "media-acc-search-harbor.mp4"), content: append(append([]byte{}, acceptanceReviewDemoMP4...), 0x44)},
	}
	mediaFiles := make([]MediaFile, 0, len(playableFiles))
	seedFiles := make([]seedFile, 0, len(playableFiles))
	streamInventories := make([]seedFileStreams, 0, len(playableFiles))
	for i := range playableFiles {
		sum := sha256.Sum256(playableFiles[i].content)
		playableFiles[i].fingerprint = "sha256:" + hex.EncodeToString(sum[:])
		mediaFiles = append(mediaFiles, MediaFile{
			MediaID:            playableFiles[i].mediaID,
			Path:               playableFiles[i].path,
			ContentFingerprint: playableFiles[i].fingerprint,
		})
		seedFiles = append(seedFiles, seedFile{path: playableFiles[i].path, content: playableFiles[i].content})
		streamInventories = append(streamInventories, seedFileStreams{
			fingerprint: playableFiles[i].fingerprint,
			streams: []MediaFileStream{
				{Index: 0, Type: "video", Codec: "h264", Profile: "high", Default: true},
				{Index: 1, Type: "audio", Codec: "aac", Language: "eng", Default: true, Channels: 2, ChannelLayout: "stereo"},
			},
		})
	}

	// (7) Search corpus + up-next queue + resume/progress breadth media. Titles
	// are distinct so a search corpus has unambiguous hits (ATEST-16/17).
	searchMedia := []Media{
		{ID: "media-acc-search-aurora", LibraryID: "library-acc-open", Title: "Aurora Over Fjords", Summary: "A calm aurora time-lapse.", Year: 2021, DurationSeconds: 300, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-acc-search-harbor", LibraryID: "library-acc-open", Title: "Harbor at Dawn", Summary: "A quiet harbor establishing shot.", Year: 2022, DurationSeconds: 240, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-acc-search-meadow", LibraryID: "library-acc-open", Title: "Meadow Light", Summary: "A pastoral meadow study.", Year: 2023, DurationSeconds: 180, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-acc-seq-1", LibraryID: "library-acc-open", Title: "Sequence Part One", Summary: "First ordered child for the up-next queue.", Year: 2024, DurationSeconds: 600, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-acc-seq-2", LibraryID: "library-acc-open", Title: "Sequence Part Two", Summary: "Second ordered child for the up-next queue.", Year: 2024, DurationSeconds: 610, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-acc-restricted", LibraryID: "library-acc-archive", Title: "Restricted Reel", Summary: "An innocuous restricted-library sample for Reveal/relock.", Year: 2022, DurationSeconds: 200, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
	}

	media := append([]Media{
		// (4) the clear A/V asset.
		{ID: "media-acc-clear", LibraryID: "library-acc-open", Title: "Clear Sample", Summary: "Short clear H.264/AAC playable asset.", Year: 2025, DurationSeconds: 30, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		// (8) ATEST-26 destructive-choreography targets. These items are dedicated to
		// the trash/restore E2E. Each test must restore the item it trashes so the
		// shared acceptance server stays consistent across nightly runs.
		{ID: "media-acc-trash-primary", LibraryID: "library-acc-open", Title: "Trash Target Alpha", Summary: "Primary ATEST-26 trash/restore target.", Year: 2025, DurationSeconds: 60, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-acc-trash-secondary", LibraryID: "library-acc-open", Title: "Trash Target Beta", Summary: "Secondary ATEST-26 trash/restore target (collision test).", Year: 2025, DurationSeconds: 60, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
	}, searchMedia...)

	return seedData{
		// (3) library/detail matrix: one unrestricted + one restricted library.
		libraries: []Library{
			{ID: "library-acc-open", Title: "Acceptance Open"},
			{ID: "library-acc-archive", Title: "Acceptance Archive", IsRestricted: true},
		},
		// (2) N profiles: one adult (reveal-capable), one managed (no reveal).
		profiles: []Profile{
			{ID: "profile-acc-default", Name: "Acceptance", Kind: ProfileAdult, Library: []LibraryID{"library-acc-open", "library-acc-archive"}},
			{ID: "profile-acc-managed", Name: "Acceptance Managed", Kind: ProfileManaged, Library: []LibraryID{"library-acc-open"}},
		},
		// (1) pre-enrolled viewer device; (6) Manager-role admin device.
		devices: []Device{
			{ID: "device-acc-viewer", Name: "Acceptance TV", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret(AcceptanceViewerSecret)},
			{ID: "device-acc-manager", Name: "Acceptance Manager", Role: DeviceRoleManager, SecretHash: HashDeviceSecret(AcceptanceManagerSecret)},
		},
		// Sessions exercise (5) token-expiry/relock and (6) the Console admin
		// window: one unrevealed (relock baseline), one revealed (reveal state),
		// one already-expired (token-expiry), and one Manager session whose
		// AdminUntil is in the future (Console authorization).
		sessions: []Session{
			{ID: "session-acc-viewer", DeviceID: "device-acc-viewer", ProfileID: "profile-acc-default"},
			{ID: "session-acc-revealed", DeviceID: "device-acc-viewer", ProfileID: "profile-acc-default", Revealed: true},
			{ID: "session-acc-expired", DeviceID: "device-acc-viewer", ProfileID: "profile-acc-default", ExpiresAt: seedTime.Add(-time.Hour)},
			{ID: "session-acc-managed", DeviceID: "device-acc-viewer", ProfileID: "profile-acc-managed"},
			{ID: "session-acc-admin", DeviceID: "device-acc-manager", ProfileID: "profile-acc-default", AdminUntil: acceptanceAdminWindowEnd},
		},
		media:            media,
		mediaFiles:       mediaFiles,
		seedFileContent:  seedFiles,
		mediaFileStreams: streamInventories,
		// (7) up-next queue: an ordered container entity → ordered children.
		entities: []Entity{
			{ID: "entity-acc-sequence", LibraryID: "library-acc-open", Kind: "container", Title: "Acceptance Sequence"},
		},
		relations: []EntityRelation{
			{FromEntityID: "entity-acc-sequence", Name: "contains", ToMediaID: "media-acc-seq-1", SortOrder: intPtr(1)},
			{FromEntityID: "entity-acc-sequence", Name: "contains", ToMediaID: "media-acc-seq-2", SortOrder: intPtr(2)},
		},
		// (7) resume/playback-progress: partial positions (resume shelf).
		progress: []PlaybackProgress{
			{ProfileID: "profile-acc-default", MediaID: "media-acc-search-aurora", PositionSeconds: 90, DurationSeconds: 300, Watched: false, PlayCount: 0, UpdatedAt: seedTime.Add(2 * time.Minute)},
			{ProfileID: "profile-acc-default", MediaID: "media-acc-search-harbor", PositionSeconds: 60, DurationSeconds: 240, Watched: false, PlayCount: 0, UpdatedAt: seedTime.Add(time.Minute)},
			{ProfileID: "profile-acc-default", MediaID: "media-acc-seq-1", PositionSeconds: 0, DurationSeconds: 600, Watched: true, PlayCount: 1, UpdatedAt: seedTime},
		},
		// (6) server-seeded admin (Console) credential — device/role only, no
		// entitlement/account code (invariant 7).
		adminPassword: AcceptanceAdminPassword,
		adminSalt:     []byte("acceptance-seed-admin-salt-0001!"),
	}
}

func demoSeed(dataDir string) seedData {
	seedTime := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	// Playback progress is anchored to seeding time, not seedTime: next-up only
	// counts engagement inside nextUpEngagementWindowMS, so a fixed date empties
	// the demo's next-up shelf once it ages out of that window.
	progressTime := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	mediaRows := []Media{
		{ID: "media-demo-bunny", LibraryID: "library-demo-open", Title: "Big Buck Bunny", Summary: "A bright short with broad appeal.", Year: 2008, DurationSeconds: 596, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-sintel", LibraryID: "library-demo-open", Title: "Sintel", Summary: "A compact fantasy journey.", Year: 2010, DurationSeconds: 888, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-steel", LibraryID: "library-demo-open", Title: "Tears of Steel", Summary: "Science fiction with a city-scale finale.", Year: 2012, DurationSeconds: 734, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 3840, VideoHeight: 2160},
		{ID: "media-demo-spring", LibraryID: "library-demo-open", Title: "Spring", Summary: "A pastoral animated short.", Year: 2019, DurationSeconds: 466, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-charge", LibraryID: "library-demo-open", Title: "Charge", Summary: "A kinetic open production sample.", Year: 2022, DurationSeconds: 492, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-caminandes", LibraryID: "library-demo-open", Title: "Caminandes", Summary: "A brisk comedy shelf item.", Year: 2013, DurationSeconds: 90, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-demo-series-1", LibraryID: "library-demo-open", Title: "Open Sequence One", Summary: "The first ordered child for next-up demos.", Year: 2024, DurationSeconds: 600, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-demo-series-2", LibraryID: "library-demo-open", Title: "Open Sequence Two", Summary: "The second ordered child for next-up demos.", Year: 2024, DurationSeconds: 610, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-demo-orbit", LibraryID: "library-demo-archive", Title: "Orbital Sunrise", Summary: "Public-domain space footage sample.", Year: 2021, DurationSeconds: 360, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-aurora", LibraryID: "library-demo-archive", Title: "Aurora Pass", Summary: "Calm atmospheric footage for browse variety.", Year: 2020, DurationSeconds: 420, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1920, VideoHeight: 1080},
		{ID: "media-demo-guarded-1", LibraryID: "library-demo-guarded", Title: "Quiet Meadow", Summary: "Innocuous restricted sample for Reveal demos.", Year: 2022, DurationSeconds: 300, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
		{ID: "media-demo-guarded-2", LibraryID: "library-demo-guarded", Title: "Forest Creek", Summary: "Second restricted sample for policy demos.", Year: 2023, DurationSeconds: 320, Container: "mp4", Codec: "h264", Class: MediaClassVideo, VideoWidth: 1280, VideoHeight: 720},
	}
	mediaRows = append(mediaRows, generatedDemoMediaRows()...)
	generated := loadDemoMediaManifest(dataDir)
	for i := range mediaRows {
		if file, ok := generated[mediaRows[i].ID]; ok {
			mediaRows[i].DurationSeconds = file.DurationSeconds
			mediaRows[i].Container = file.Container
			mediaRows[i].Codec = file.Codec
			mediaRows[i].VideoWidth = file.VideoWidth
			mediaRows[i].VideoHeight = file.VideoHeight
		}
	}
	return seedData{
		libraries: []Library{
			{ID: "library-demo-open", Title: "Open Demo"},
			{ID: "library-demo-archive", Title: "Space Archive"},
			{ID: "library-demo-guarded", Title: "Family Controls", IsRestricted: true},
		},
		profiles: []Profile{
			{ID: "profile-demo-default", Name: "Demo", Kind: ProfileAdult, Library: []LibraryID{"library-demo-open", "library-demo-archive", "library-demo-guarded"}},
			{ID: "profile-demo-managed", Name: "Managed Demo", Kind: ProfileManaged, Library: []LibraryID{"library-demo-open", "library-demo-archive"}},
		},
		devices: []Device{
			{ID: "device-demo-tv", Name: "Demo TV", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("demo-viewer-secret")},
			{ID: "device-demo-manager", Name: "Demo Manager", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("demo-manager-secret")},
		},
		sessions: []Session{
			{ID: "session-demo-viewer", DeviceID: "device-demo-tv", ProfileID: "profile-demo-default"},
			{ID: "session-demo-revealed", DeviceID: "device-demo-tv", ProfileID: "profile-demo-default", Revealed: true},
			{ID: "session-demo-managed", DeviceID: "device-demo-tv", ProfileID: "profile-demo-managed"},
			{ID: "session-demo-manager", DeviceID: "device-demo-manager", ProfileID: "profile-demo-default"},
		},
		media:      mediaRows,
		mediaFiles: demoMediaFiles(dataDir, mediaRows, generated),
		artwork:    demoArtwork(dataDir, generatedDemoMediaIDs()),
		// Ratings (CORE-2398) use the core-conventional fields a client shows:
		// films a certification and series a TV display rating, each with a
		// 0-10 community score, so clients can render ratings in demo mode.
		mediaFields: []seedMediaFields{
			{"media-demo-bunny", demoRated("G", 7.6, MediaField{Name: "format", Type: EntityFieldText, Text: "short"}, MediaField{Name: "tone", Type: EntityFieldText, Text: "Comedy"})},
			{"media-demo-caminandes", demoRated("G", 7.1, MediaField{Name: "format", Type: EntityFieldText, Text: "short"}, MediaField{Name: "tone", Type: EntityFieldText, Text: "Comedy"})},
			{"media-demo-sintel", demoRated("PG-13", 7.4, MediaField{Name: "format", Type: EntityFieldText, Text: "feature"}, MediaField{Name: "tone", Type: EntityFieldText, Text: "Adventure"})},
			{"media-demo-steel", demoRated("PG-13", 6.5, MediaField{Name: "format", Type: EntityFieldText, Text: "feature"}, MediaField{Name: "tone", Type: EntityFieldText, Text: "Science"})},
			{"media-demo-spring", demoRated("PG", 7.8)},
			{"media-demo-charge", demoRated("PG-13", 6.9)},
			{"media-demo-orbit", demoRated("G", 8.2)},
			{"media-demo-aurora", demoRated("G", 7.0)},
			{"media-demo-series-1", demoSeriesRated("TV-PG", 8.1, MediaField{Name: "format", Type: EntityFieldText, Text: "serial"})},
			{"media-demo-series-2", demoSeriesRated("TV-PG", 7.9, MediaField{Name: "format", Type: EntityFieldText, Text: "serial"})},
		},
		entities: []Entity{
			{ID: "entity-demo-comedy", LibraryID: "library-demo-open", Kind: "category", Title: "Comedy"},
			{ID: "entity-demo-science", LibraryID: "library-demo-open", Kind: "category", Title: "Science"},
			{ID: "entity-demo-sequence", LibraryID: "library-demo-open", Kind: "container", Title: "Open Sequence"},
			{ID: "entity-demo-space", LibraryID: "library-demo-archive", Kind: "category", Title: "Space"},
		},
		relations: []EntityRelation{
			{FromEntityID: "entity-demo-comedy", Name: "tagged", ToMediaID: "media-demo-bunny"},
			{FromEntityID: "entity-demo-comedy", Name: "tagged", ToMediaID: "media-demo-caminandes"},
			{FromEntityID: "entity-demo-science", Name: "tagged", ToMediaID: "media-demo-steel"},
			{FromEntityID: "entity-demo-space", Name: "tagged", ToMediaID: "media-demo-orbit"},
			{FromEntityID: "entity-demo-space", Name: "tagged", ToMediaID: "media-demo-aurora"},
			{FromEntityID: "entity-demo-sequence", Name: "contains", ToMediaID: "media-demo-series-1", SortOrder: intPtr(1)},
			{FromEntityID: "entity-demo-sequence", Name: "contains", ToMediaID: "media-demo-series-2", SortOrder: intPtr(2)},
		},
		markers: []Marker{
			{ID: "marker-demo-sintel-intro", MediaID: "media-demo-sintel", Title: "Opening", StartSeconds: 12, SegmentKind: "intro", Tags: []string{"arrival"}, CreatedAt: seedTime, UpdatedAt: seedTime},
			{ID: "marker-demo-sintel-turn", MediaID: "media-demo-sintel", Title: "Turning Point", StartSeconds: 360, Tags: []string{"story"}, CreatedAt: seedTime, UpdatedAt: seedTime},
			{ID: "marker-demo-bunny-credits", MediaID: "media-demo-bunny", StartSeconds: 550, SegmentKind: "credits", Tags: []string{"credits"}, CreatedAt: seedTime, UpdatedAt: seedTime},
		},
		progress: []PlaybackProgress{
			{ProfileID: "profile-demo-default", MediaID: "media-demo-personal-07", PositionSeconds: 48, DurationSeconds: 90, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(9 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-personal-01", PositionSeconds: 82, DurationSeconds: 180, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(8 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-personal-12", PositionSeconds: 420, DurationSeconds: 1800, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(7 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-restricted-05", PositionSeconds: 39, DurationSeconds: 90, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(6 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-sintel", PositionSeconds: 240, DurationSeconds: 888, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(5 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-personal-18", PositionSeconds: 55, DurationSeconds: 90, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(4 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-restricted-09", PositionSeconds: 44, DurationSeconds: 90, Watched: false, PlayCount: 0, UpdatedAt: progressTime.Add(3 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-series-1", PositionSeconds: 0, DurationSeconds: 600, Watched: true, PlayCount: 1, UpdatedAt: progressTime.Add(2 * time.Minute)},
			{ProfileID: "profile-demo-default", MediaID: "media-demo-bunny", PositionSeconds: 0, DurationSeconds: 596, Watched: true, PlayCount: 3, UpdatedAt: progressTime.Add(time.Minute)},
		},
	}
}

type generatedDemoMedia struct {
	id        MediaID
	libraryID LibraryID
	title     string
	summary   string
	duration  int
	year      int
	vertical  bool
}

// demoRated appends a film's certification and community score to fields.
func demoRated(certification string, score float64, fields ...MediaField) []MediaField {
	return append(fields,
		MediaField{Name: certificationField, Type: EntityFieldText, Text: certification},
		MediaField{Name: audienceScoreField, Type: EntityFieldNumber, Number: score})
}

// demoSeriesRated appends a series TV display rating and community score to
// fields; a series rating is display-only, never a ceiling value.
func demoSeriesRated(rating string, score float64, fields ...MediaField) []MediaField {
	return append(fields,
		MediaField{Name: certificationDisplayField, Type: EntityFieldText, Text: rating},
		MediaField{Name: audienceScoreField, Type: EntityFieldNumber, Number: score})
}

func generatedDemoMediaRows() []Media {
	specs := generatedDemoMediaSpecs()
	rows := make([]Media, 0, len(specs))
	for _, spec := range specs {
		width, height := 1920, 1080
		if spec.vertical {
			width, height = 1080, 1920
		}
		rows = append(rows, Media{
			ID:              spec.id,
			LibraryID:       spec.libraryID,
			Title:           spec.title,
			Summary:         spec.summary,
			Year:            spec.year,
			DurationSeconds: spec.duration,
			Container:       "mp4",
			Codec:           "h264",
			Class:           MediaClassVideo,
			VideoWidth:      width,
			VideoHeight:     height,
		})
	}
	return rows
}

func generatedDemoMediaIDs() []MediaID {
	specs := generatedDemoMediaSpecs()
	ids := make([]MediaID, 0, len(specs))
	for _, spec := range specs {
		ids = append(ids, spec.id)
	}
	return ids
}

func generatedDemoMediaSpecs() []generatedDemoMedia {
	restricted := []struct {
		title    string
		vertical bool
	}{
		{"Hotel Lounge", false},
		{"Blue Corridor", true},
		{"Neon Walk", false},
		{"Marquee Rain", true},
		{"Apartment Dance", true},
		{"Practice Routine", true},
		{"Balcony Dawn", false},
		{"Club Lights", false},
		{"Rooftop Routine", true},
		{"Kitchen Take", true},
		{"Midnight Booth", false},
		{"Lobby Crossing", false},
		{"Park Routine", true},
		{"Loft Dinner", false},
		{"Backstage Wait", true},
		{"Stage Rehearsal", false},
		{"Motel Balcony", true},
		{"Desert Stop", false},
		{"Garage Lights", true},
		{"Warehouse Rehearsal", false},
	}
	personal := []struct {
		title    string
		vertical bool
	}{
		{"Backyard Birthday", false},
		{"Birthday Cake", true},
		{"Lakeside Afternoon", false},
		{"Lakeside Picnic", true},
		{"Holiday Table", false},
		{"Kitchen Candles", false},
		{"Living Room Dance", true},
		{"Bike Trip", false},
		{"Lake Picnic", false},
		{"Backyard Party", true},
		{"Projector Night", false},
		{"Auditorium Concert", true},
		{"Moving Boxes", false},
		{"Holiday Dinner", true},
		{"Snow Day", false},
		{"Garden Harvest", true},
		{"Blanket Fort", false},
		{"Graduation Toast", true},
		{"Garage Practice", false},
		{"Nursery Setup", true},
		{"Potluck Prep", false},
		{"Anniversary Patio", true},
	}
	out := make([]generatedDemoMedia, 0, len(restricted)+len(personal))
	for i, item := range restricted {
		id := MediaID(fmt.Sprintf("media-demo-restricted-%02d", i+1))
		out = append(out, generatedDemoMedia{
			id:        id,
			libraryID: "library-demo-guarded",
			title:     item.title,
			summary:   "Fictional restricted demo still for Reveal and in-progress shelves.",
			duration:  demoGeneratedDuration(item.vertical),
			year:      2026,
			vertical:  item.vertical,
		})
	}
	for i, item := range personal {
		id := MediaID(fmt.Sprintf("media-demo-personal-%02d", i+1))
		out = append(out, generatedDemoMedia{
			id:        id,
			libraryID: "library-demo-open",
			title:     item.title,
			summary:   "Fictional personal-archive demo still for browse and in-progress shelves.",
			duration:  demoGeneratedDuration(item.vertical),
			year:      2026,
			vertical:  item.vertical,
		})
	}
	return out
}

func demoGeneratedDuration(vertical bool) int {
	if vertical {
		return 90
	}
	return 180
}

func demoArtwork(dataDir string, mediaIDs []MediaID) []seedArtwork {
	artworkDir := filepath.Join(dataDir, "artwork")
	out := make([]seedArtwork, 0, len(mediaIDs)*2)
	for _, mediaID := range mediaIDs {
		for _, slot := range []string{"poster", "backdrop"} {
			data, err := seedJPEGArtworkContent("seedart/" + string(mediaID) + "." + slot + ".png")
			if err != nil {
				panic(fmt.Sprintf("seed artwork %s/%s missing from embed: %v", mediaID, slot, err))
			}
			dst := filepath.Join(artworkDir, filepath.Base(string(mediaID)), slot+".jpg")
			out = append(out, seedArtwork{dst: dst, content: data})
		}
	}
	return out
}

func seedJPEGArtworkContent(path string) ([]byte, error) {
	data, err := seedArtworkFiles.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode seed artwork %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encode seed artwork %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

type demoMediaManifest struct {
	Version int                     `json:"version"`
	Files   []demoMediaManifestFile `json:"files"`
}

type demoMediaManifestFile struct {
	MediaID            MediaID `json:"mediaID"`
	Path               string  `json:"path"`
	ContentFingerprint string  `json:"contentFingerprint"`
	DurationSeconds    int     `json:"durationSeconds"`
	Container          string  `json:"container"`
	Codec              string  `json:"codec"`
	VideoWidth         int     `json:"videoWidth"`
	VideoHeight        int     `json:"videoHeight"`
}

func loadDemoMediaManifest(dataDir string) map[MediaID]demoMediaManifestFile {
	manifestPath := filepath.Join(dataDir, "seed-media", "demo", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		slog.Info("demo seed media manifest missing; using placeholder files", "path", manifestPath, "generator", "script/gen_demo_media.sh --data-dir "+dataDir)
		return nil
	}
	if err != nil {
		slog.Warn("demo seed media manifest unreadable; using placeholder files", "path", manifestPath, "error", err)
		return nil
	}
	var manifest demoMediaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		slog.Warn("demo seed media manifest invalid; using placeholder files", "path", manifestPath, "error", err)
		return nil
	}
	files := make(map[MediaID]demoMediaManifestFile, len(manifest.Files))
	for _, file := range manifest.Files {
		if file.MediaID == "" || file.Path == "" || file.Container == "" || file.Codec == "" || file.DurationSeconds <= 0 {
			slog.Warn("demo seed media manifest entry incomplete; skipping", "media_id", file.MediaID)
			continue
		}
		if !filepath.IsAbs(file.Path) {
			file.Path = filepath.Join(filepath.Dir(manifestPath), file.Path)
		}
		info, err := os.Stat(file.Path)
		if err != nil {
			slog.Warn("demo seed generated media missing; using placeholder for item", "media_id", file.MediaID, "path", file.Path, "error", err)
			continue
		}
		if info.Size() == 0 {
			slog.Warn("demo seed generated media is empty; using placeholder for item", "media_id", file.MediaID, "path", file.Path)
			continue
		}
		fingerprint, err := HashMediaFileContent(file.Path)
		if err != nil {
			slog.Warn("demo seed generated media cannot be fingerprinted; using placeholder for item", "media_id", file.MediaID, "path", file.Path, "error", err)
			continue
		}
		file.ContentFingerprint = fingerprint
		files[file.MediaID] = file
	}
	return files
}

func demoMediaFiles(dataDir string, media []Media, generated map[MediaID]demoMediaManifestFile) []MediaFile {
	files := make([]MediaFile, 0, len(media))
	for _, item := range media {
		if file, ok := generated[item.ID]; ok {
			files = append(files, MediaFile{
				MediaID:            item.ID,
				Path:               file.Path,
				ContentFingerprint: file.ContentFingerprint,
			})
			continue
		}
		files = append(files, MediaFile{
			MediaID:            item.ID,
			Path:               filepath.Join(dataDir, "seed-media", "demo", string(item.ID)+".mp4"),
			ContentFingerprint: "seed-demo-" + string(item.ID),
		})
	}
	return files
}

// seedData populates the catalog and events databases with a fixed test/demo
// dataset for the given profile.
//
// Cross-DB atomicity note (test-only): the catalog and events writes each
// commit inside their own transaction; there is no two-phase commit spanning
// both databases.  A failure between the catalog commit and the events commit
// leaves the catalog populated and events empty — a partially-seeded state.
// This is acceptable because seedData is called exclusively during server boot
// for development/conformance/acceptance/demo profiles; production stores are
// never seeded.  Any partially-seeded state is remedied by wiping and
// restarting the ephemeral test database, not by a compensating transaction.
func (s *Store) seedData(ctx context.Context, profile SeedProfile) error {
	var count int
	if err := s.catalogW.QueryRowContext(ctx, "SELECT COUNT(*) FROM profile").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var seed seedData
	switch profile {
	case SeedProfileDevelopment:
		seed = developmentSeed()
	case SeedProfileConformance:
		seed = conformanceSeed(s.dataDir)
	case SeedProfileAcceptance:
		seed = acceptanceSeed(s.dataDir)
	case SeedProfileDemo:
		seed = demoSeed(s.dataDir)
	default:
		return fmt.Errorf("unknown seed profile %q", profile)
	}
	if err := writeSeedFileContent(seed.seedFileContent); err != nil {
		return err
	}
	if err := ensureSeedMediaFiles(seed.mediaFiles); err != nil {
		return err
	}
	if err := writeSeedArtwork(seed.artwork); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := insertSeedCatalog(ctx, tx, seed); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	eventsTx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := insertSeedEvents(ctx, eventsTx, seed); err != nil {
		_ = eventsTx.Rollback()
		return err
	}
	return eventsTx.Commit()
}

func insertSeedCatalog(ctx context.Context, tx *sql.Tx, seed seedData) error {
	for _, library := range seed.libraries {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO library(id, title, plugin_id, is_restricted) VALUES (?, ?, ?, ?)",
			library.ID, library.Title, library.PluginID, boolInt(library.IsRestricted),
		); err != nil {
			return err
		}
	}
	for _, profile := range seed.profiles {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO profile(id, name, kind, reveal_pin_hash) VALUES (?, ?, ?, ?)",
			profile.ID, profile.Name, profile.Kind, seedRevealPINHash(profile.Kind),
		); err != nil {
			return err
		}
		for i, libraryID := range profile.Library {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO profile_library(profile_id, library_id, position) VALUES (?, ?, ?)",
				profile.ID, libraryID, i,
			); err != nil {
				return err
			}
		}
	}
	for _, device := range seed.devices {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO device(id, name, role, distribution_class, device_surface, secret_hash, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)",
			device.ID, device.Name, device.Role, DeviceDistributionDirect, DeviceSurfaceCoreWebUI, device.SecretHash, boolInt(device.Revoked),
		); err != nil {
			return err
		}
	}
	for _, session := range seed.sessions {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, kind) VALUES (?, ?, ?, ?, ?, ?, ?)",
			session.ID, session.DeviceID, session.ProfileID, boolInt(session.Revealed), nullableMillis(session.ExpiresAt), nullableMillis(session.AdminUntil), SessionKindEnrolled,
		); err != nil {
			return err
		}
	}
	for _, media := range seed.media {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, media_class, video_width, video_height)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			media.ID, media.LibraryID, media.Title, nullString(media.Summary), nullableInt(media.Year), media.DurationSeconds, media.Container, media.Codec, seedMediaClass(media.Class), media.VideoWidth, media.VideoHeight,
		); err != nil {
			return err
		}
	}
	for _, file := range seed.mediaFiles {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO media_file(id, content_fingerprint, media_id, path, last_seen_at_ms)
			 VALUES (?, ?, ?, ?, ?)`,
			file.ContentFingerprint, file.ContentFingerprint, file.MediaID, file.Path, timeToMillis(time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)),
		); err != nil {
			return err
		}
	}
	for _, inv := range seed.mediaFileStreams {
		if err := replaceMediaFileInventoryTx(ctx, tx, false, inv.fingerprint, inv.streams, nil); err != nil {
			return err
		}
	}
	for _, fields := range seed.mediaFields {
		if err := validateMediaFields(fields.fields); err != nil {
			return err
		}
		if err := insertMediaFields(ctx, tx, fields.mediaID, fields.fields); err != nil {
			return err
		}
	}
	for _, entity := range seed.entities {
		if err := insertSeedEntity(ctx, tx, entity); err != nil {
			return err
		}
	}
	for _, relation := range seed.relations {
		if err := insertSeedRelation(ctx, tx, relation); err != nil {
			return err
		}
	}
	for _, marker := range seed.markers {
		if err := insertSeedMarker(ctx, tx, marker); err != nil {
			return err
		}
	}
	if seed.adminPassword != "" {
		// Pinned seed clock (matches the media_file last_seen_at_ms above) so the
		// admin-credential row is reproducible across re-seeds.
		seedClock := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
		if err := seedAdminCredentialTx(ctx, tx, seed.adminPassword, seed.adminSalt, seedClock); err != nil {
			return err
		}
	}
	return nil
}

func insertSeedEvents(ctx context.Context, tx *sql.Tx, seed seedData) error {
	for _, progress := range seed.progress {
		updatedAtMS := timeToMillis(progress.UpdatedAt)
		event := playbackProjectionEvent{
			Operation:       playbackProjectionOperationProgress,
			ProfileID:       string(progress.ProfileID),
			MediaID:         string(progress.MediaID),
			PositionSeconds: progress.PositionSeconds,
			DurationSeconds: progress.DurationSeconds,
			Watched:         progress.Watched,
			UpdatedAtMS:     updatedAtMS,
			PlayCount:       progress.PlayCount,
			ExplicitCount:   progress.PlayCount,
			LastPlayedAtMS:  &updatedAtMS,
			EventTimeMS:     optionalMillis(progress.EventTimeMS > 0, progress.EventTimeMS),
			ChannelContext:  progress.ChannelContext,
		}
		if err := appendPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return err
		}
		if err := applyPlaybackProjectionEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

// writeSeedFileContent writes deterministic byte payloads for seeded Media
// Files (conformance profile) so the on-disk bytes match the fingerprint the
// seed records. It always rewrites, so a re-seed onto a stale file self-heals.
func writeSeedFileContent(files []seedFile) error {
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(file.path, file.content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// seedArtworkModTime is the pinned modification time stamped on every seeded
// artwork file. The artwork version token (the ?v=<token> in artwork URLs) is
// derived from a CONTENT hash of the file bytes (see internal/artwork:
// contentVersion), not the mtime, so the token is already byte-stable for
// identical bytes. The mtime is pinned to Unix 0 anyway for cache-stability —
// so a re-seed of identical bytes does not perturb file mtimes that other
// stat-based paths (the scan cheap-triple) compare — matching the determinism
// the read-path contract gets from the conformance fixed clock.
var seedArtworkModTime = time.Unix(0, 0).UTC()

// writeSeedArtwork lands each placeholder artwork file and pins its mtime so
// the artwork version token is deterministic. Files are overwritten on a fresh
// seed; the empty-data guard keeps a corrupt embed from silently landing a
// zero-byte image.
func writeSeedArtwork(files []seedArtwork) error {
	for _, file := range files {
		if len(file.content) == 0 {
			return fmt.Errorf("seed artwork %s has no bytes", file.dst)
		}
		if err := os.MkdirAll(filepath.Dir(file.dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(file.dst, file.content, 0o600); err != nil {
			return err
		}
		if err := os.Chtimes(file.dst, seedArtworkModTime, seedArtworkModTime); err != nil {
			return err
		}
	}
	return nil
}

func ensureSeedMediaFiles(files []MediaFile) error {
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o700); err != nil {
			return err
		}
		if _, err := os.Stat(file.Path); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(file.Path, nil, 0o600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func insertSeedEntity(ctx context.Context, tx *sql.Tx, entity Entity) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO entity(id, library_id, kind, title) VALUES (?, ?, ?, ?)`,
		entity.ID, entity.LibraryID, entity.Kind, entity.Title,
	); err != nil {
		return err
	}
	for _, field := range entity.Fields {
		var table string
		var value any
		switch field.Type {
		case EntityFieldText, EntityFieldURL:
			table, value = "entity_field_text", field.Text
		case EntityFieldNumber:
			table, value = "entity_field_real", field.Number
		case EntityFieldFlag:
			table, value = "entity_field_int", boolInt(field.Flag)
		case EntityFieldDate:
			table, value = "entity_field_date", timeToMillis(field.Date.UTC())
		default:
			return errors.New("unknown entity field type " + string(field.Type))
		}
		column := "value"
		if table == "entity_field_date" {
			column = "value_ms"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+table+`(entity_id, kind, field, field_type, `+column+`) VALUES (?, ?, ?, ?, ?)
		`, entity.ID, entity.Kind, field.Name, field.Type, value); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO entity_fts(entity_id, title, fields) VALUES (?, ?, '')`, entity.ID, entity.Title)
	return err
}

func insertSeedRelation(ctx context.Context, tx *sql.Tx, relation EntityRelation) error {
	var attributes any
	if len(relation.Attributes) > 0 {
		data, err := json.Marshal(relation.Attributes)
		if err != nil {
			return err
		}
		attributes = string(data)
	}
	var sortOrder any
	if relation.SortOrder != nil {
		sortOrder = *relation.SortOrder
	}
	if relation.ToEntityID != "" {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order) VALUES (?, ?, ?, ?, ?)`,
			relation.FromEntityID, relation.Name, relation.ToEntityID, attributes, sortOrder,
		)
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO entity_relation(from_entity_id, relation, to_media_id, attributes, sort_order) VALUES (?, ?, ?, ?, ?)`,
		relation.FromEntityID, relation.Name, relation.ToMediaID, attributes, sortOrder,
	)
	return err
}

func insertSeedMarker(ctx context.Context, tx *sql.Tx, marker Marker) error {
	if marker.CreatedAt.IsZero() {
		marker.CreatedAt = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	}
	if marker.UpdatedAt.IsZero() {
		marker.UpdatedAt = marker.CreatedAt
	}
	if err := validateMarker(&marker); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO marker(id, media_id, title, start_seconds, end_seconds, segment_kind, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, marker.ID, marker.MediaID, marker.Title, marker.StartSeconds,
		nullFloat(marker.EndSeconds), nullString(marker.SegmentKind),
		marker.CreatedAt.UnixMilli(), marker.UpdatedAt.UnixMilli()); err != nil {
		return err
	}
	return insertMarkerTags(ctx, tx, marker.ID, marker.Tags)
}

func intPtr(value int) *int {
	return &value
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func seedMediaClass(class MediaClass) MediaClass {
	if class == "" {
		return MediaClassVideo
	}
	return class
}

// libraryColumns is the Library projection scanLibrary reads; it takes
// libraryIconConfigPrefix as its one bind parameter.
const libraryColumns = `id, title, plugin_id, is_restricted, preview_clips_enabled, ai_indexing_enabled, family_scope, custodial, encrypted, show_on_home,
			COALESCE((SELECT value FROM server_config WHERE key = ? || library.id), '')`

// scanLibrary reads one libraryColumns row, followed by any extra columns the
// caller selected after them.
func scanLibrary(rows *sql.Rows, extra ...any) (Library, error) {
	var library Library
	var plugin sql.NullString
	var restricted, previewClips, aiIndexing, custodial, encrypted, showOnHome int
	dest := append([]any{&library.ID, &library.Title, &plugin, &restricted, &previewClips, &aiIndexing, &library.FamilyScope, &custodial, &encrypted, &showOnHome, &library.Icon}, extra...)
	if err := rows.Scan(dest...); err != nil {
		return Library{}, err
	}
	if plugin.Valid {
		library.PluginID = &plugin.String
	}
	library.IsRestricted = restricted != 0
	library.PreviewClips = previewClips != 0
	library.AIIndexing = aiIndexing != 0
	library.Custodial = custodial != 0
	library.Encrypted = encrypted != 0
	library.ShowOnHome = showOnHome != 0
	return library, nil
}

type rowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type sqlReader interface {
	queryRower
	rowsQueryer
}

func loadLibraries(ctx context.Context, db rowsQueryer) ([]Library, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+libraryColumns+`
		FROM library WHERE trashed_at_ms IS NULL ORDER BY rowid`, libraryIconConfigPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var libraries []Library
	for rows.Next() {
		library, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		libraries = append(libraries, library)
	}
	return libraries, rows.Err()
}

func (s *Store) libraryByIDLocked(ctx context.Context, id LibraryID) (Library, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return Library{}, err
	}
	for _, library := range libraries {
		if library.ID == id {
			return library, nil
		}
	}
	return Library{}, errors.New("library not found")
}

func (s *Store) deviceSQL(ctx context.Context, id DeviceID) (Device, error) {
	return readDevice(ctx, s.catalogR, id)
}

func readDevice(ctx context.Context, q queryRower, id DeviceID) (Device, error) {
	var device Device
	var revoked int
	var playerProfileID string
	err := q.QueryRowContext(ctx,
		"SELECT id, name, role, distribution_class, device_surface, secret_hash, revoked, COALESCE(player_profile_id, ''), player_display_name, player_binding_generation FROM device WHERE id = ?", id,
	).Scan(&device.ID, &device.Name, &device.Role, &device.DistributionClass, &device.Surface, &device.SecretHash, &revoked, &playerProfileID, &device.PlayerDisplayName, &device.PlayerBindingGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, errors.New("device not found")
	}
	if err != nil {
		return Device{}, err
	}
	device.PlayerProfileID = ProfileID(playerProfileID)
	device.Revoked = revoked != 0
	return device, nil
}

func (s *Store) sessionSQL(ctx context.Context, id SessionID) (Session, error) {
	return readSession(ctx, s.catalogR, id)
}

func readSession(ctx context.Context, q queryRower, id SessionID) (Session, error) {
	var session Session
	var revealed int
	var deviceID, kind sql.NullString
	var expires sql.NullInt64
	var adminUntil sql.NullInt64
	var integrationScopes string
	err := q.QueryRowContext(ctx,
		"SELECT id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, kind, integration_scopes FROM auth_session WHERE id = ?", id,
	).Scan(&session.ID, &deviceID, &session.ProfileID, &revealed, &expires, &adminUntil, &kind, &integrationScopes)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if deviceID.Valid {
		session.DeviceID = DeviceID(deviceID.String)
	}
	switch SessionKind(kind.String) {
	case SessionKindEnrolled:
		if session.DeviceID == "" {
			return Session{}, errors.New("invalid enrolled session")
		}
		session.Kind = SessionKindEnrolled
	case SessionKindIntegration:
		if session.DeviceID != "" {
			return Session{}, errors.New("invalid integration session")
		}
		session.Kind = SessionKindIntegration
		session.IntegrationScopes, err = decodeIntegrationScopes(integrationScopes)
		if err != nil {
			return Session{}, err
		}
	case "":
		if session.DeviceID == "" {
			return Session{}, errors.New("invalid legacy session")
		}
		session.Kind = SessionKindEnrolled
	default:
		return Session{}, errors.New("unknown session kind")
	}
	session.Revealed = revealed != 0
	if expires.Valid {
		session.ExpiresAt = millisToTime(expires.Int64)
	}
	if adminUntil.Valid {
		session.AdminUntil = millisToTime(adminUntil.Int64)
	}
	return session, nil
}

func (s *Store) profileSQL(ctx context.Context, id ProfileID) (Profile, error) {
	return readProfile(ctx, s.catalogR, id)
}

func readProfile(ctx context.Context, q sqlReader, id ProfileID) (Profile, error) {
	var profile Profile
	var maxStreams sql.NullInt64
	var transcodeAllowed, disabled, preferSDH, isOwner, integrationSignIn, walkUpEnabled int
	var audioLanguages, subtitleLanguages string
	err := q.QueryRowContext(ctx, `
		SELECT id, name, kind, max_concurrent_streams, transcode_allowed,
		       disabled, audio_languages, subtitle_languages, subtitle_mode, prefer_sdh,
		       created_at_ms, is_owner, integration_sign_in, remote_max_bitrate, walk_up_enabled
		FROM profile WHERE id = ?`, id,
	).Scan(&profile.ID, &profile.Name, &profile.Kind, &maxStreams, &transcodeAllowed,
		&disabled, &audioLanguages, &subtitleLanguages, &profile.SubtitleMode, &preferSDH,
		&profile.CreatedAtMs, &isOwner, &integrationSignIn, &profile.RemoteMaxBitrate, &walkUpEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ValidationError("profile not found")
	}
	if err != nil {
		return Profile{}, err
	}
	if maxStreams.Valid {
		limit := int(maxStreams.Int64)
		profile.MaxConcurrentStreams = &limit
	}
	profile.TranscodeAllowed = transcodeAllowed != 0
	profile.Disabled = disabled != 0
	profile.PreferSDH = preferSDH != 0
	profile.IsOwner = isOwner != 0
	profile.IntegrationSignIn = integrationSignIn != 0
	profile.WalkUpEnabled = walkUpEnabled != 0
	if profile.AudioLanguages, err = decodeLanguages(audioLanguages); err != nil {
		return Profile{}, err
	}
	if profile.SubtitleLanguages, err = decodeLanguages(subtitleLanguages); err != nil {
		return Profile{}, err
	}
	rows, err := q.QueryContext(ctx,
		"SELECT library_id FROM profile_library WHERE profile_id = ? ORDER BY position", id,
	)
	if err != nil {
		return Profile{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var libraryID LibraryID
		if err := rows.Scan(&libraryID); err != nil {
			return Profile{}, err
		}
		profile.Library = append(profile.Library, libraryID)
	}
	if err := rows.Err(); err != nil {
		return Profile{}, err
	}
	rows.Close()
	if profile.CertificationCeilings, err = readCertificationCeilings(ctx, q, id); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

// profileDisabled reports the Profile's disabled flag; unknown Profiles error.
// q is a queryRower so the check can run inside a redemption transaction.
func profileDisabled(ctx context.Context, q queryRower, id ProfileID) (bool, error) {
	var disabled int
	err := q.QueryRowContext(ctx, "SELECT disabled FROM profile WHERE id = ?", id).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("profile not found")
	}
	if err != nil {
		return false, err
	}
	return disabled != 0, nil
}

// updateProfileSettingsSQL writes the columns settings supplies, taking each
// value from the already-merged Profile. Columns settings leaves nil are not
// written, so a write that read the row before a concurrent edit (an Admin
// change of kind, disabled or stream policy racing a Profile's own track
// preference write, CORE-1937) cannot put the stale values back. Setting the
// integration sign-in opt-in false revokes the integration sessions and
// integration keys, so opting back in does not revive either.
func (s *Store) updateProfileSettingsSQL(ctx context.Context, profile Profile, settings ProfileSettings) error {
	var sets []string
	var args []any
	set := func(column string, value any) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if settings.Name != nil {
		set("name", profile.Name)
	}
	if settings.Kind != nil {
		set("kind", profile.Kind)
	}
	if settings.MaxConcurrentStreams != nil {
		var maxStreams any
		if profile.MaxConcurrentStreams != nil {
			maxStreams = *profile.MaxConcurrentStreams
		}
		set("max_concurrent_streams", maxStreams)
	}
	if settings.TranscodeAllowed != nil {
		set("transcode_allowed", boolInt(profile.TranscodeAllowed))
	}
	if settings.Disabled != nil {
		set("disabled", boolInt(profile.Disabled))
	}
	if settings.AudioLanguages != nil {
		audioLanguages, err := encodeLanguages(profile.AudioLanguages)
		if err != nil {
			return err
		}
		set("audio_languages", audioLanguages)
	}
	if settings.SubtitleLanguages != nil {
		subtitleLanguages, err := encodeLanguages(profile.SubtitleLanguages)
		if err != nil {
			return err
		}
		set("subtitle_languages", subtitleLanguages)
	}
	if settings.SubtitleMode != nil {
		set("subtitle_mode", profile.SubtitleMode)
	}
	if settings.PreferSDH != nil {
		set("prefer_sdh", boolInt(profile.PreferSDH))
	}
	if settings.IntegrationSignIn != nil {
		set("integration_sign_in", boolInt(*settings.IntegrationSignIn))
	}
	if settings.RemoteMaxBitrate != nil {
		set("remote_max_bitrate", profile.RemoteMaxBitrate)
	}
	if len(sets) == 0 {
		return nil
	}
	// One transaction: an opt-out whose session or key revocation failed
	// must not leave the Profile opted out with live keys a later opt-in
	// would revive.
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// #nosec G202 -- every column name above is a fixed literal; values are bound.
	_, err = tx.ExecContext(ctx, "UPDATE profile SET "+strings.Join(sets, ", ")+" WHERE id = ?", append(args, profile.ID)...)
	if err != nil {
		return err
	}
	if profile.Kind == ProfileManaged {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM profile_library
			WHERE profile_id = ? AND library_id IN (SELECT id FROM library WHERE is_restricted = 1)`,
			profile.ID)
	}
	if err == nil && settings.IntegrationSignIn != nil && !*settings.IntegrationSignIn {
		_, err = tx.ExecContext(ctx, `DELETE FROM auth_session WHERE profile_id = ? AND kind = ?`, profile.ID, SessionKindIntegration)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE compat_integration_key SET revoked_at_ms = ? WHERE profile_id = ? AND revoked_at_ms = 0`, timeToMillis(time.Now()), profile.ID)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// encodeLanguages stores an ordered language list as a JSON array ("[]" when empty).
func encodeLanguages(list []string) (string, error) {
	if len(list) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// decodeLanguages parses the stored JSON array; empty arrays read back as nil.
func decodeLanguages(text string) ([]string, error) {
	if text == "" || text == "[]" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

func (s *Store) profileExistsSQL(ctx context.Context, id ProfileID) (bool, error) {
	var one int
	err := s.catalogR.QueryRowContext(ctx, "SELECT 1 FROM profile WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) mediaExistsSQL(ctx context.Context, id MediaID) (bool, error) {
	var one int
	err := s.catalogR.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) visibleMediaSQL(ctx context.Context, id MediaID, filter LibraryFilter) (Media, error) {
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return Media{}, err
	}
	return s.visibleMediaSQLFrom(ctx, plane.reader, id, filter)
}

func (s *Store) encryptedLibraryForReaderLocked(ctx context.Context, reader *sql.DB) (Library, bool, error) {
	if reader == s.catalogR {
		return Library{}, false, nil
	}
	for libraryID, encrypted := range s.encryptedDBs {
		if encrypted == nil || encrypted.reader != reader {
			continue
		}
		library, err := s.libraryByIDLocked(ctx, libraryID)
		return library, true, err
	}
	return Library{}, false, errors.New("catalog plane reader is not registered")
}

func (s *Store) visibleMediaSQLFrom(ctx context.Context, db *sql.DB, id MediaID, filter LibraryFilter) (Media, error) {
	var media Media
	var summary sql.NullString
	var year sql.NullInt64
	var mutatedBy sql.NullString
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	library, encrypted, err := s.encryptedLibraryForReaderLocked(ctx, db)
	if err != nil {
		return Media{}, err
	}
	if encrypted {
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		certClause, certArgs = planeCertificationFilterSQL(pluginID, filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	}
	args := append([]any{id}, certArgs...)
	err = db.QueryRowContext(ctx, `
		SELECT m.id, m.library_id, m.title, m.summary, m.year, m.duration_seconds, m.container, m.codec, m.video_width, m.video_height, m.media_class, m.mutated_by
		FROM media m
		WHERE m.id = ? AND m.trashed_at_ms IS NULL`+certClause+`
	`, args...).Scan(&media.ID, &media.LibraryID, &media.Title, &summary, &year, &media.DurationSeconds, &media.Container, &media.Codec, &media.VideoWidth, &media.VideoHeight, &media.Class, &mutatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ValidationError("media not found")
	}
	if err != nil {
		return Media{}, err
	}
	media.Summary = summary.String
	media.Year = int(year.Int64)
	media.MutatedBy = mutationSourceFromDB(mutatedBy.String)
	if library.ID == "" {
		library, err = s.libraryByIDLocked(ctx, media.LibraryID)
		if err != nil {
			return Media{}, err
		}
	} else if media.LibraryID != library.ID {
		return Media{}, ValidationError("media not found")
	}
	if !libraryVisible(library, filter) {
		return Media{}, ValidationError("media not found")
	}
	return media, nil
}

type mediaReaders struct {
	catalog   rowsQueryer
	encrypted func(LibraryID) rowsQueryer
}

func (s *Store) encryptedMediaReaderLocked(id LibraryID) rowsQueryer {
	if db := s.encryptedDBs[id]; db != nil && db.state == "unsealed-registered" && db.reader != nil {
		return db.reader
	}
	return nil
}

func (s *Store) mediaReadersLocked() mediaReaders {
	return mediaReaders{catalog: s.catalogR, encrypted: s.encryptedMediaReaderLocked}
}

func (s *Store) visibleMediaBatchSQL(ctx context.Context, ids []MediaID, filter LibraryFilter) (map[MediaID]Media, error) {
	return visibleMediaBatch(ctx, s.mediaReadersLocked(), ids, filter)
}

func visibleMediaBatch(ctx context.Context, readers mediaReaders, ids []MediaID, filter LibraryFilter) (map[MediaID]Media, error) {
	visible := make(map[MediaID]Media, len(ids))
	err := eachVisibleMedia(ctx, readers, ids, filter, visibleMediaColumns, scanVisibleMedia, func(media Media) {
		visible[media.ID] = media
	})
	if err != nil {
		return nil, err
	}
	return visible, nil
}

// visibleMediaIDsSQL answers which of ids visibleMediaBatchSQL would return,
// reading only each Media's id and Library.
func (s *Store) visibleMediaIDsSQL(ctx context.Context, ids []MediaID, filter LibraryFilter) (map[MediaID]struct{}, error) {
	visible := make(map[MediaID]struct{}, len(ids))
	err := eachVisibleMedia(ctx, s.mediaReadersLocked(), ids, filter, "m.id, m.library_id", func(rows *sql.Rows) (Media, error) {
		var media Media
		err := rows.Scan(&media.ID, &media.LibraryID)
		return media, err
	}, func(media Media) {
		visible[media.ID] = struct{}{}
	})
	if err != nil {
		return nil, err
	}
	return visible, nil
}

const visibleMediaColumns = `m.id, m.library_id, m.title, m.summary, m.year, m.duration_seconds, m.container, m.codec, m.video_width, m.video_height, m.media_class, m.mutated_by`

func scanVisibleMedia(rows *sql.Rows) (Media, error) {
	var media Media
	var summary sql.NullString
	var year sql.NullInt64
	var mutatedBy sql.NullString
	if err := rows.Scan(&media.ID, &media.LibraryID, &media.Title, &summary, &year, &media.DurationSeconds, &media.Container, &media.Codec, &media.VideoWidth, &media.VideoHeight, &media.Class, &mutatedBy); err != nil {
		return Media{}, err
	}
	media.Summary = summary.String
	media.Year = int(year.Int64)
	media.MutatedBy = mutationSourceFromDB(mutatedBy.String)
	return media, nil
}

// eachVisibleMedia reads the untrashed Media among ids that the filter's
// Libraries and certification rules admit, from the catalog and every open
// Encrypted Library readers can open, selecting columns and handing each row scanned by scan to
// emit.
func eachVisibleMedia(ctx context.Context, readers mediaReaders, ids []MediaID, filter LibraryFilter, columns string, scan func(*sql.Rows) (Media, error), emit func(Media)) error {
	if len(ids) == 0 {
		return nil
	}
	libraries, err := loadLibraries(ctx, readers.catalog)
	if err != nil {
		return err
	}
	visibleLibraries := map[LibraryID]bool{}
	libraryPlugins := map[LibraryID]string{}
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visibleLibraries[library.ID] = true
			if library.PluginID != nil {
				libraryPlugins[library.ID] = *library.PluginID
			}
		}
	}
	read := func(db rowsQueryer, query string, args []any, admit func(Media) bool) error {
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			media, err := scan(rows)
			if err != nil {
				return err
			}
			if admit(media) {
				emit(media)
			}
		}
		return rows.Err()
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		placeholders := sqlPlaceholders(len(chunk))
		args := make([]any, 0, len(chunk)+len(certArgs))
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, certArgs...)
		// #nosec G202 -- columns is a fixed column list; only generated placeholders and closed certification SQL are interpolated.
		err := read(readers.catalog, `
			SELECT `+columns+`
			FROM media m
			WHERE m.id IN (`+placeholders+`)
			  AND m.trashed_at_ms IS NULL`+certClause+`
		`, args, func(media Media) bool { return visibleLibraries[media.LibraryID] })
		if err != nil {
			return err
		}

		for _, library := range libraries {
			libraryID := library.ID
			if !library.Encrypted || !visibleLibraries[libraryID] {
				continue
			}
			reader := readers.encrypted(libraryID)
			if reader == nil {
				continue
			}
			planeCertClause, planeCertArgs := planeCertificationFilterSQL(libraryPlugins[libraryID], filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
			args := make([]any, 0, len(chunk)+1+len(planeCertArgs))
			for _, id := range chunk {
				args = append(args, id)
			}
			args = append(args, libraryID)
			args = append(args, planeCertArgs...)
			// #nosec G202 -- columns is a fixed column list; only generated placeholders and closed certification SQL are interpolated.
			err := read(reader, `
				SELECT `+columns+`
				FROM media m
				WHERE m.id IN (`+placeholders+`)
				  AND m.library_id = ?
				  AND m.trashed_at_ms IS NULL`+planeCertClause+`
			`, args, func(Media) bool { return true })
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) primaryMediaFileSQL(ctx context.Context, id MediaID) (MediaFile, error) {
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return MediaFile{}, err
	}
	return primaryMediaFileSQL(ctx, plane.reader, id)
}

func primaryMediaFileSQL(ctx context.Context, db *sql.DB, id MediaID) (MediaFile, error) {
	var file MediaFile
	var lastSeen int64
	// A Media may now have many files (Versions x Parts, ADR-0009 §2); the
	// primary is the first in (version_label, part_ordinal) order — the default
	// Version's first Part — so the choice is deterministic, not whichever row
	// SQLite returns first.
	err := db.QueryRowContext(ctx, `
		SELECT media_id, path, content_fingerprint, version_label, part_ordinal, oshash, phash, last_seen_at_ms
		FROM media_file
		WHERE media_id = ? AND missing_at_ms IS NULL
		ORDER BY version_label, part_ordinal
		LIMIT 1
	`, id).Scan(&file.MediaID, &file.Path, &file.ContentFingerprint, &file.VersionLabel, &file.PartOrdinal, &file.OSHash, &file.PHash, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaFile{}, errors.New("media file not found")
	}
	if err != nil {
		return MediaFile{}, err
	}
	file.LastSeenAt = millisToTime(lastSeen)
	return file, nil
}

// mediaFilesForMediaSQL reads every available file for a Media, ordered into
// Versions (by label) and Parts (by ordinal) — see MediaFilesForMedia.
func (s *Store) mediaFilesForMediaSQL(ctx context.Context, id MediaID) ([]MediaFile, error) {
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := plane.reader.QueryContext(ctx, `
		SELECT media_id, path, content_fingerprint, version_label, part_ordinal, oshash, phash, size_bytes, last_seen_at_ms
		FROM media_file
		WHERE media_id = ? AND missing_at_ms IS NULL
		ORDER BY version_label, part_ordinal
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []MediaFile
	for rows.Next() {
		var file MediaFile
		var lastSeen int64
		if err := rows.Scan(&file.MediaID, &file.Path, &file.ContentFingerprint, &file.VersionLabel, &file.PartOrdinal, &file.OSHash, &file.PHash, &file.SizeBytes, &lastSeen); err != nil {
			return nil, err
		}
		file.LastSeenAt = millisToTime(lastSeen)
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("media file not found")
	}
	return files, nil
}

func (s *Store) mediaFilesForLibrarySQL(ctx context.Context, db *sql.DB, libraryID LibraryID) ([]MediaFile, error) {
	var files []MediaFile
	err := s.forEachMediaFileForLibrarySQL(ctx, db, libraryID, func(file MediaFile) error {
		files = append(files, file)
		return nil
	})
	return files, err
}

func (s *Store) forEachMediaFileForLibrarySQL(ctx context.Context, db *sql.DB, libraryID LibraryID, yield func(MediaFile) error) error {
	rows, err := db.QueryContext(ctx, `
		SELECT f.media_id, f.path, f.content_fingerprint, f.version_label, f.part_ordinal, f.oshash, f.phash, f.last_seen_at_ms, f.size_bytes, f.mtime_ms
		FROM media_file f
		JOIN media m ON m.id = f.media_id
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL AND f.missing_at_ms IS NULL
		ORDER BY f.path COLLATE NOCASE, f.media_id
	`, libraryID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var file MediaFile
		var lastSeen, mtimeMS int64
		if err := rows.Scan(&file.MediaID, &file.Path, &file.ContentFingerprint, &file.VersionLabel, &file.PartOrdinal, &file.OSHash, &file.PHash, &lastSeen, &file.SizeBytes, &mtimeMS); err != nil {
			return err
		}
		file.LastSeenAt = millisToTime(lastSeen)
		file.ModTime = mediaFileMillisToTime(mtimeMS)
		if err := yield(file); err != nil {
			return err
		}
	}
	return rows.Err()
}

// mediaFingerprintsSQL joins the Media row's probed duration with its primary
// file's fingerprint hashes. The LEFT JOIN keeps a Media item whose file went
// missing resolvable — the duration may still drive a {duration} lookup.
func (s *Store) mediaFingerprintsSQL(ctx context.Context, id MediaID) (MediaFingerprints, error) {
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return MediaFingerprints{}, err
	}
	var fp MediaFingerprints
	err = plane.reader.QueryRowContext(ctx, `
		SELECT m.duration_seconds, COALESCE(f.oshash, ''), COALESCE(f.phash, '')
		FROM media m
		LEFT JOIN media_file f ON f.media_id = m.id AND f.missing_at_ms IS NULL
		WHERE m.id = ?
	`, id).Scan(&fp.DurationSeconds, &fp.OSHash, &fp.PHash)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaFingerprints{}, errors.New("media not found")
	}
	if err != nil {
		return MediaFingerprints{}, err
	}
	return fp, nil
}

// mediaFieldFilterSQL renders the typed clauses for plugin-declared Media
// field filters. Iteration is name-sorted so the SQL shape is deterministic.
// Empty filters yield no clause.
func mediaFieldFilterSQL(filters map[string]any) (string, []any, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}
	names := make([]string, 0, len(filters))
	for name := range filters {
		names = append(names, name)
	}
	sort.Strings(names)
	var clause strings.Builder
	args := make([]any, 0, len(filters)*4)
	for _, name := range names {
		fieldClause, fieldArgs, err := mediaFieldPredicateSQL(name, filters[name])
		if err != nil {
			return "", nil, err
		}
		clause.WriteString(`
		  AND (`)
		clause.WriteString(fieldClause)
		clause.WriteString(`)`)
		args = append(args, fieldArgs...)
	}
	return clause.String(), args, nil
}

func mediaFieldPredicateSQL(field string, value any) (string, []any, error) {
	switch typed := value.(type) {
	case string:
		return mediaFieldStringPredicateSQL(field, typed)
	case bool:
		return `
		  EXISTS (SELECT 1 FROM media_field_int mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value = ?)`, []any{field, boolToInt(typed)}, nil
	case time.Time:
		return mediaFieldDatePredicateSQL(field, typed.UTC().UnixMilli())
	case map[string]any:
		return mediaFieldRangePredicateSQL(field, typed)
	default:
		if number, ok := filterNumberValue(value); ok {
			return `
		  EXISTS (SELECT 1 FROM media_field_real mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value = ?)`, []any{field, number}, nil
		}
		return "", nil, fmt.Errorf("field filter %q has unsupported value type %T", field, value)
	}
}

func mediaFieldStringPredicateSQL(field string, value string) (string, []any, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil, fmt.Errorf("field filter %q requires a value", field)
	}
	args := []any{field, value}
	clause := `
		  EXISTS (SELECT 1 FROM media_field_text mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value = ?)`
	if when, ok := parseFilterTime(value); ok {
		clause = `
		  (EXISTS (SELECT 1 FROM media_field_text mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value = ?)
		   OR EXISTS (SELECT 1 FROM media_field_date mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value_ms = ?))`
		args = []any{field, value, field, when.UTC().UnixMilli()}
	}
	return clause, args, nil
}

func mediaFieldDatePredicateSQL(field string, millis int64) (string, []any, error) {
	return `
		  EXISTS (SELECT 1 FROM media_field_date mf WHERE mf.media_id = m.id AND mf.field = ? AND mf.value_ms = ?)`, []any{field, millis}, nil
}

func mediaFieldRangePredicateSQL(field string, raw map[string]any) (string, []any, error) {
	gte, hasGte := raw["gte"]
	lte, hasLte := raw["lte"]
	for key := range raw {
		if key != "gte" && key != "lte" {
			return "", nil, fmt.Errorf("field filter %q range key %q is not supported", field, key)
		}
	}
	if !hasGte && !hasLte {
		return "", nil, fmt.Errorf("field filter %q range requires gte or lte", field)
	}
	if usesDateRange(gte, lte) {
		return mediaFieldDateRangePredicateSQL(field, gte, lte)
	}
	return mediaFieldNumberRangePredicateSQL(field, gte, lte)
}

func mediaFieldNumberRangePredicateSQL(field string, gte, lte any) (string, []any, error) {
	clauses := []string{}
	args := []any{}
	if gte != nil {
		number, ok := filterNumberValue(gte)
		if !ok {
			return "", nil, fmt.Errorf("field filter %q range gte has unsupported type %T", field, gte)
		}
		clauses = append(clauses, `mf.value >= ?`)
		args = append(args, number)
	}
	if lte != nil {
		number, ok := filterNumberValue(lte)
		if !ok {
			return "", nil, fmt.Errorf("field filter %q range lte has unsupported type %T", field, lte)
		}
		clauses = append(clauses, `mf.value <= ?`)
		args = append(args, number)
	}
	return `
		  EXISTS (SELECT 1 FROM media_field_real mf WHERE mf.media_id = m.id AND mf.field = ? AND ` + strings.Join(clauses, " AND ") + `)`, append([]any{field}, args...), nil
}

func mediaFieldDateRangePredicateSQL(field string, gte, lte any) (string, []any, error) {
	clauses := []string{}
	args := []any{}
	if gte != nil {
		when, ok := parseFilterTime(gte)
		if !ok {
			return "", nil, fmt.Errorf("field filter %q range gte has unsupported type %T", field, gte)
		}
		clauses = append(clauses, `mf.value_ms >= ?`)
		args = append(args, when.UTC().UnixMilli())
	}
	if lte != nil {
		when, ok := parseFilterTime(lte)
		if !ok {
			return "", nil, fmt.Errorf("field filter %q range lte has unsupported type %T", field, lte)
		}
		clauses = append(clauses, `mf.value_ms <= ?`)
		args = append(args, when.UTC().UnixMilli())
	}
	return `
		  EXISTS (SELECT 1 FROM media_field_date mf WHERE mf.media_id = m.id AND mf.field = ? AND ` + strings.Join(clauses, " AND ") + `)`, append([]any{field}, args...), nil
}

func usesDateRange(gte, lte any) bool {
	return isDateFilterValue(gte) || isDateFilterValue(lte)
}

func isDateFilterValue(value any) bool {
	switch value.(type) {
	case time.Time:
		return true
	case string:
		_, ok := parseFilterTime(value)
		return ok
	default:
		return false
	}
}

func filterNumberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		if err == nil {
			return number, true
		}
	}
	return 0, false
}

func parseFilterTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return time.Time{}, false
		}
		return typed.UTC(), true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
			when, err := time.Parse(layout, text)
			if err == nil {
				return when.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// entityRelationFilterSQL builds the entity-relation narrowing clause for the
// catalog feeds (SourceQuery.EntityFilter): keep Media holding a relation row
// from an entity of the declared kind with the given title, in one of the
// visible Libraries. The visible-Library check makes hidden indistinguishable
// from missing — an entity behind the Reveal contract filters everything out,
// exactly like one that does not exist. Composes onto the existing feed SQL
// like mediaFieldFilterSQL, so trash, certification, and reveal filtering on
// the Media side stay untouched. visible must be non-empty (callers already
// return early on an empty visible set). liveOnly also treats a trashed
// entity as missing: trashing keeps its relation rows for a restore.
func entityRelationFilterSQL(filter *EntityFilter, visible []LibraryID, liveOnly bool) (string, []any) {
	if filter == nil {
		return "", nil
	}
	args := make([]any, 0, len(visible)+2)
	args = append(args, filter.Kind, filter.Name)
	placeholders := make([]string, len(visible))
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	clause := `
		  AND EXISTS (SELECT 1 FROM entity_relation r
		      JOIN entity e ON e.id = r.from_entity_id
		      WHERE r.to_media_id = m.id AND e.kind = ? AND e.title = ?
		        AND e.library_id IN (` + strings.Join(placeholders, ", ") + `)`
	if liveOnly {
		clause += ` AND e.trashed_at_ms IS NULL`
	}
	return clause + `)`, args
}

// containerRollUpSQL wraps the "all-items" candidate query matched so each
// Media held by a member container whose parent container is live and in a
// visible Library is listed once as that parent Entity; every other Media is
// listed as itself. Both kinds sort together by title. placeholders and
// visible are the visible-Library binds matched already uses; the returned
// args follow matched's, before LIMIT and OFFSET.
func containerRollUpSQL(matched string, rollUp *ContainerRollUp, placeholders []string, visible []LibraryID) (string, []any) {
	hasClimbed := len(rollUp.MemberRelations) > 0 && len(rollUp.ParentRelations) > 0
	hasDirect := len(rollUp.DirectRelations) > 0

	args := make([]any, 0, len(rollUp.MemberRelations)+len(rollUp.ParentRelations)+len(rollUp.DirectRelations)*2+len(visible)*3)
	var ctes []string
	var topParts []string

	if hasClimbed {
		members := sqlPlaceholders(len(rollUp.MemberRelations))
		parents := sqlPlaceholders(len(rollUp.ParentRelations))
		for _, relation := range rollUp.MemberRelations {
			args = append(args, relation)
		}
		for _, relation := range rollUp.ParentRelations {
			args = append(args, relation)
		}
		for _, id := range visible {
			args = append(args, id)
		}
		ctes = append(ctes, `climbed AS (
			SELECT mt.id AS media_id, t.id AS top_id, t.title AS top_title, t.library_id AS top_library_id
			FROM matched mt
			JOIN entity_relation c ON c.to_media_id = mt.id
			  AND c.relation IN (`+members+`)
			JOIN entity sn ON sn.id = c.from_entity_id AND sn.trashed_at_ms IS NULL
			JOIN entity_relation p ON p.from_entity_id = c.from_entity_id
			  AND p.relation IN (`+parents+`)
			JOIN entity t ON t.id = p.to_entity_id AND t.trashed_at_ms IS NULL
			  AND t.library_id IN (`+strings.Join(placeholders, ", ")+`)
		)`)
		topParts = append(topParts, `SELECT media_id, top_id, top_title, top_library_id FROM climbed`)
	}

	if hasDirect {
		direct := sqlPlaceholders(len(rollUp.DirectRelations))
		libs := strings.Join(placeholders, ", ")
		for _, relation := range rollUp.DirectRelations {
			args = append(args, relation)
		}
		for _, id := range visible {
			args = append(args, id)
		}
		for _, relation := range rollUp.DirectRelations {
			args = append(args, relation)
		}
		for _, id := range visible {
			args = append(args, id)
		}
		notClimbed := ""
		if hasClimbed {
			notClimbed = " AND mt.id NOT IN (SELECT media_id FROM climbed)"
		}
		// Keep single-track audio items (e.g. standalone singles) unrolled;
		// multi-track audio containers (>= 2 visible tracks) and non-audio containers roll up.
		ctes = append(ctes, `direct AS (
			SELECT mt.id AS media_id, t.id AS top_id, t.title AS top_title, t.library_id AS top_library_id
			FROM matched mt
			JOIN media mt_m ON mt_m.id = mt.id
			JOIN entity_relation c ON c.to_media_id = mt.id
			  AND c.relation IN (`+direct+`)
			JOIN entity t ON t.id = c.from_entity_id AND t.trashed_at_ms IS NULL
			  AND t.library_id IN (`+libs+`)
			WHERE (mt_m.media_class != 'audio' OR (
				SELECT COUNT(DISTINCT mc.to_media_id)
				FROM entity_relation mc
				JOIN media mm ON mm.id = mc.to_media_id
				WHERE mc.from_entity_id = t.id
				  AND mc.relation IN (`+direct+`)
				  AND mm.trashed_at_ms IS NULL
				  AND mm.library_id IN (`+libs+`)
			) >= 2)`+notClimbed+`
		)`)
		topParts = append(topParts, `SELECT media_id, top_id, top_title, top_library_id FROM direct`)
	}

	return `
		WITH matched AS (` + matched + `),
		` + strings.Join(ctes, ",\n\t\t") + `,
		tops AS (
			` + strings.Join(topParts, "\n\t\t\tUNION ALL\n\t\t\t") + `
		)
		SELECT id, kind, title, library_id, duration_seconds, '' AS members FROM matched
		WHERE id NOT IN (SELECT media_id FROM tops)
		UNION ALL
		SELECT top_id, 'entity', top_title, top_library_id, 0, group_concat(media_id, char(31)) FROM tops
		GROUP BY top_id, top_title, top_library_id
		ORDER BY 3 COLLATE NOCASE, 1
		LIMIT ? OFFSET ?`, args
}

func (s *Store) sourceItemsSQL(ctx context.Context, filter LibraryFilter, fieldFilters map[string]any, entityFilter *EntityFilter, limit, offset int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+1)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	filterClause, filterArgs, err := mediaFieldFilterSQL(fieldFilters)
	if err != nil {
		return nil, err
	}
	args = append(args, filterArgs...)
	entityClause, entityArgs := entityRelationFilterSQL(entityFilter, visible, false)
	args = append(args, entityArgs...)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	args = append(args, limit, offset)
	// NOT INDEXED forces the natural rowid scan that ORDER BY m.rowid DESC wants.
	// Without it the planner uses media_library_title_nocase_idx for the
	// library_id seek, which returns rows in title order — so the whole visible
	// set must be materialized and re-sorted in a temp B-tree before LIMIT,
	// turning a 50-row page into an 80k-row sort (~36ms on the bench fixture vs
	// ~0.1ms here). rowid cannot be indexed by a secondary index, so the scan is
	// the only plan that streams the ORDER BY and short-circuits at LIMIT.
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, m.library_id, m.duration_seconds
		FROM media m NOT INDEXED
		WHERE m.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))`+filterClause+entityClause+certClause+`
		ORDER BY m.rowid DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var id MediaID
		var title string
		var libraryID LibraryID
		var durationSeconds int
		if err := rows.Scan(&id, &title, &libraryID, &durationSeconds); err != nil {
			return nil, err
		}
		items = append(items, SourceItem{
			ID:        id,
			Kind:      "media",
			Title:     title,
			LibraryID: libraryID,
			Summary:   mediaDurationSummary(durationSeconds),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) onThisDayItemsSQL(ctx context.Context, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+3)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	serverToday := s.serverNow()
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, serverToday.Format("01-02"), serverToday.Format("2006"))
	args = append(args, certArgs...)
	args = append(args, limit, offset)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, m.library_id, m.duration_seconds
		FROM media m
		JOIN media_field_date fd ON fd.media_id = m.id AND fd.field = 'capture-date'
		WHERE m.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND (NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)
		       OR EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.missing_at_ms IS NULL))
		  AND strftime('%m-%d', fd.value_ms / 1000, 'unixepoch') = ?
		  AND strftime('%Y', fd.value_ms / 1000, 'unixepoch') < ?`+certClause+`
		ORDER BY fd.value_ms DESC, m.id
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var id MediaID
		var title string
		var libraryID LibraryID
		var durationSeconds int
		if err := rows.Scan(&id, &title, &libraryID, &durationSeconds); err != nil {
			return nil, err
		}
		items = append(items, SourceItem{
			ID:        id,
			Kind:      "media",
			Title:     title,
			LibraryID: libraryID,
			Summary:   mediaDurationSummary(durationSeconds),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) insertDeviceSession(ctx context.Context, device Device, session Session) error {
	distributionClass, err := NormalizeDeviceDistributionClass(device.DistributionClass)
	if err != nil {
		return err
	}
	device.DistributionClass = distributionClass
	deviceSurface, err := NormalizeDeviceSurface(device.Surface)
	if err != nil {
		return err
	}
	device.Surface = deviceSurface
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertDeviceSessionTx(ctx, tx, device, session); err != nil {
		return err
	}
	return tx.Commit()
}

// insertDeviceSessionTx inserts a normalized Device and its enrolled Session
// inside tx.
func insertDeviceSessionTx(ctx context.Context, tx *sql.Tx, device Device, session Session) error {
	var playerProfileID any
	if device.PlayerProfileID != "" {
		playerProfileID = string(device.PlayerProfileID)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO device(id, name, role, distribution_class, device_surface, secret_hash, revoked, player_profile_id, player_display_name, player_binding_generation) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		device.ID, device.Name, device.Role, device.DistributionClass, device.Surface, device.SecretHash, boolInt(device.Revoked), playerProfileID, device.PlayerDisplayName, device.PlayerBindingGeneration,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, admin_until_ms, kind) VALUES (?, ?, ?, ?, ?, ?, ?)",
		session.ID, session.DeviceID, session.ProfileID, boolInt(session.Revealed), nullableMillis(session.ExpiresAt), nullableMillis(session.AdminUntil), SessionKindEnrolled,
	)
	return err
}

func (s *Store) insertPairingCode(ctx context.Context, code PairingCode) error {
	_, err := s.catalogW.ExecContext(ctx,
		"INSERT INTO pairing_code(code, profile_id, device_role, expires_at_ms, consumed_at_ms) VALUES (?, ?, ?, ?, ?)",
		code.Code, code.ProfileID, code.DeviceRole, timeToMillis(code.ExpiresAt), nullableMillis(code.ConsumedAt),
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint") {
		return ErrPairingCodeExists
	}
	return err
}

func (s *Store) consumePairingCodeSQL(ctx context.Context, code string, consumedAt time.Time) (PairingCode, error) {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return PairingCode{}, err
	}
	defer tx.Rollback()
	pending, err := pendingPairingCode(ctx, tx, code, consumedAt)
	if err != nil {
		return PairingCode{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE pairing_code SET consumed_at_ms = ? WHERE code = ?",
		timeToMillis(consumedAt), code,
	); err != nil {
		return PairingCode{}, err
	}
	return pending, tx.Commit()
}

func pendingPairingCode(ctx context.Context, q queryRower, code string, now time.Time) (PairingCode, error) {
	var pending PairingCode
	var expires int64
	err := q.QueryRowContext(ctx,
		"SELECT code, profile_id, device_role, expires_at_ms FROM pairing_code WHERE code = ? AND consumed_at_ms IS NULL AND expires_at_ms > ?",
		code, timeToMillis(now),
	).Scan(&pending.Code, &pending.ProfileID, &pending.DeviceRole, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingCode{}, ErrPairingCodeInvalid
	}
	if err != nil {
		return PairingCode{}, err
	}
	pending.ExpiresAt = millisToTime(expires)
	return pending, nil
}

func (s *Store) validPairingCode(ctx context.Context, code string, now time.Time) (PairingCode, error) {
	return pendingPairingCode(ctx, s.catalogR, code, now)
}

func (s *Store) redeemPairingCode(ctx context.Context, code string, consumedAt time.Time, device Device, session Session) (PairingCode, error) {
	distributionClass, err := NormalizeDeviceDistributionClass(device.DistributionClass)
	if err != nil {
		return PairingCode{}, err
	}
	device.DistributionClass = distributionClass
	deviceSurface, err := NormalizeDeviceSurface(device.Surface)
	if err != nil {
		return PairingCode{}, err
	}
	device.Surface = deviceSurface
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return PairingCode{}, err
	}
	defer tx.Rollback()
	pending, err := pendingPairingCode(ctx, tx, code, consumedAt)
	if err != nil {
		return PairingCode{}, err
	}
	if session.ProfileID == "" {
		session.ProfileID = pending.ProfileID
	}
	if session.ProfileID != pending.ProfileID {
		return PairingCode{}, errors.New("session profile mismatch")
	}
	if device.Role != pending.DeviceRole {
		return PairingCode{}, errors.New("device role mismatch")
	}
	// A Profile disabled after the code was minted still refuses enrollment.
	var profileKind ProfileKind
	var disabled int
	err = tx.QueryRowContext(ctx, "SELECT kind, disabled FROM profile WHERE id = ?", session.ProfileID).Scan(&profileKind, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingCode{}, errors.New("profile not found")
	}
	if err != nil {
		return PairingCode{}, err
	}
	if disabled != 0 {
		return PairingCode{}, ErrProfileDisabled
	}
	if profileKind == ProfilePlayer {
		device.PlayerProfileID = session.ProfileID
		if strings.TrimSpace(device.PlayerDisplayName) == "" {
			device.PlayerDisplayName = device.Name
		}
		if device.PlayerBindingGeneration <= 0 {
			device.PlayerBindingGeneration = 1
		}
		session.Revealed = false
		session.AdminUntil = time.Time{}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE pairing_code SET consumed_at_ms = ? WHERE code = ?",
		timeToMillis(consumedAt), code,
	); err != nil {
		return PairingCode{}, err
	}
	var playerProfileID any
	if device.PlayerProfileID != "" {
		playerProfileID = string(device.PlayerProfileID)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO device(id, name, role, distribution_class, device_surface, secret_hash, revoked, player_profile_id, player_display_name, player_binding_generation) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		device.ID, device.Name, device.Role, device.DistributionClass, device.Surface, device.SecretHash, boolInt(device.Revoked), playerProfileID, device.PlayerDisplayName, device.PlayerBindingGeneration,
	); err != nil {
		return PairingCode{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES (?, ?, ?, ?, ?, ?)",
		session.ID, session.DeviceID, session.ProfileID, boolInt(session.Revealed), nullableMillis(session.ExpiresAt), SessionKindEnrolled,
	); err != nil {
		return PairingCode{}, err
	}
	return pending, tx.Commit()
}

func (s *Store) redeemPairingCodeForDevice(ctx context.Context, code string, consumedAt time.Time, deviceID DeviceID, role DeviceRole, oldSessionID SessionID, newSession Session, newSecretHash string) (PairingCode, error) {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return PairingCode{}, err
	}
	defer tx.Rollback()
	pending, err := pendingPairingCode(ctx, tx, code, consumedAt)
	if err != nil {
		return PairingCode{}, err
	}
	if pending.DeviceRole != role {
		// Leave the code unconsumed: it still grants what it was minted for.
		return PairingCode{}, ErrPairingCodeRoleMismatch
	}
	var boundProfileID string
	var revoked int
	err = tx.QueryRowContext(ctx, "SELECT revoked, COALESCE(player_profile_id, '') FROM device WHERE id = ?", deviceID).Scan(&revoked, &boundProfileID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && revoked != 0) {
		return PairingCode{}, errors.New("device not found or revoked")
	}
	if err != nil {
		return PairingCode{}, err
	}
	if boundProfileID != "" {
		var disabled int
		var kind ProfileKind
		err = tx.QueryRowContext(ctx, "SELECT kind, disabled FROM profile WHERE id = ?", boundProfileID).Scan(&kind, &disabled)
		if err != nil {
			return PairingCode{}, err
		}
		if disabled != 0 {
			return PairingCode{}, ErrProfileDisabled
		}
		if kind != ProfilePlayer {
			return PairingCode{}, ValidationError("bound profile is not kind player")
		}
		newSession.ProfileID = ProfileID(boundProfileID)
		newSession.Revealed = false
		newSession.AdminUntil = time.Time{}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE pairing_code SET consumed_at_ms = ? WHERE code = ?",
		timeToMillis(consumedAt), code,
	); err != nil {
		return PairingCode{}, err
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE device SET role = ?, secret_hash = ? WHERE id = ? AND revoked = 0",
		role, newSecretHash, deviceID,
	)
	if err != nil {
		return PairingCode{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PairingCode{}, err
	}
	if affected == 0 {
		return PairingCode{}, errors.New("device not found or revoked")
	}
	result, err = tx.ExecContext(ctx,
		"DELETE FROM auth_session WHERE id = ? AND device_id = ?",
		oldSessionID, deviceID,
	)
	if err != nil {
		return PairingCode{}, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return PairingCode{}, err
	}
	if affected == 0 {
		return PairingCode{}, errors.New("session not found")
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES (?, ?, ?, ?, ?, ?)",
		newSession.ID, newSession.DeviceID, newSession.ProfileID, boolInt(newSession.Revealed), nullableMillis(newSession.ExpiresAt), SessionKindEnrolled,
	); err != nil {
		return PairingCode{}, err
	}
	return pending, tx.Commit()
}

func (s *Store) updateDeviceRevoked(ctx context.Context, id DeviceID, revoked bool) error {
	result, err := s.catalogW.ExecContext(ctx, "UPDATE device SET revoked = ? WHERE id = ?", boolInt(revoked), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("device not found")
	}
	if revoked {
		nowMS := timeToMillis(s.serverNow())
		_, _ = s.eventsW.ExecContext(ctx, `UPDATE party_screen SET dropped_at_ms = ? WHERE device_id = ? AND dropped_at_ms IS NULL`, nowMS, id)
	}
	return nil
}

func (s *Store) upsertMediaFile(ctx context.Context, file MediaFile, now time.Time) error {
	plane, err := s.catalogPlaneForMediaLocked(ctx, file.MediaID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertMediaFileTx(ctx, tx, file, now); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertMediaFileTx(ctx context.Context, tx *sql.Tx, file MediaFile, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file
		SET missing_at_ms = ?
		WHERE path = ? AND content_fingerprint <> ? AND missing_at_ms IS NULL
	`, timeToMillis(now), file.Path, file.ContentFingerprint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, oshash, phash, size_bytes, mtime_ms, missing_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(content_fingerprint) DO UPDATE SET
		  media_id = excluded.media_id,
		  path = excluded.path,
		  -- Empty hashes never clobber stored ones: a re-scan without
		  -- FFmpeg (no phash) must not erase an earlier computation.
		  oshash = CASE WHEN excluded.oshash = '' THEN media_file.oshash ELSE excluded.oshash END,
		  phash = CASE WHEN excluded.phash = '' THEN media_file.phash ELSE excluded.phash END,
		  -- Refresh the cheap-triple stat (size/mtime) so a later scan can match
		  -- it; a zero (unrecorded) value never clobbers a stored stat.
		  size_bytes = CASE WHEN excluded.size_bytes = 0 THEN media_file.size_bytes ELSE excluded.size_bytes END,
		  mtime_ms = CASE WHEN excluded.mtime_ms = 0 THEN media_file.mtime_ms ELSE excluded.mtime_ms END,
		  missing_at_ms = NULL,
		  last_seen_at_ms = excluded.last_seen_at_ms
	`, file.ContentFingerprint, file.ContentFingerprint, file.MediaID, file.Path, file.OSHash, file.PHash, file.SizeBytes, mediaFileTimeToMillis(file.ModTime), timeToMillis(now)); err != nil {
		return err
	}
	return nil
}

// replaceMediaFileInventoryTx full-replaces the per-stream inventory and font
// attachments for one content fingerprint. Delete-then-insert,
// not upsert: a re-probe is a full rescan, so a stream the new probe no longer
// reports must disappear, and stream order/identity always reflects the latest
// probe. Called only when the caller actually probed (non-nil slices).
// mediaFileStreamsSQL reads the persisted per-stream inventory for one content
// fingerprint, in ffprobe stream order (PLAY-1 wrote it; the playback decision
// table reads it back — ADR-0010 §4). A fingerprint with no inventory (a
// probe-less scan) returns an empty slice, not an error.
func (s *Store) mediaFileStreamsSQL(ctx context.Context, fingerprint string) ([]MediaFileStream, error) {
	return mediaFileStreamsFromPlane(ctx, s.catalogR, fingerprint)
}

// mediaFileStreamsFromPlane reads one fingerprint's stream inventory from a
// specific catalog plane (the global catalog or a per-Library encrypted DB —
// ADR-0045 §2.3), like mediaFileSidecarCaptionsFromPlane.
func mediaFileStreamsFromPlane(ctx context.Context, db *sql.DB, fingerprint string) ([]MediaFileStream, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT stream_index, stream_type, codec, profile, level, language,
		  is_default, is_forced, is_hearing_impaired, is_visual_impaired, is_commentary,
		  channels, channel_layout, bit_depth,
		  color_transfer, color_primaries, hdr10_plus, dovi_profile, dovi_bl_compat_id,
		  sample_rate, encoder_delay_samples, encoder_padding_samples, decoded_samples
		FROM media_file_stream
		WHERE content_fingerprint = ?
		ORDER BY stream_index
	`, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var streams []MediaFileStream
	for rows.Next() {
		var st MediaFileStream
		var isDefault, isForced, hearingImpaired, visualImpaired, commentary, hdr10Plus int
		if err := rows.Scan(&st.Index, &st.Type, &st.Codec, &st.Profile, &st.Level, &st.Language,
			&isDefault, &isForced, &hearingImpaired, &visualImpaired, &commentary,
			&st.Channels, &st.ChannelLayout, &st.BitDepth,
			&st.ColorTransfer, &st.ColorPrimaries, &hdr10Plus, &st.DoViProfile, &st.DoViBLCompatID,
			&st.SampleRate, &st.EncoderDelaySamples, &st.EncoderPaddingSamples, &st.DecodedSamples); err != nil {
			return nil, err
		}
		st.Default = isDefault != 0
		st.Forced = isForced != 0
		st.HearingImpaired = hearingImpaired != 0
		st.VisualImpaired = visualImpaired != 0
		st.Commentary = commentary != 0
		st.HDR10Plus = hdr10Plus != 0
		streams = append(streams, st)
	}
	return streams, rows.Err()
}

func (s *Store) mediaFileSidecarCaptionsSQL(ctx context.Context, fingerprint string) ([]MediaFileSidecarCaption, error) {
	return mediaFileSidecarCaptionsFromPlane(ctx, s.catalogR, fingerprint)
}

// mediaFileSidecarCaptionsFromPlane reads a file's sidecar caption tracks from a
// specific catalog plane (the global catalog or a per-Library encrypted DB —
// ADR-0045 §2.3). The bulk photo-export job reads encrypted-Library captions
// through this seam; the fingerprint-keyed public read stays on the global
// catalog for the existing serve paths.
func mediaFileSidecarCaptionsFromPlane(ctx context.Context, db *sql.DB, fingerprint string) ([]MediaFileSidecarCaption, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rel_path, kind, format, language, is_forced, is_sdh
		FROM media_file_sidecar_caption
		WHERE content_fingerprint = ?
		ORDER BY rel_path
	`, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var captions []MediaFileSidecarCaption
	for rows.Next() {
		var c MediaFileSidecarCaption
		var isForced, isSDH int
		if err := rows.Scan(&c.RelPath, &c.Kind, &c.Format, &c.Language, &isForced, &isSDH); err != nil {
			return nil, err
		}
		c.Forced = isForced != 0
		c.SDH = isSDH != 0
		captions = append(captions, c)
	}
	return captions, rows.Err()
}

func replaceMediaFileInventoryTx(ctx context.Context, tx *sql.Tx, encrypted bool, fingerprint string, streams []MediaFileStream, attachments []MediaFileFontAttachment) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_file_stream WHERE content_fingerprint = ?", fingerprint); err != nil {
		return err
	}
	// An Encrypted Library's routed schema (encrypted_library 0002) keeps two
	// frozen NOT NULL columns without defaults that the catalog lacks: the
	// legacy stream kind `type` and the font attachment `stream_index`. That
	// plane fills them alongside the shared columns (CORE-2055).
	streamLegacyColumns, streamLegacyValues := "", ""
	fontLegacyColumns, fontLegacyValues := "", ""
	if encrypted {
		streamLegacyColumns, streamLegacyValues = ", type", ", ?"
		fontLegacyColumns, fontLegacyValues = ", stream_index", ", ?"
	}
	for _, stream := range streams {
		args := []any{fingerprint, stream.Index, stream.Type, stream.Codec, stream.Profile, stream.Level, stream.Language,
			boolInt(stream.Default), boolInt(stream.Forced), boolInt(stream.HearingImpaired),
			boolInt(stream.VisualImpaired), boolInt(stream.Commentary),
			stream.Channels, stream.ChannelLayout, stream.BitDepth,
			stream.ColorTransfer, stream.ColorPrimaries, boolInt(stream.HDR10Plus), stream.DoViProfile, stream.DoViBLCompatID,
			stream.SampleRate, stream.EncoderDelaySamples, stream.EncoderPaddingSamples, stream.DecodedSamples}
		if encrypted {
			args = append(args, stream.Type)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_file_stream(
			  content_fingerprint, stream_index, stream_type, codec, profile, level, language,
			  is_default, is_forced, is_hearing_impaired, is_visual_impaired, is_commentary,
			  channels, channel_layout, bit_depth,
			  color_transfer, color_primaries, hdr10_plus, dovi_profile, dovi_bl_compat_id,
			  sample_rate, encoder_delay_samples, encoder_padding_samples, decoded_samples`+streamLegacyColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`+streamLegacyValues+`)
		`, args...); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_file_font_attachment WHERE content_fingerprint = ?", fingerprint); err != nil {
		return err
	}
	for i, att := range attachments {
		args := []any{fingerprint, att.Filename, att.MimeType}
		if encrypted {
			args = append(args, i)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_file_font_attachment(content_fingerprint, filename, mime_type`+fontLegacyColumns+`)
			VALUES (?, ?, ?`+fontLegacyValues+`)
			ON CONFLICT(content_fingerprint, filename) DO UPDATE SET mime_type = excluded.mime_type
		`, args...); err != nil {
			return err
		}
	}
	return nil
}

// replaceMediaFileSidecarsTx full-replaces the sidecar caption rows for one
// content fingerprint: delete-then-insert, the same
// refresh posture as the stream inventory, so a re-scan drops a removed sidecar
// and records a new one. Sidecars are discovered by a filesystem walk, not by
// ffprobe, so they carry on ImportMediaFile independently of the probe-derived
// stream inventory — a probe-less re-scan still leaves them nil (untouched).
func replaceMediaFileSidecarsTx(ctx context.Context, tx *sql.Tx, fingerprint string, sidecars []MediaFileSidecarCaption) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_file_sidecar_caption WHERE content_fingerprint = ?", fingerprint); err != nil {
		return err
	}
	for _, sc := range sidecars {
		kind := sc.Kind
		if kind == "" {
			kind = SidecarKindCaption
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_file_sidecar_caption(
			  content_fingerprint, rel_path, kind, format, language, is_forced, is_sdh)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(content_fingerprint, rel_path) DO UPDATE SET
			  kind = excluded.kind, format = excluded.format, language = excluded.language,
			  is_forced = excluded.is_forced, is_sdh = excluded.is_sdh
		`, fingerprint, sc.RelPath, kind, sc.Format, sc.Language, boolInt(sc.Forced), boolInt(sc.SDH)); err != nil {
			return err
		}
	}
	return nil
}

// importMediaFileSQL returns created=true when the upsert inserted a new Media
// row (no row with this id existed before), so the caller can emit media.created
// vs media.updated. Existence is read inside the same tx as the upsert so the
// classification cannot race a concurrent writer.
func (s *Store) importMediaFileSQL(ctx context.Context, plane catalogPlane, imported ImportMediaFile, now time.Time) (Media, MediaFile, bool, error) {
	// Observe the admitted inode before opening the write transaction. Only
	// an exact legacy reimport uses these stats, to invalidate stale values.
	var admittedSize, admittedMtime int64
	if imported.legacyFingerprint != "" {
		if info, err := os.Lstat(imported.Path); err == nil && info.Mode().IsRegular() {
			admittedSize, admittedMtime = info.Size(), mediaFileTimeToMillis(info.ModTime())
		}
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return Media{}, MediaFile{}, false, err
	}
	defer tx.Rollback()

	var mediaID MediaID
	var versionLabel string
	var partOrdinal int
	err = tx.QueryRowContext(ctx, `
		SELECT f.media_id, f.version_label, f.part_ordinal
		FROM media_file f JOIN media m ON m.id = f.media_id
		WHERE f.content_fingerprint = ? AND (f.missing_at_ms IS NULL OR (
		  f.path = ? AND f.version_label <> '' AND m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND NOT EXISTS (SELECT 1 FROM media_file live
		    WHERE live.media_id = f.media_id AND live.version_label = f.version_label
		      AND live.part_ordinal = f.part_ordinal AND live.missing_at_ms IS NULL AND live.path <> f.path)
		))
	`, imported.ContentFingerprint, imported.Path, imported.LibraryID).Scan(&mediaID, &versionLabel, &partOrdinal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Media{}, MediaFile{}, false, err
	}
	if imported.managedAdmission {
		for _, fingerprint := range []string{imported.ContentFingerprint, imported.legacyFingerprint} {
			if fingerprint == "" {
				continue
			}
			existing, err := checkManagedAdmissionFingerprintTx(ctx, tx, imported, fingerprint)
			if err != nil {
				return Media{}, MediaFile{}, false, err
			}
			if existing != "" && fingerprint == imported.legacyFingerprint {
				imported.ContentFingerprint, mediaID = fingerprint, existing
				if _, err := tx.ExecContext(ctx, `
					UPDATE media_file
					SET mtime_ms = 0, size_bytes = CASE WHEN size_bytes = ? THEN size_bytes ELSE 0 END
					WHERE content_fingerprint = ? AND (size_bytes <> ? OR mtime_ms <> ?)
				`, admittedSize, fingerprint, admittedSize, admittedMtime); err != nil {
					return Media{}, MediaFile{}, false, err
				}
			}
		}
	}
	// Bytes that turn up in another Library leave their Versions behind: moving
	// the whole Media would carry its other files into a Library their viewers
	// may not see. The file detaches into a Media of its own instead.
	detachedFrom := MediaID("")
	if mediaID != "" {
		shared, err := mediaHoldsOtherFilesInAnotherLibraryTx(ctx, tx, mediaID, imported)
		if err != nil {
			return Media{}, MediaFile{}, false, err
		}
		if shared {
			detachedFrom, mediaID = mediaID, ""
			versionLabel, partOrdinal = "", 0
		}
	}
	// A file a Subscription downloaded to its recorded target path joins the
	// entry's file-less Media instead of minting a new one, keeping the
	// feed's title (CORE-2953). The catalogue lives on the catalog plane only.
	boundEntry := false
	if mediaID == "" && !plane.encrypted {
		mediaID, err = subscriptionEntryMediaForPathTx(ctx, tx, imported.LibraryID, imported.Path)
		if err != nil {
			return Media{}, MediaFile{}, false, err
		}
		if mediaID == "" {
			mediaID, err = bindStagedSubscriptionEntryTx(ctx, tx, imported.LibraryID, imported.Path)
			if err != nil {
				return Media{}, MediaFile{}, false, err
			}
		}
		boundEntry = mediaID != ""
	}
	if mediaID == "" {
		if plane.encrypted {
			mediaID, err = s.allocateEncryptedMediaIDLocked(ctx)
			if err != nil {
				return Media{}, MediaFile{}, false, err
			}
		} else {
			mediaID = mediaIDForFingerprint(imported.ContentFingerprint)
			if mediaID == detachedFrom {
				mediaID = mediaIDForFingerprint(HashMediaFilePath(imported.ContentFingerprint + "\x00" + string(imported.LibraryID)))
			}
		}
	}
	// A re-imported file that is not its Media's first Version leaves the row's
	// probe facts alone: they describe that first Version (see
	// firstVersionFingerprintSQL), whose file playback trusts them for.
	keepTitle, keepProbeFacts := false, false
	if mediaID != "" && detachedFrom == "" {
		first, err := firstVersionFingerprintSQL(ctx, tx, mediaID)
		if err != nil {
			return Media{}, MediaFile{}, false, err
		}
		keepTitle = first != "" && first != imported.ContentFingerprint
		keepProbeFacts = keepTitle || (first != "" && imported.KeepProbeFacts)
	}
	if boundEntry {
		keepTitle = true
	}
	media, created, err := upsertMediaRowForImportTx(ctx, tx, mediaID, imported, plane.encrypted, keepTitle, keepProbeFacts)
	if err != nil {
		return Media{}, MediaFile{}, false, err
	}
	file := MediaFile{
		MediaID:            mediaID,
		Path:               imported.Path,
		ContentFingerprint: imported.ContentFingerprint,
		VersionLabel:       versionLabel,
		PartOrdinal:        partOrdinal,
		OSHash:             imported.OSHash,
		PHash:              imported.PHash,
		SizeBytes:          imported.SizeBytes,
		ModTime:            imported.ModTime,
		LastSeenAt:         now,
	}
	if err := upsertMediaFileTx(ctx, tx, file, now); err != nil {
		return Media{}, MediaFile{}, false, err
	}
	if boundEntry {
		if _, err := tx.ExecContext(ctx, `
			UPDATE subscription_entry SET download_state = 'downloaded'
			WHERE media_id = ? AND download_path = ?
		`, mediaID, imported.Path); err != nil {
			return Media{}, MediaFile{}, false, err
		}
	}
	if detachedFrom != "" {
		// A detached file is the only file of its new Media: it takes the
		// default slot whatever Version it held before.
		if _, err := tx.ExecContext(ctx,
			"UPDATE media_file SET version_label = '', part_ordinal = 0 WHERE content_fingerprint = ?",
			imported.ContentFingerprint); err != nil {
			return Media{}, MediaFile{}, false, err
		}
	}
	// Per-stream inventory: a probe full-replaces the stored
	// inventory for this content fingerprint (re-probe = full rescan, no partial
	// backfill). A probe-less re-scan carries nil slices and skips this entirely,
	// leaving the stored inventory intact — the same empty-never-clobbers posture
	// as the fingerprint hashes.
	if imported.Streams != nil || imported.FontAttachments != nil {
		if err := replaceMediaFileInventoryTx(ctx, tx, plane.encrypted, imported.ContentFingerprint, imported.Streams, imported.FontAttachments); err != nil {
			return Media{}, MediaFile{}, false, err
		}
	}
	// Sidecar caption tracks: a non-nil slice
	// full-replaces the stored sidecars for this content fingerprint (re-scan
	// refresh — a removed sidecar drops, a new one appears). A nil slice (the
	// cheap-triple fast-path, which does not re-walk the directory) leaves the
	// stored sidecars intact, like the empty-never-clobbers inventory above.
	if imported.Sidecars != nil {
		if err := replaceMediaFileSidecarsTx(ctx, tx, imported.ContentFingerprint, imported.Sidecars); err != nil {
			return Media{}, MediaFile{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Media{}, MediaFile{}, false, err
	}
	return media, file, created, nil
}

// subscriptionEntryMediaForPathTx returns the untrashed, still file-less
// Media of the Library's feed entry whose recorded download path is path, or
// "" when none is.
func subscriptionEntryMediaForPathTx(ctx context.Context, tx *sql.Tx, libraryID LibraryID, path string) (MediaID, error) {
	var mediaID MediaID
	err := tx.QueryRowContext(ctx, `
		SELECT se.media_id
		FROM subscription_entry se
		JOIN subscription s ON s.id = se.subscription_id
		JOIN media m ON m.id = se.media_id
		WHERE se.download_path = ? AND s.library_id = ? AND m.library_id = ? AND m.trashed_at_ms IS NULL
		  AND NOT EXISTS (SELECT 1 FROM media_file mf WHERE mf.media_id = se.media_id AND mf.missing_at_ms IS NULL)
		ORDER BY se.first_seen_at_ms, se.subscription_id, se.guid
		LIMIT 1
	`, path, libraryID, libraryID).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return mediaID, err
}

// mediaHoldsOtherFilesInAnotherLibraryTx reports whether mediaID belongs to a
// Library other than imported's and holds a live file other than imported's.
func mediaHoldsOtherFilesInAnotherLibraryTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, imported ImportMediaFile) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM media m JOIN media_file f ON f.media_id = m.id
		WHERE m.id = ? AND m.library_id <> ? AND f.content_fingerprint <> ? AND f.missing_at_ms IS NULL
		LIMIT 1`,
		mediaID, imported.LibraryID, imported.ContentFingerprint,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// checkManagedAdmissionFingerprintTx rejects a managed admission whose content is
// already cataloged at a different path or in a different library, whether that
// row is live or retained as missing (a trashed file keeps its row so restore
// can revive it by path; repointing it would orphan the restore). It returns
// the Media ID for an exact live match at this path in this library, or an
// exact legacy match even when its file is missing or its Media is soft-trashed.
func checkManagedAdmissionFingerprintTx(ctx context.Context, tx *sql.Tx, imported ImportMediaFile, fingerprint string) (MediaID, error) {
	var mediaID MediaID
	var path string
	var libraryID LibraryID
	var missingAt sql.NullInt64
	var trashed bool
	err := tx.QueryRowContext(ctx, `
		SELECT f.media_id, f.path, m.library_id, f.missing_at_ms, m.trashed_at_ms IS NOT NULL
		FROM media_file f JOIN media m ON m.id = f.media_id
		WHERE f.content_fingerprint = ?`,
		fingerprint,
	).Scan(&mediaID, &path, &libraryID, &missingAt, &trashed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	legacy := fingerprint == imported.legacyFingerprint
	if path == imported.Path && libraryID == imported.LibraryID {
		if missingAt.Valid && !legacy {
			return "", nil
		}
		return mediaID, nil
	}
	if missingAt.Valid || trashed {
		return "", ValidationError("a file with the same content is retained in a library's trash or missing files")
	}
	return "", ValidationError("a file with the same content is already in a library")
}

// upsertMediaRowForImportTx inserts or updates the Media row for an imported
// file. keepProbeFacts leaves an existing row's duration, container, codec and
// frame size as they are.
func upsertMediaRowForImportTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, imported ImportMediaFile, encrypted, keepTitle, keepProbeFacts bool) (Media, bool, error) {
	var existed int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", mediaID).Scan(&existed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Media{}, false, err
	}
	created := errors.Is(err, sql.ErrNoRows)
	media := Media{
		ID:              mediaID,
		LibraryID:       imported.LibraryID,
		Title:           imported.Title,
		Summary:         imported.Summary,
		Year:            imported.Year,
		DurationSeconds: max(0, imported.DurationSecondsHint),
		Container:       imported.Container,
		Codec:           imported.Codec,
		Class:           imported.Class,
		VideoWidth:      max(0, imported.VideoWidthHint),
		VideoHeight:     max(0, imported.VideoHeightHint),
	}
	rawClass := string(imported.Class)
	if keepProbeFacts {
		rawClass = ""
	}
	if media.Class == "" {
		media.Class = MediaClassVideo
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, video_width, video_height, media_class, mutated_by, extra)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'scan', ?)
		ON CONFLICT(id) DO UPDATE SET
		  library_id = excluded.library_id,
		  title = CASE WHEN ? OR media.title_identified = 1 THEN media.title ELSE excluded.title END,
		  summary = CASE WHEN media.summary_identified = 1 THEN media.summary
		                 WHEN excluded.summary <> ''       THEN excluded.summary
		                 ELSE media.summary END,
		  year = CASE WHEN media.year_identified = 1 THEN media.year
		              WHEN excluded.year > 0         THEN excluded.year
		              ELSE media.year END,
		  duration_seconds = CASE WHEN ? THEN media.duration_seconds ELSE excluded.duration_seconds END,
		  container = CASE WHEN ? THEN media.container ELSE excluded.container END,
		  codec = CASE WHEN ? THEN media.codec ELSE excluded.codec END,
		  video_width = CASE WHEN ? OR excluded.video_width <= 0 THEN media.video_width ELSE excluded.video_width END,
		  video_height = CASE WHEN ? OR excluded.video_height <= 0 THEN media.video_height ELSE excluded.video_height END,
		  media_class = CASE WHEN ? <> '' THEN ? ELSE media.media_class END,
		  extra = CASE WHEN ? OR excluded.extra = 1 THEN excluded.extra ELSE media.extra END
	`, media.ID, media.LibraryID, media.Title, media.Summary, nullableInt(media.Year), media.DurationSeconds, media.Container, media.Codec, media.VideoWidth, media.VideoHeight, media.Class, boolInt(imported.Extra),
		keepTitle, keepProbeFacts, keepProbeFacts, keepProbeFacts, keepProbeFacts, keepProbeFacts, rawClass, rawClass, imported.ExtraFromPath); err != nil {
		return Media{}, false, err
	}
	// Record scan-tier provenance for the built-ins the import supplies. The
	// per-field CASE above already preserves identification-locked values;
	// recordFieldProvenanceTx only raises provenance, so a manual/operator field
	// keeps its recorded source and a scan write never demotes it.
	// The title always follows the filename; summary/year record only when the
	// import carries a value (the same empty-never-clobbers arm as the CASE). The
	// encrypted-library plane has no provenance table (it carries no media_field
	// side tables either) — skip it there.
	if !encrypted {
		if err := recordFieldProvenanceTx(ctx, tx, media.ID, "title", MutationSourceScan); err != nil {
			return Media{}, false, err
		}
		if imported.Summary != "" {
			if err := recordFieldProvenanceTx(ctx, tx, media.ID, "summary", MutationSourceScan); err != nil {
				return Media{}, false, err
			}
		}
		if imported.Year > 0 {
			if err := recordFieldProvenanceTx(ctx, tx, media.ID, "year", MutationSourceScan); err != nil {
				return Media{}, false, err
			}
		}
	}
	var storedSummary sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT title, summary FROM media WHERE id = ?", media.ID).Scan(&media.Title, &storedSummary); err != nil {
		return Media{}, false, err
	}
	media.Summary = storedSummary.String
	if keepProbeFacts {
		if err := tx.QueryRowContext(ctx,
			"SELECT duration_seconds, container, codec, video_width, video_height, media_class FROM media WHERE id = ?", media.ID,
		).Scan(&media.DurationSeconds, &media.Container, &media.Codec, &media.VideoWidth, &media.VideoHeight, &media.Class); err != nil {
			return Media{}, false, err
		}
	}
	return media, created, nil
}

func (s *Store) importMediaFilePartsSQL(ctx context.Context, plane catalogPlane, parts []ImportMediaFilePart, resetMethods []string, now time.Time) (Media, []MediaFile, bool, error) {
	ordered := append([]ImportMediaFilePart(nil), parts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].SourceOrdinal < ordered[j].SourceOrdinal
	})
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return Media{}, nil, false, err
	}
	defer tx.Rollback()

	var mediaID MediaID
	for _, part := range ordered {
		err = tx.QueryRowContext(ctx,
			"SELECT media_id FROM media_file WHERE content_fingerprint = ? AND missing_at_ms IS NULL",
			part.File.ContentFingerprint,
		).Scan(&mediaID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Media{}, nil, false, err
		}
		if mediaID != "" {
			break
		}
	}
	if mediaID == "" {
		if plane.encrypted {
			mediaID, err = s.allocateEncryptedMediaIDLocked(ctx)
			if err != nil {
				return Media{}, nil, false, err
			}
		} else {
			mediaID = mediaIDForFingerprint(ordered[0].File.ContentFingerprint)
		}
	}

	primary := ordered[0].File
	keepProbeFacts := false
	if primary.KeepProbeFacts {
		first, err := firstVersionFingerprintSQL(ctx, tx, mediaID)
		if err != nil {
			return Media{}, nil, false, err
		}
		keepProbeFacts = first == primary.ContentFingerprint
	}
	media, created, err := upsertMediaRowForImportTx(ctx, tx, mediaID, primary, plane.encrypted, false, keepProbeFacts)
	if err != nil {
		return Media{}, nil, false, err
	}
	if !created && len(resetMethods) > 0 {
		changed, err := mediaPartsDifferTx(ctx, tx, mediaID, ordered)
		if err != nil {
			return Media{}, nil, false, err
		}
		if changed {
			if err := resetGroupMarkersTx(ctx, tx, plane, mediaID, resetMethods); err != nil {
				return Media{}, nil, false, err
			}
		}
	}
	if len(resetMethods) > 0 {
		// A Part moving in from another Media regroups that Media too: its
		// remaining membership already matches on its next scan, so its stale
		// group Markers are reset here, in the same transaction.
		sources, err := partSourceMediaTx(ctx, tx, mediaID, ordered)
		if err != nil {
			return Media{}, nil, false, err
		}
		for _, source := range sources {
			if err := resetGroupMarkersTx(ctx, tx, plane, source, resetMethods); err != nil {
				return Media{}, nil, false, err
			}
		}
	}
	files := make([]MediaFile, 0, len(ordered))
	for i, part := range ordered {
		imported := part.File
		file := MediaFile{
			MediaID:            mediaID,
			Path:               imported.Path,
			ContentFingerprint: imported.ContentFingerprint,
			PartOrdinal:        i,
			OSHash:             imported.OSHash,
			PHash:              imported.PHash,
			SizeBytes:          imported.SizeBytes,
			ModTime:            imported.ModTime,
			LastSeenAt:         now,
		}
		if err := upsertMediaFilePartTx(ctx, tx, file, now); err != nil {
			return Media{}, nil, false, err
		}
		if imported.Streams != nil || imported.FontAttachments != nil {
			if err := replaceMediaFileInventoryTx(ctx, tx, plane.encrypted, imported.ContentFingerprint, imported.Streams, imported.FontAttachments); err != nil {
				return Media{}, nil, false, err
			}
		}
		if imported.Sidecars != nil {
			if err := replaceMediaFileSidecarsTx(ctx, tx, imported.ContentFingerprint, imported.Sidecars); err != nil {
				return Media{}, nil, false, err
			}
		}
		files = append(files, file)
	}
	if err := tx.Commit(); err != nil {
		return Media{}, nil, false, err
	}
	return media, files, created, nil
}

// partSourceMediaTx lists the Media other than mediaID that currently own one
// of the ordered Parts' files, each once, in Part order.
func partSourceMediaTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, ordered []ImportMediaFilePart) ([]MediaID, error) {
	var sources []MediaID
	for _, part := range ordered {
		rows, err := tx.QueryContext(ctx,
			"SELECT DISTINCT media_id FROM media_file WHERE content_fingerprint = ? AND media_id <> ?",
			part.File.ContentFingerprint, mediaID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var source MediaID
			if err := rows.Scan(&source); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if !slices.Contains(sources, source) {
				sources = append(sources, source)
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return sources, nil
}

// resetGroupMarkersTx drops a regrouped Media's detected Markers of methods in
// the import transaction. Markers live only in the catalog database; an
// encrypted library's plane has no marker table, so there is nothing to reset.
func resetGroupMarkersTx(ctx context.Context, tx *sql.Tx, plane catalogPlane, mediaID MediaID, methods []string) error {
	if plane.encrypted {
		return nil
	}
	for _, method := range methods {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM marker
			WHERE media_id = ? AND provenance = ? AND method = ?
		`, mediaID, string(ProvenanceDetected), method); err != nil {
			return err
		}
	}
	return nil
}

func mediaPartsDifferTx(ctx context.Context, tx *sql.Tx, mediaID MediaID, ordered []ImportMediaFilePart) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content_fingerprint
		FROM media_file
		WHERE media_id = ? AND version_label = '' AND missing_at_ms IS NULL
		ORDER BY part_ordinal ASC
	`, mediaID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			return false, err
		}
		if i >= len(ordered) || ordered[i].File.ContentFingerprint != fingerprint {
			return true, nil
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return i != len(ordered), nil
}

func upsertMediaFilePartTx(ctx context.Context, tx *sql.Tx, file MediaFile, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file
		SET missing_at_ms = ?
		WHERE path = ? AND content_fingerprint <> ? AND missing_at_ms IS NULL
	`, timeToMillis(now), file.Path, file.ContentFingerprint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_file
		SET missing_at_ms = ?
		WHERE media_id = ? AND version_label = ? AND part_ordinal = ?
		  AND content_fingerprint <> ? AND missing_at_ms IS NULL
	`, timeToMillis(now), file.MediaID, file.VersionLabel, file.PartOrdinal, file.ContentFingerprint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, oshash, phash, size_bytes, mtime_ms, missing_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(content_fingerprint) DO UPDATE SET
		  media_id = excluded.media_id,
		  path = excluded.path,
		  version_label = excluded.version_label,
		  part_ordinal = excluded.part_ordinal,
		  oshash = CASE WHEN excluded.oshash = '' THEN media_file.oshash ELSE excluded.oshash END,
		  phash = CASE WHEN excluded.phash = '' THEN media_file.phash ELSE excluded.phash END,
		  size_bytes = CASE WHEN excluded.size_bytes = 0 THEN media_file.size_bytes ELSE excluded.size_bytes END,
		  mtime_ms = CASE WHEN excluded.mtime_ms = 0 THEN media_file.mtime_ms ELSE excluded.mtime_ms END,
		  missing_at_ms = NULL,
		  last_seen_at_ms = excluded.last_seen_at_ms
	`, file.ContentFingerprint, file.ContentFingerprint, file.MediaID, file.Path, file.VersionLabel, file.PartOrdinal, file.OSHash, file.PHash, file.SizeBytes, mediaFileTimeToMillis(file.ModTime), timeToMillis(file.LastSeenAt)); err != nil {
		return err
	}
	return nil
}

// addMediaFileVersionSQL inserts an additional file for an existing Media at a
// Version/Part slot. Unlike upsertMediaFileTx (the import path,
// which only ever writes the default slot), this honours file.VersionLabel and
// file.PartOrdinal, so a Media can hold many files. ON CONFLICT(content_
// fingerprint) refreshes a re-scan of the same bytes in place — the same
// content-identity keying as the import upsert. Optional per-stream inventory
// is full-replaced for the fingerprint like the import path.
func (s *Store) addMediaFileVersionSQL(ctx context.Context, file MediaFile, streams []MediaFileStream) error {
	plane, err := s.catalogPlaneForMediaLocked(ctx, file.MediaID)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var one int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", file.MediaID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("media not found")
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, version_label, part_ordinal, oshash, phash, size_bytes, mtime_ms, missing_at_ms, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT(content_fingerprint) DO UPDATE SET
		  media_id = excluded.media_id,
		  path = excluded.path,
		  version_label = excluded.version_label,
		  part_ordinal = excluded.part_ordinal,
		  oshash = CASE WHEN excluded.oshash = '' THEN media_file.oshash ELSE excluded.oshash END,
		  phash = CASE WHEN excluded.phash = '' THEN media_file.phash ELSE excluded.phash END,
		  size_bytes = CASE WHEN excluded.size_bytes = 0 THEN media_file.size_bytes ELSE excluded.size_bytes END,
		  mtime_ms = CASE WHEN excluded.mtime_ms = 0 THEN media_file.mtime_ms ELSE excluded.mtime_ms END,
		  missing_at_ms = NULL,
		  last_seen_at_ms = excluded.last_seen_at_ms
	`, file.ContentFingerprint, file.ContentFingerprint, file.MediaID, file.Path, file.VersionLabel, file.PartOrdinal,
		file.OSHash, file.PHash, file.SizeBytes, mediaFileTimeToMillis(file.ModTime), timeToMillis(file.LastSeenAt)); err != nil {
		return err
	}
	if streams != nil {
		if err := replaceMediaFileInventoryTx(ctx, tx, plane.encrypted, file.ContentFingerprint, streams, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// updateMediaMetadataSQL applies the non-nil MediaMetadata fields. The title
// FTS index stays current through the 0006 UPDATE OF title trigger; summary
// and year are deliberately outside FTS (0011).
func (s *Store) updateMediaMetadataSQL(ctx context.Context, id MediaID, metadata MediaMetadata) error {
	if metadata.Source == MutationSourceAI {
		return s.updateAIMediaMetadataSQL(ctx, id, metadata)
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateMediaMetadataTx(ctx, tx, id, metadata); err != nil {
		return err
	}
	return tx.Commit()
}

// updateMediaMetadataTx applies the non-nil MediaMetadata fields at
// metadata.Source (operator when empty) inside the caller's transaction, which
// runs on the Media's catalog plane.
func updateMediaMetadataTx(ctx context.Context, tx *sql.Tx, id MediaID, metadata MediaMetadata) error {
	source := metadata.Source
	if source == "" {
		source = MutationSourceOperator
	}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("media not found")
		}
		return err
	}
	cleared := make(map[string]bool, len(metadata.ClearLocks))
	for _, field := range metadata.ClearLocks {
		if cleared[field] {
			continue
		}
		if err := setMediaFieldLockTx(ctx, tx, id, field, false, true); err != nil {
			return err
		}
		cleared[field] = true
	}
	// Per-field source-precedence gate: a built-in field lands
	// only when the write's source rank >= the field's recorded rank, so an
	// operator re-identify cannot overwrite a manual edit. Every landed field
	// records its (media, field, source) provenance and keeps the legacy
	// media.<field>_identified boolean in sync as a derived cache — the record
	// is authoritative on conflict, the boolean subsumed under it.
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	builtins := []struct {
		field  string
		column string
		value  any
		set    bool
	}{
		{"title", "title", derefString(metadata.Title), metadata.Title != nil},
		{"summary", "summary", derefString(metadata.Summary), metadata.Summary != nil},
		{"year", "year", derefInt(metadata.Year), metadata.Year != nil},
	}
	for _, b := range builtins {
		if !b.set {
			continue
		}
		allowed, err := canWriteFieldTx(ctx, tx, id, b.field, source)
		if err != nil {
			return err
		}
		if !allowed {
			continue // a higher-precedence source owns this field — preserve it
		}
		sets = append(sets, b.column+" = ?", b.column+"_identified = 1")
		args = append(args, b.value)
		if err := recordFieldProvenanceTx(ctx, tx, id, b.field, source); err != nil {
			return err
		}
	}
	if metadata.SortTitle != nil {
		if err := updateSortTitleTx(ctx, tx, id, strings.TrimSpace(*metadata.SortTitle), source); err != nil {
			return err
		}
	}
	if len(sets) == 0 {
		return nil // nothing outranked the stored values; media exists
	}
	sets = append(sets, "mutated_by = ?")
	args = append(args, string(source))
	args = append(args, id)
	// #nosec G202 -- assignments use fixed column names and bound values.
	_, err := tx.ExecContext(ctx, "UPDATE media SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	return err
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Store) updateAIMediaMetadataSQL(ctx context.Context, id MediaID, metadata MediaMetadata) error {
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentTitle string
	var currentSummary sql.NullString
	var currentYear sql.NullInt64
	var titleLocked, summaryLocked, yearLocked int
	err = tx.QueryRowContext(ctx, `
		SELECT title, summary, year, title_identified, summary_identified, year_identified
		FROM media WHERE id = ?`, id,
	).Scan(&currentTitle, &currentSummary, &currentYear, &titleLocked, &summaryLocked, &yearLocked)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("media not found")
	}
	if err != nil {
		return err
	}
	// Machine-derived (AI) is the lowest tier and is a gap-fill: it only writes a
	// field that is currently EMPTY and not identification-locked. The
	// emptiness guard — not the full precedence rank — governs the write, so AI
	// still fills a blank field even though scan recorded its provenance. Each
	// landed field records machine-derived provenance so the record stays complete
	// (a later re-check knows AI owns the value; manual/operator still outrank it).
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	landed := make([]string, 0, 3)
	aiFields := []struct {
		field  string
		column string
		empty  bool
		locked bool
		value  any
		set    bool
	}{
		{"title", "title", currentTitle == "", titleLocked != 0, derefString(metadata.Title), metadata.Title != nil},
		{"summary", "summary", !currentSummary.Valid || currentSummary.String == "", summaryLocked != 0, derefString(metadata.Summary), metadata.Summary != nil},
		{"year", "year", !currentYear.Valid || currentYear.Int64 == 0, yearLocked != 0, derefInt(metadata.Year), metadata.Year != nil},
	}
	for _, f := range aiFields {
		if !f.set || f.locked || !f.empty {
			continue
		}
		sets = append(sets, f.column+" = ?")
		args = append(args, f.value)
		landed = append(landed, f.field)
	}
	if len(sets) == 0 {
		return tx.Commit()
	}
	sets = append(sets, "mutated_by = ?")
	args = append(args, string(MutationSourceAI), id)
	if _, err := tx.ExecContext(ctx,
		"UPDATE media SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return err
	}
	for _, field := range landed {
		if err := recordFieldProvenanceTx(ctx, tx, id, field, MutationSourceAI); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) insertAuthAttempt(ctx context.Context, attempt AuthAttempt) error {
	_, writer := s.authAttemptDBs(attempt.Action)
	_, err := writer.ExecContext(ctx,
		"INSERT INTO auth_attempt(occurred_at_ms, action, key, success) VALUES (?, ?, ?, ?)",
		timeToMillis(attempt.OccurredAt), attempt.Action, attempt.Key, boolInt(attempt.Success),
	)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableMillis(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return timeToMillis(value)
}

func (s *Store) allItemsSQL(ctx context.Context, filter LibraryFilter, fieldFilters map[string]any, entityFilter *EntityFilter, rollUp *ContainerRollUp, limit, offset int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible)+1)
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	filterClause, filterArgs, err := mediaFieldFilterSQL(fieldFilters)
	if err != nil {
		return nil, err
	}
	args = append(args, filterArgs...)
	entityClause, entityArgs := entityRelationFilterSQL(entityFilter, visible, false)
	args = append(args, entityArgs...)
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	matched := `
		SELECT m.id, 'media' AS kind, m.title, m.library_id, m.duration_seconds
		FROM media m
		WHERE m.library_id IN (` + strings.Join(placeholders, ", ") + `)
		  AND m.trashed_at_ms IS NULL` + mediaExcludeExtrasClause + filterClause + entityClause + certClause
	// #nosec G202 -- only generated placeholders and closed filter clauses are interpolated; every value is bound.
	query := matched + `
		ORDER BY m.title COLLATE NOCASE, m.id
		LIMIT ? OFFSET ?`
	rolling := rollUp != nil && ((len(rollUp.MemberRelations) > 0 && len(rollUp.ParentRelations) > 0) || len(rollUp.DirectRelations) > 0)
	if rolling {
		var rollUpArgs []any
		query, rollUpArgs = containerRollUpSQL(matched, rollUp, placeholders, visible)
		args = append(args, rollUpArgs...)
	}
	args = append(args, limit, offset)
	rows, err := s.catalogR.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SourceItem{}
	for rows.Next() {
		var id MediaID
		var kind, title, members string
		var libraryID LibraryID
		var durationSeconds int
		dest := []any{&id, &kind, &title, &libraryID, &durationSeconds}
		if rolling {
			dest = append(dest, &members)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		item := SourceItem{ID: id, Kind: kind, Title: title, LibraryID: libraryID}
		if kind == "media" {
			item.Summary = mediaDurationSummary(durationSeconds)
		}
		if members != "" {
			for _, member := range strings.Split(members, "\x1f") {
				item.rollUpMembers = append(item.rollUpMembers, MediaID(member))
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// randomItemsSQL backs the "random" Source kind: a seeded shuffle
// rail over the visible catalog. It rides the exact reveal/cert/extras filtering
// allItemsSQL uses — the candidate set is the same set those feeds expose — and
// then orders it by a stable permutation keyed on (seed, media_id). The order is
// computed in Go (modernc.org/sqlite registers no hashing UDF) so it stays pure:
// the same seed always yields the same order, different seeds reorder, and the
// result never depends on time, rand, or row-storage order. Offset/limit are
// applied AFTER the permutation so paging a shuffle is stable for one seed.
func (s *Store) randomItemsSQL(ctx context.Context, filter LibraryFilter, seed string, limit, offset int) ([]SourceItem, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var visible []LibraryID
	for _, library := range libraries {
		if libraryVisible(library, filter) {
			visible = append(visible, library.ID)
		}
	}
	if len(visible) == 0 {
		return []SourceItem{}, nil
	}
	placeholders := make([]string, len(visible))
	args := make([]any, 0, len(visible))
	for i, id := range visible {
		placeholders[i] = "?"
		args = append(args, id)
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	args = append(args, certArgs...)
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, m.library_id, m.duration_seconds
		FROM media m
		WHERE m.library_id IN (`+strings.Join(placeholders, ", ")+`)
		  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+certClause+`
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type candidate struct {
		item SourceItem
		key  [sha256.Size]byte
	}
	candidates := []candidate{}
	for rows.Next() {
		var id MediaID
		var title string
		var libraryID LibraryID
		var durationSeconds int
		if err := rows.Scan(&id, &title, &libraryID, &durationSeconds); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate{
			item: SourceItem{
				ID:        id,
				Kind:      "media",
				Title:     title,
				LibraryID: libraryID,
				Summary:   mediaDurationSummary(durationSeconds),
			},
			// sha256("<seed>\x00<media_id>") — a stable permutation key. The NUL
			// separator keeps (seed,id) pairs unambiguous; the digest re-seeds the
			// whole order when seed changes, so two seeds shuffle independently.
			key: sha256.Sum256([]byte(seed + "\x00" + string(id))),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if c := bytes.Compare(candidates[i].key[:], candidates[j].key[:]); c != 0 {
			return c < 0
		}
		// Hash ties (astronomically unlikely) fall back to media_id so the order
		// stays total and deterministic.
		return candidates[i].item.ID < candidates[j].item.ID
	})
	if offset >= len(candidates) {
		return []SourceItem{}, nil
	}
	end := offset + limit
	if end > len(candidates) {
		end = len(candidates)
	}
	items := make([]SourceItem, 0, end-offset)
	for _, c := range candidates[offset:end] {
		items = append(items, c.item)
	}
	return items, nil
}
