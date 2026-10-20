package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coreNoGoSeam struct {
	name         string
	packages     []string
	files        []string
	appImports   []string
	serverRoutes []string
	needles      map[string][]string
}

var coreNoGoSeams = []coreNoGoSeam{
	{
		name:       "store",
		packages:   []string{"store"},
		files:      []string{"internal/store/store.go", "internal/store/sqlite.go"},
		appImports: []string{"store"},
	},
	{
		name:       "auth/session",
		packages:   []string{"auth", "streamtoken"},
		files:      []string{"internal/auth/auth.go", "internal/playback/sessions.go"},
		appImports: []string{"auth", "streamtoken"},
		serverRoutes: []string{
			"/api/enroll",
			"/api/playback-info",
		},
	},
	{
		name:       "managed file ops",
		packages:   []string{"graph", "store"},
		files:      []string{"internal/graph/graph_fileops.go", "internal/store/managed_file_ops.go"},
		appImports: []string{"graph", "store"},
	},
	{
		name:       "backup/recovery",
		packages:   []string{"backup", "store"},
		files:      []string{"internal/backup/service.go", "internal/store/backup.go", "internal/store/backup_ingest.go"},
		appImports: []string{"store"},
		needles: map[string][]string{
			"internal/app/app.go":       {"databaseBackupHandler", "BackupWithOptions", "SetBackupIngestEncryptor"},
			"internal/server/v1.go":     {"BackupIngest", "BackupIngestStatus"},
			"internal/server/server.go": {"SetBackupIngestEncryptor"},
		},
	},
	{
		name:       "offline pack substrate",
		packages:   []string{"offlinepack"},
		files:      []string{"internal/offlinepack/offlinepack.go", "internal/offlinepack/http.go", "internal/store/offline_pack.go"},
		appImports: []string{"offlinepack"},
		serverRoutes: []string{
			"/api/pack/manifest/",
			"/api/pack/content/",
			"/api/pack/key/",
		},
	},
	{
		name:       "moderation/App Store safety substrate",
		packages:   []string{"moderation"},
		files:      []string{"internal/moderation/moderation.go", "internal/graph/graph_capability_appstore.go"},
		appImports: []string{"moderation"},
		serverRoutes: []string{
			"/api/moderation/report",
			"/api/moderation/block",
			"/api/moderation/blocks",
			"/api/moderation/contact",
		},
	},
	{
		name:       "capability reporting",
		packages:   []string{"capabilities"},
		files:      []string{"internal/capabilities/capabilities.go", "internal/capabilities/registry.go"},
		appImports: []string{"capabilities"},
		needles: map[string][]string{
			"internal/app/app.go": {"SetCapabilities", "CurrentWithOverrides"},
		},
	},
	{
		name:       "Integration Host",
		packages:   []string{"integrationhost"},
		files:      []string{"internal/integrationhost/hostapi.go", "internal/integrationhost/supervisor.go", "internal/app/integration_worker.go"},
		appImports: []string{"integrationhost"},
		needles: map[string][]string{
			"internal/app/app.go": {"IntegrationLifecycle:         integrationController", "buildIntegrationManager"},
		},
	},
	{
		name:       "scan/scrape/identify engines",
		packages:   []string{"scan", "scrape", "identify"},
		files:      []string{"internal/scan/scan.go", "internal/scrape/scrape.go", "internal/identify/pipeline.go"},
		appImports: []string{"scan", "scrape", "identify"},
		needles: map[string][]string{
			"internal/app/app.go": {"scrape.New", "identify.NewService", "scanpkg.NewJob"},
		},
	},
	{
		name:       "FFmpeg playback engine",
		packages:   []string{"ffmpeg", "playback", "mediahttp"},
		files:      []string{"internal/ffmpeg/ffmpeg.go", "internal/playback/playback.go", "internal/mediahttp/mediahttp.go"},
		appImports: []string{"ffmpeg", "playback", "mediahttp"},
		serverRoutes: []string{
			"/api/media/direct/",
			"/api/media/hls/",
			"/api/media/subtitle/",
			"/api/playback-info",
		},
	},
}

func TestCoreNoGoSeamsStayCore(t *testing.T) {
	root := repoRoot(t)
	for _, seam := range coreNoGoSeams {
		t.Run(seam.name, func(t *testing.T) {
			for _, pkg := range seam.packages {
				dir := filepath.Join(root, "internal", pkg)
				if !hasGoFiles(t, dir) {
					t.Fatalf("internal/%s is missing Go files; %s moved out of Core", pkg, seam.name)
				}
				if strings.HasPrefix(filepath.ToSlash(filepath.Join("internal", pkg)), "internal/plugin/") {
					t.Fatalf("internal/%s is behind plugin activation; %s must remain Core", pkg, seam.name)
				}
				if _, ok := archWhitelist[pkg]; !ok {
					if _, ok := archAnythingExcept[pkg]; !ok {
						t.Fatalf("internal/%s has no arch row; %s is no longer a first-class Core package", pkg, seam.name)
					}
				}
			}
			for _, rel := range seam.files {
				assertCoreFile(t, root, seam.name, rel)
			}
		})
	}
}

func TestNoPluginAppKeepsCoreNoGoSeamsWired(t *testing.T) {
	root := repoRoot(t)
	appImports := packageImports(t, filepath.Join(root, "internal", "app"))
	serverSource := readText(t, filepath.Join(root, "internal", "server", "server.go"))
	for _, seam := range coreNoGoSeams {
		t.Run(seam.name, func(t *testing.T) {
			for _, pkg := range seam.appImports {
				if !appImports[modulePrefix+pkg] {
					t.Fatalf("internal/app no longer imports internal/%s; no-plugin app can lose %s", pkg, seam.name)
				}
			}
			for _, route := range seam.serverRoutes {
				if !strings.Contains(serverSource, `"`+route+`"`) {
					t.Fatalf("server.New no longer always mounts %s; no-plugin app can lose %s", route, seam.name)
				}
			}
			for rel, needles := range seam.needles {
				source := readText(t, filepath.Join(root, filepath.FromSlash(rel)))
				for _, needle := range needles {
					if !strings.Contains(source, needle) {
						t.Fatalf("%s missing %q; no-plugin app can lose %s", rel, needle, seam.name)
					}
				}
			}
		})
	}
}

func assertCoreFile(t *testing.T, root, seam, rel string) {
	t.Helper()
	clean := filepath.ToSlash(rel)
	if strings.HasPrefix(clean, "internal/plugin/") {
		t.Fatalf("%s moved behind plugin activation at %s", seam, rel)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("%s requires %s: %v", seam, rel, err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
