package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/store"
)

const reviewDemoEnrollmentSecretRef = "review-demo-enrollment-key"

// metricsScrapeSecretRef names the /metrics scrape credential inside the 0600
// secrets directory (GCR-012-H).
const metricsScrapeSecretRef = "metrics-scrape-token" //nolint:gosec // the secret's file NAME, not its value.

// provisionMetricsScrapeToken mints this run's /metrics scrape credential and
// rewrites it to <DataDir>/secrets/metrics-scrape-token. SetSecret creates the
// directory 0700 and installs the value through a 0600 sibling temp + rename,
// so the file is replaced rather than appended to and a scraper reading
// concurrently never sees a torn value.
//
// The credential is fresh on every start, so a restart rotates it: a scraper
// must re-read the file after the server restarts rather than caching the value
// across runs. That is the deliberate trade — the alternative, persisting one
// token forever, turns a single disclosure into permanent access, and the file
// a scraper already has to read is the natural place to notice the rotation.
//
// The plaintext is returned to the caller and never logged.
func provisionMetricsScrapeToken(dataDir string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("metrics scrape credential generation failed: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	files := secretFiles{dir: filepath.Join(dataDir, "secrets"), dataDir: dataDir}
	if err := files.SetSecret(metricsScrapeSecretRef, token); err != nil {
		return "", fmt.Errorf("metrics scrape credential write failed: %w", err)
	}
	return token, nil
}

// bootstrapAdminPassword applies BEAMFALL_ADMIN_PASSWORD on first run only
// (ADR-0012 §8 headless setup). It is a no-op when the env var is unset/blank
// or when an admin password already exists — so a long-lived process keeping
// the env set never rotates a password the operator later changed. The
// plaintext is never logged.
func bootstrapAdminPassword(ctx context.Context, st *store.Store, logger *slog.Logger) error {
	password, ok := os.LookupEnv("BEAMFALL_ADMIN_PASSWORD")
	if !ok || strings.TrimSpace(password) == "" {
		return nil
	}
	set, err := st.AdminPasswordSet(ctx)
	if err != nil {
		return err
	}
	if set {
		// Already configured — never silently rotate from the environment.
		return nil
	}
	if err := st.SetAdminPassword(ctx, password, time.Now().UTC()); err != nil {
		return err
	}
	logger.Info("admin password set from BEAMFALL_ADMIN_PASSWORD (first-run bootstrap)")
	return nil
}

// bootstrapSetupToken generates and installs the one-time setup token when the
// server has no owner Profile yet (ADR-0007 §2 / feature:first-run-setup-token).
// The plaintext token is a credential — it is generated once and never stored
// on disk. On subsequent starts where setup is complete, this is a no-op so the
// log stays clean on every normal restart.
func bootstrapSetupToken(ctx context.Context, st *store.Store, svc *pairing.Service) (string, error) {
	profiles, err := st.Profiles(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range profiles {
		if p.Kind == store.ProfileAdult {
			// At least one owner profile exists: setup is not in the bootstrap
			// state that requires the token. No token needed.
			return "", nil
		}
	}
	// No owner profile yet — generate a token so the operator can complete
	// first-run setup through the web UI /setup page.
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("setup token generation failed: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	svc.SetBootstrapToken(token)
	return token, nil
}

func firstRunSetupURL(scheme, listenAddr, token string) string {
	return fmt.Sprintf("%s://%s/web/setup?token=%s", scheme, setupURLHost(listenAddr), url.QueryEscape(token))
}

// firstRunSetupURLRedacted is the setup URL with the one-time bootstrap token
// value stripped — the form safe for structured logs. It mirrors the
// pairing-code rule: the secret is shown to the operator on the console, never
// carried in a persisted/shipped log line. The full tokened URL goes to stdout
// via firstRunSetupURL.
func firstRunSetupURLRedacted(scheme, listenAddr string) string {
	return fmt.Sprintf("%s://%s/web/setup?token=REDACTED", scheme, setupURLHost(listenAddr))
}

// setupURLHost normalizes a bound listen address into the host:port the operator
// dials — wildcard/unspecified hosts collapse to loopback.
func setupURLHost(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = "127.0.0.1"
		port = "8800"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func configureReviewDemoEnrollment(cfgProfileID string, dataDir string, svc *pairing.Service, logger *slog.Logger) error {
	key, err := (secretFiles{dir: filepath.Join(dataDir, "secrets")}).Secret(reviewDemoEnrollmentSecretRef)
	if err != nil {
		return fmt.Errorf("review demo enrollment key unavailable: %w", err)
	}
	if len(strings.TrimSpace(key)) < 16 {
		return fmt.Errorf("review demo enrollment key must be at least 16 characters")
	}
	if err := svc.SetDemoEnrollment(pairing.DemoEnrollment{
		Key:       key,
		ProfileID: store.ProfileID(strings.TrimSpace(cfgProfileID)),
	}); err != nil {
		return fmt.Errorf("review demo enrollment configuration invalid: %w", err)
	}
	logger.Info("review demo enrollment enabled", "profileID", strings.TrimSpace(cfgProfileID))
	return nil
}

// secretFiles resolves scraper AuthRefs to one-secret-per-file entries under
// <DataDir>/secrets/<authref> (CLAUDE.md invariant 5: secrets live in 0600
// files or platform secure stores, never plaintext config). A missing or
// over-permissive file is a clean error at use time — never a boot failure,
// because most scrapers run unauthenticated and a server without a TMDB key
// must still start.
type secretFiles struct {
	dir     string
	dataDir string
}

// Secret implements scrape.CredentialSource.
func (f secretFiles) Secret(ref string) (string, error) {
	// AuthRefs are plugin data: refuse anything that could escape the
	// secrets directory or address a hidden file.
	if ref == "" || ref != filepath.Base(ref) || strings.HasPrefix(ref, ".") {
		return "", fmt.Errorf("invalid credential ref %q", ref)
	}
	path := filepath.Join(f.dir, ref)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("credential %q unavailable: %w", ref, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("credential file %s must be a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("credential file %s must be mode 0600 — refusing group/other-accessible secret", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("credential file %s is empty", path)
	}
	return secret, nil
}

// SetSecret writes a secret to its 0600 file under the secrets directory
// (invariant #5: secrets live in secure stores / 0600 files, never plaintext
// config). It is the write half of Secret, used by the Console's
// setQBittorrentIngest mutation to persist the qBittorrent password without it
// ever touching the catalog database or a log line. An empty value clears the
// secret (removes the file), which an anonymous endpoint wants. The directory
// is created 0700 if absent.
func (f secretFiles) SetSecret(ref, value string) error {
	if ref == "" || ref != filepath.Base(ref) || strings.HasPrefix(ref, ".") {
		return fmt.Errorf("invalid credential ref %q", ref)
	}
	path := filepath.Join(f.dir, ref)
	if strings.TrimSpace(value) == "" {
		// Clear: an endpoint configured with no password must not keep a stale
		// secret. A missing file is already "no secret", so absence is success
		// and needs no directory to fsync. A real removal is made durable by
		// fsyncing the parent so the unlink survives a crash — the rewrap journal
		// clear depends on this.
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return syncDir(f.dir)
	}
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	// Durably write via a 0600 sibling temp + rename: a reader never sees a
	// partial secret, a crash never leaves a torn file, and the committed value
	// survives a crash (fsync the file before rename, the directory after). This
	// crash-consistency is what the KEK/bundle commits and the rewrap journal
	// rely on.
	tmp, err := os.CreateTemp(f.dir, ".libsecret-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := writeAndSyncSecret(tmp, value); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return syncDir(f.dir)
}

// SecretRefs inventories flat secret names for crash-resumable libencrypt ref
// migration. It never reads values; suspicious non-regular entries remain in
// the inventory so migration fails closed when it tries to inspect them.
func (f secretFiles) SecretRefs(prefix string) ([]string, error) {
	entries, err := os.ReadDir(f.dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			refs = append(refs, entry.Name())
		}
	}
	return refs, nil
}

// MoveSecret copy-verifies-publishes dst before durably deleting src. The
// expected values make a changed source or destination a fail-closed mismatch;
// repeating after a crash between publication and deletion converges safely.
func (f secretFiles) MoveSecret(src, dst, expectedSource, destination string) error {
	if src == "" || src != filepath.Base(src) || strings.HasPrefix(src, ".") ||
		dst == "" || dst != filepath.Base(dst) || strings.HasPrefix(dst, ".") || src == dst {
		return errors.New("invalid secret move refs")
	}
	if err := requireRegularSecretPath(filepath.Join(f.dir, src), false); err != nil {
		return err
	}
	if err := requireRegularSecretPath(filepath.Join(f.dir, dst), false); err != nil {
		return err
	}
	dstValue, dstErr := f.Secret(dst)
	if dstErr == nil {
		if dstValue != strings.TrimSpace(destination) {
			return fmt.Errorf("secret move destination %q mismatch", dst)
		}
	} else if !os.IsNotExist(unwrapSecretFileError(dstErr)) {
		return dstErr
	}
	srcValue, srcErr := f.Secret(src)
	if srcErr != nil {
		if dstErr == nil && os.IsNotExist(unwrapSecretFileError(srcErr)) {
			return nil
		}
		return srcErr
	}
	if srcValue != strings.TrimSpace(expectedSource) {
		return fmt.Errorf("secret move source %q changed", src)
	}
	if dstErr != nil {
		if err := f.SetSecret(dst, destination); err != nil {
			return err
		}
		verified, err := f.Secret(dst)
		if err != nil || verified != strings.TrimSpace(destination) {
			return fmt.Errorf("secret move destination %q verification failed: %w", dst, err)
		}
	}
	return f.SetSecret(src, "")
}

func requireRegularSecretPath(path string, required bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secret move path %s is not a regular file", path)
	}
	return nil
}

func unwrapSecretFileError(err error) error {
	for err != nil {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
	return nil
}

// writeAndSyncSecret writes value to tmp at 0600 and fsyncs it. The explicit
// Chmod re-asserts the mode: an O_CREATE perm is umask-masked, so a
// umask-narrowed temp could otherwise keep wider bits. Closes tmp on return.
func writeAndSyncSecret(tmp *os.File, value string) error {
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.WriteString(value); err != nil {
		return err
	}
	return tmp.Sync()
}

// syncDir fsyncs a directory so a rename/unlink of one of its entries is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

func (f secretFiles) SecretLocus(ref string) libencrypt.KeyLocusInfo {
	if ref == "" || ref != filepath.Base(ref) || strings.HasPrefix(ref, ".") {
		return libencrypt.KeyLocusInfo{Class: libencrypt.KeyLocusUnknown}
	}
	if sameVolume(f.dataDir, f.dir) {
		return libencrypt.KeyLocusInfo{
			Class:      libencrypt.KeyLocusFileSameVolume,
			Disclosure: libencrypt.KeyLocusSameVolumeDisclosure,
		}
	}
	return libencrypt.KeyLocusInfo{
		Class:      libencrypt.KeyLocusFileSeparateVolume,
		Disclosure: libencrypt.KeyLocusSeparateVolumeDisclosure,
	}
}

func sameVolume(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		aStat, aOK := aInfo.Sys().(*syscall.Stat_t)
		bStat, bOK := bInfo.Sys().(*syscall.Stat_t)
		if aOK && bOK {
			return aStat.Dev == bStat.Dev
		}
	}
	rel, err := filepath.Rel(a, b)
	return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
