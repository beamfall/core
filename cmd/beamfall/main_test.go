package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	backupcore "github.com/beamfall/core/internal/backup"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

func TestDefaultCLIHTTPClientHasTimeout(t *testing.T) {
	if defaultCLIHTTPClient.Timeout != defaultCLIHTTPTimeout {
		t.Fatalf("timeout = %v, want %v", defaultCLIHTTPClient.Timeout, defaultCLIHTTPTimeout)
	}
}

func TestOperatorLoggerHonorsConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newOperatorLogger(&buf, slog.LevelDebug)
	logger.Debug("debug-enabled")
	if !strings.Contains(buf.String(), "debug-enabled") {
		t.Fatalf("debug log was filtered: %q", buf.String())
	}
}

func TestRunHelpCommandPrintsOperatorCommands(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}} {
		var out bytes.Buffer
		if err := runWithOutput(context.Background(), args, &out); err != nil {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		if !strings.Contains(out.String(), "Usage: beamfall [command]") {
			t.Fatalf("run(%v) output = %q, want help", args, out.String())
		}
	}

	var out bytes.Buffer
	if err := helpCommand(&out); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{
		"Usage: beamfall [command]",
		"serve",
		"capabilities [--version]",
		"reset-admin-password",
		"token",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "ffmpeg-shim") {
		t.Fatalf("help output exposed internal shim:\n%s", help)
	}
}

func TestSubcommandHelpWritesUsage(t *testing.T) {
	tests := []struct {
		name string
		run  func(*bytes.Buffer) error
		want string
	}{
		{
			name: "capabilities",
			run:  func(out *bytes.Buffer) error { return capabilitiesCommand([]string{"--help"}, out) },
			want: "Usage: beamfall capabilities [--version]",
		},
		{
			name: "backup",
			run:  func(out *bytes.Buffer) error { return backup(context.Background(), []string{"--help"}, out) },
			want: "-dest",
		},
		{
			name: "scan",
			run:  func(out *bytes.Buffer) error { return scan(context.Background(), []string{"--help"}, out) },
			want: "-library-id",
		},
		{
			name: "apply-config",
			run: func(out *bytes.Buffer) error {
				return applyConfig(context.Background(), []string{"--help"}, nil, out)
			},
			want: "-file",
		},
		{
			name: "export-config",
			run:  func(out *bytes.Buffer) error { return exportConfig(context.Background(), []string{"--help"}, out) },
			want: "-server",
		},
		{
			name: "release group",
			run:  func(out *bytes.Buffer) error { return releaseCommand(context.Background(), []string{"--help"}, out) },
			want: "Usage: beamfall release <command>",
		},
		{
			name: "release leaf",
			run:  func(out *bytes.Buffer) error { return releaseVerify([]string{"--help"}, out) },
			want: "-dist",
		},
		{
			name: "reset-admin-password",
			run: func(out *bytes.Buffer) error {
				return resetAdminPassword(context.Background(), []string{"--help"}, out)
			},
			want: "-data-dir",
		},
		{
			name: "token group",
			run:  func(out *bytes.Buffer) error { return tokenCommand(context.Background(), []string{"help"}, out) },
			want: "Usage: beamfall token <command>",
		},
		{
			name: "token leaf",
			run: func(out *bytes.Buffer) error {
				return tokenCommand(context.Background(), []string{"create", "--help"}, out)
			},
			want: "-name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tt.run(&out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("help output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestServeHelpWritesUsage(t *testing.T) {
	var out bytes.Buffer
	_, err := config.LoadWithOutput([]string{"--help"}, nil, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("LoadWithOutput(--help) error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), "-data-dir") || !strings.Contains(out.String(), "-addr") {
		t.Fatalf("serve help output = %q, want serve flags", out.String())
	}
}

func TestRunCapabilitiesCommandVersionIncludesMetadata(t *testing.T) {
	var out bytes.Buffer
	if err := capabilitiesCommand([]string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"version"`) || !strings.Contains(out.String(), `"commit"`) || !strings.Contains(out.String(), `"buildTags"`) {
		t.Fatalf("version output = %s, want commit and buildTags", out.String())
	}
}

func TestRunVersionCommandIncludesSourceOffer(t *testing.T) {
	oldVersion, oldURL := capabilities.ReleaseVersion, capabilities.SourceOfferURL
	capabilities.ReleaseVersion = "v9.9.9"
	capabilities.SourceOfferURL = "https://github.com/beamfall/core/releases/download/v9.9.9/beamfall.source.json"
	t.Cleanup(func() {
		capabilities.ReleaseVersion = oldVersion
		capabilities.SourceOfferURL = oldURL
	})

	var out bytes.Buffer
	if err := versionCommand(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"version": "v9.9.9"`) || !strings.Contains(out.String(), `"correspondingSourceOfferURL"`) {
		t.Fatalf("version output = %s, want release version and source offer", out.String())
	}
	if strings.Contains(out.String(), `"capabilities"`) {
		t.Fatalf("version output = %s, want no capabilities list", out.String())
	}
}

func TestRunBackupCommandPostsBearerGraphQL(t *testing.T) {
	var gotAuth string
	var gotDest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" {
			t.Fatalf("path = %q, want /api/graphql", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "backup") {
			t.Fatalf("query = %q, want backup mutation", body.Query)
		}
		gotDest, _ = body.Variables["dest"].(string)
		_, _ = w.Write([]byte(`{"data":{"backup":{"manifest":"/tmp/beamfall-backup/manifest.json"}}}`))
	}))
	defer server.Close()
	destDir := t.TempDir()
	var out bytes.Buffer

	if err := backup(context.Background(), []string{
		"--server", server.URL,
		"--token", "secret-token",
		"--dest", destDir,
	}, &out); err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotDest != destDir {
		t.Fatalf("dest variable = %q, want %q", gotDest, destDir)
	}
	if got := strings.TrimSpace(out.String()); got != "/tmp/beamfall-backup/manifest.json" {
		t.Fatalf("backup output = %q", got)
	}
}

func TestRunBackupRequiresDestination(t *testing.T) {
	var out bytes.Buffer
	if err := backup(context.Background(), nil, &out); err == nil {
		t.Fatal("backup accepted missing --dest")
	}
}

func TestRestoreCommandRefusesServerLock(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "server.lock"), []byte("running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := restore(context.Background(), []string{
		"--manifest", filepath.Join(t.TempDir(), "manifest.json"),
		"--data-dir", dataDir,
	}, nil)
	if err == nil {
		t.Fatal("restore accepted data dir with server lock")
	}
	if !strings.Contains(err.Error(), "server appears to be running") {
		t.Fatalf("error = %q, want server lock message", err.Error())
	}
}

func TestRestoreCommandVerifiesBeforeSnapshot(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	sourceDir := t.TempDir()
	st, err := store.Open(ctx, sourceDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(sourceDir))
	result, err := st.Backup(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	escrow, err := backupcore.ReadBootstrapEscrow(sourceDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.WriteFile(result.Catalog.BackupPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	marker := filepath.Join(dataDir, "current.txt")
	if err := os.WriteFile(marker, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = restore(ctx, []string{"--manifest", result.Manifest, "--data-dir", dataDir, "--key", escrow.Key.Display()}, nil)
	if err == nil {
		t.Fatal("restore accepted a corrupted encrypted Backup Set")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("restore touched current data dir before verify: %v", statErr)
	}
}

func TestRestoreCommandSnapshotsCurrentDataDirThenRestores(t *testing.T) {
	ctx := context.Background()
	backupDir := t.TempDir()
	sourceDir := t.TempDir()
	st, err := store.Open(ctx, sourceDir, store.Options{SeedDevelopmentData: true, AllowedRoots: []string{backupDir}})
	if err != nil {
		t.Fatal(err)
	}
	st.SetSnapshotSealer(backupcore.NewDirectorySealer(sourceDir))
	result, err := st.Backup(ctx, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	escrow, err := backupcore.ReadBootstrapEscrow(sourceDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "current.txt"), []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if err := restore(ctx, []string{"--manifest", result.Manifest, "--data-dir", dataDir, "--key", escrow.Key.Display()}, &out); err != nil {
		t.Fatal(err)
	}
	snapshot := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out.String()), "restored "+dataDir+" (previous data at "), ")")
	if _, err := os.Stat(filepath.Join(snapshot, "current.txt")); err != nil {
		t.Fatalf("previous data snapshot missing marker: %v", err)
	}
	restored, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatalf("open restored data dir: %v", err)
	}
	restored.Close()
}

func TestRestoreCommandRollsBackMigrationSnapshotWithoutKey(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups", "pre-migration"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := ""
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
			manifest = filepath.Join(dataDir, "backups", "pre-migration", entry.Name(), "manifest.json")
			break
		}
	}
	if manifest == "" {
		t.Fatal("pre-migration session manifest missing")
	}
	var out bytes.Buffer
	if err := restore(ctx, []string{"--rollback-manifest", manifest, "--data-dir", dataDir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rolled back databases") {
		t.Fatalf("rollback output = %q", out.String())
	}
}

func TestRunScanCommandPostsBearerGraphQL(t *testing.T) {
	var gotAuth string
	var gotRoot string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "scanLibrary") {
			t.Fatalf("query = %q, want scanLibrary mutation", body.Query)
		}
		gotRoot, _ = body.Variables["root"].(string)
		_, _ = w.Write([]byte(`{"data":{"scanLibrary":{"id":"job-scan","status":"queued"}}}`))
	}))
	defer server.Close()
	var out bytes.Buffer

	if err := scan(context.Background(), []string{
		"--server", server.URL,
		"--token", "scan-token",
		"--library-id", "library-local",
		"--root", "/media/root",
	}, &out); err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer scan-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotRoot != "/media/root" {
		t.Fatalf("root variable = %q", gotRoot)
	}
	if !strings.Contains(out.String(), "job_id=job-scan job_status=queued") {
		t.Fatalf("scan output = %q, want queued job", out.String())
	}
}

func TestRunScanCommandCanRunOneOverHTTP(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotQuery = body.Query
		_, _ = w.Write([]byte(`{"data":{"runJob":{"id":"job-ran","status":"completed"}}}`))
	}))
	defer server.Close()
	var out bytes.Buffer

	if err := scan(context.Background(), []string{
		"--server", server.URL,
		"--token", "scan-token",
		"--run-one",
	}, &out); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(gotQuery, "runJob") {
		t.Fatalf("query = %q, want runJob mutation", gotQuery)
	}
	if !strings.Contains(out.String(), "job_status=completed") {
		t.Fatalf("run-one output = %q, want completed", out.String())
	}
}

func TestRunScanRequiresRoot(t *testing.T) {
	var out bytes.Buffer
	if err := scan(context.Background(), []string{"--library-id", "library-local"}, &out); err == nil {
		t.Fatal("scan accepted missing --root")
	}
}

func TestRunScanRequiresTokenWithoutLeakingIt(t *testing.T) {
	var out bytes.Buffer
	err := scan(context.Background(), []string{
		"--server", "http://127.0.0.1:1",
		"--token", "do-not-echo",
		"--library-id", "library-local",
		"--root", "/media/root",
	}, &out)
	if err == nil {
		t.Fatal("scan accepted unreachable server")
	}
	if strings.Contains(err.Error(), "do-not-echo") {
		t.Fatalf("error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "server is running") || !strings.Contains(err.Error(), "token is set") {
		t.Fatalf("error = %q, want server/token hint", err.Error())
	}
}

func TestResetAdminPasswordSetsAndVerifies(t *testing.T) {
	dataDir := t.TempDir()
	var out bytes.Buffer

	if err := resetAdminPassword(context.Background(), []string{
		"--data-dir", dataDir,
		"--password", "correct-horse-battery",
	}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "admin password updated") {
		t.Fatalf("output = %q, want confirmation", out.String())
	}
	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.VerifyAdminPassword(context.Background(), "correct-horse-battery"); err != nil {
		t.Fatalf("password did not verify after reset: %v", err)
	}
}

func TestResetAdminPasswordCanOverwriteExisting(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(context.Background(), "old-password", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if err := resetAdminPassword(context.Background(), []string{
		"--data-dir", dataDir,
		"--password", "new-password",
	}, nil); err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if err := st2.VerifyAdminPassword(context.Background(), "new-password"); err != nil {
		t.Fatalf("new password did not verify: %v", err)
	}
	if err := st2.VerifyAdminPassword(context.Background(), "old-password"); err == nil {
		t.Fatal("old password still verified after reset")
	}
}

func TestResetAdminPasswordRefusesServerLock(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "server.lock"), []byte("running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := resetAdminPassword(context.Background(), []string{
		"--data-dir", dataDir,
		"--password", "secret",
	}, nil)
	if err == nil {
		t.Fatal("reset-admin-password accepted data dir with server lock")
	}
	if !strings.Contains(err.Error(), "server appears to be running") {
		t.Fatalf("error = %q, want server lock message", err.Error())
	}
}

func TestResetAdminPasswordRefusesEmptyPassword(t *testing.T) {
	dataDir := t.TempDir()
	err := resetAdminPassword(context.Background(), []string{
		"--data-dir", dataDir,
		"--password", "   ",
	}, nil)
	if err == nil {
		t.Fatal("reset-admin-password accepted whitespace-only password")
	}
}

func TestRunTokenCreateBootstrapsOffline(t *testing.T) {
	dataDir := t.TempDir()
	var out bytes.Buffer

	if err := tokenCommand(context.Background(), []string{"create", "--data-dir", dataDir, "--name", "admin"}, &out); err != nil {
		t.Fatal(err)
	}
	fullToken := strings.TrimSpace(out.String())
	if fullToken == "" {
		t.Fatal("token create printed no token")
	}
	st, err := store.Open(context.Background(), dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.VerifyAPIToken(context.Background(), fullToken, time.Now().UTC()); err != nil {
		t.Fatalf("created token did not verify: %v", err)
	}
}

func TestRunTokenCreateRefusesServerLock(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "server.lock"), []byte("running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	err := tokenCommand(context.Background(), []string{"create", "--data-dir", dataDir, "--name", "admin"}, &out)

	if err == nil {
		t.Fatal("token create accepted data dir with server lock")
	}
	if !strings.Contains(err.Error(), "server appears to be running") {
		t.Fatalf("error = %q, want server lock message", err.Error())
	}
}

func TestRunTokenListPostsBearerGraphQL(t *testing.T) {
	var gotAuth string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/graphql" {
			t.Fatalf("path = %q, want /api/graphql", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotQuery = body.Query
		_, _ = w.Write([]byte(`{"data":{"apiTokens":[{"id":"tok1","name":"admin","createdAt":"2026-01-01T00:00:00Z","lastUsedAt":"","revoked":false}]}}`))
	}))
	defer server.Close()
	var out bytes.Buffer

	if err := tokenCommand(context.Background(), []string{
		"list",
		"--server", server.URL,
		"--token", "admin-token",
	}, &out); err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer admin-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if !strings.Contains(gotQuery, "apiTokens") {
		t.Fatalf("query = %q, want apiTokens query", gotQuery)
	}
	if !strings.Contains(out.String(), "id=tok1") || !strings.Contains(out.String(), `name="admin"`) {
		t.Fatalf("token list output = %q, want token row", out.String())
	}
}

func TestRunTokenRevokePostsBearerGraphQL(t *testing.T) {
	var gotAuth string
	var gotID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Query, "revokeApiToken") {
			t.Fatalf("query = %q, want revokeApiToken mutation", body.Query)
		}
		gotID, _ = body.Variables["id"].(string)
		_, _ = w.Write([]byte(`{"data":{"revokeApiToken":{"id":"tok1","name":"admin","createdAt":"2026-01-01T00:00:00Z","lastUsedAt":"","revoked":true}}}`))
	}))
	defer server.Close()
	var out bytes.Buffer

	if err := tokenCommand(context.Background(), []string{
		"revoke",
		"--server", server.URL,
		"--token", "admin-token",
		"--id", "tok1",
	}, &out); err != nil {
		t.Fatal(err)
	}

	if gotAuth != "Bearer admin-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotID != "tok1" {
		t.Fatalf("id variable = %q, want tok1", gotID)
	}
	if !strings.Contains(out.String(), "id=tok1 revoked=true") {
		t.Fatalf("token revoke output = %q, want revoked confirmation", out.String())
	}
}

func TestRunTokenListRequiresTokenWithoutLeakingIt(t *testing.T) {
	var out bytes.Buffer
	err := tokenCommand(context.Background(), []string{
		"list",
		"--server", "http://127.0.0.1:1",
		"--token", "do-not-echo",
	}, &out)
	if err == nil {
		t.Fatal("token list accepted unreachable server")
	}
	if strings.Contains(err.Error(), "do-not-echo") {
		t.Fatalf("error leaked token: %v", err)
	}
}

// serveSignalShutdownBound is the wall-clock budget a signalled serve gets to
// retire completely. It is deliberately far above the internal drain bounds:
// the assertion here is that shutdown TERMINATES, not how fast.
const serveSignalShutdownBound = 45 * time.Second

// TestServeSignalShutdownIsBoundedAndDescendantClean boots the real server
// binary out of process and proves that each of INT and TERM retires it within
// a bound, releases the bound port, and leaves no descendant process behind. In
// process assertions cannot cover this: signal delivery, the listener's release
// back to the operating system, and descendant retirement are all properties of
// the process, not of App.Run.
func TestServeSignalShutdownIsBoundedAndDescendantClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal delivery and process listing are unavailable here")
	}
	binary := buildServeSignalTestBinary(t)
	for _, delivery := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "interrupt", signal: os.Interrupt},
		{name: "terminate", signal: syscall.SIGTERM},
	} {
		t.Run(delivery.name, func(t *testing.T) {
			addr := reserveServeSignalTestAddr(t)
			process := startServeSignalTestProcess(t, binary, addr)
			waitForServeSignalTestListener(t, addr)
			descendants := serveSignalTestDescendants(t, process.Process.Pid)

			if err := process.Process.Signal(delivery.signal); err != nil {
				t.Fatalf("signal serve: %v", err)
			}
			waitForServeSignalTestExit(t, process)
			assertServeSignalTestPortReleased(t, addr)
			assertServeSignalTestDescendantsRetired(t, descendants)
		})
	}
}

func buildServeSignalTestBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "beamfall")
	// #nosec G204 -- go is fixed; the output path is this test's private temporary directory.
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beamfall: %v\n%s", err, out)
	}
	return binary
}

// reserveServeSignalTestAddr picks a free loopback port and releases it, so the
// port-release assertion after shutdown has a known address to re-bind.
func reserveServeSignalTestAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return addr
}

func startServeSignalTestProcess(t *testing.T, binary, addr string) *exec.Cmd {
	t.Helper()
	// #nosec G204 -- binary was built by this test into its own temporary directory.
	process := exec.Command(binary, "serve")
	process.Env = append(os.Environ(),
		"BEAMFALL_ADDR="+addr,
		"BEAMFALL_DATA_DIR="+t.TempDir(),
		"BEAMFALL_DISCOVERY_ENABLED=false",
	)
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	if err := process.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		if process.ProcessState != nil {
			return
		}
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	return process
}

func waitForServeSignalTestListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("serve never bound %s", addr)
}

// serveSignalTestDescendants records the children the running server owns, so
// the post-shutdown assertion names real pids rather than trusting that none
// existed.
func serveSignalTestDescendants(t *testing.T, pid int) []int {
	t.Helper()
	// #nosec G204 -- pgrep is fixed; the only argument is the pid of the child
	// process this test started, never external input.
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		// pgrep exits non-zero when the parent owns no children, which is the
		// ordinary case for a server with no active transcode.
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		child, convErr := strconv.Atoi(field)
		if convErr != nil {
			t.Fatalf("parse descendant pid %q: %v", field, convErr)
		}
		pids = append(pids, child)
	}
	return pids
}

func waitForServeSignalTestExit(t *testing.T, process *exec.Cmd) {
	t.Helper()
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("serve exited with %v, want a clean exit after the signal", err)
		}
	case <-time.After(serveSignalShutdownBound):
		t.Fatalf("serve did not exit within %v of the signal", serveSignalShutdownBound)
	}
}

func assertServeSignalTestPortReleased(t *testing.T, addr string) {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("%s still bound after shutdown: %v", addr, err)
	}
	_ = listener.Close()
}

func assertServeSignalTestDescendantsRetired(t *testing.T, pids []int) {
	t.Helper()
	for _, pid := range pids {
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := process.Signal(syscall.Signal(0)); err == nil {
			t.Fatalf("descendant pid %d survived the server's shutdown", pid)
		}
	}
}
