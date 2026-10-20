package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Settings-taxonomy forbidden-settings guard. The full
// per-surface category conformance (omit-never-rename over the §2 vocabulary,
// plus the profile-key registry) is added by SETTINGS-1; this guard enforces
// the machine-checkable slice of the §3 forbidden-settings list — the shapes a
// setting must never take on any surface — and gives the two JFSET-5 additions
// (external-FFmpeg-path override, entry 10; raw custom CSS, entry 11) teeth.
//
// Each forbidden shape is a regexp over a setting *identifier* (a config env
// name, a Console field name, or a profile-pref key). A match is a contract
// violation, closed with the cited source. The patterns are intentionally
// narrow to the forbidden identifiers, not the surrounding implementation, so
// the hardened FFmpeg invocation in internal/ffmpeg/hardening.go (which is the
// sanctioned bundled path, not a settable override) is never itself flagged.
type forbiddenSetting struct {
	name     string
	citation string
	pattern  *regexp.Regexp
}

var forbiddenSettings = []forbiddenSetting{
	{
		// ADR-0172 §3.10 — arbitrary-binary execution defeats the hardened
		// bundle (internal/ffmpeg/hardening.go, ADR-0012 §10).
		name:     "external/custom FFmpeg or ffprobe binary-path override",
		citation: "ADR-0172 §3.10 (internal/ffmpeg/hardening.go · ADR-0012 §10)",
		pattern:  regexp.MustCompile(`(?i)(ffmpeg|ffprobe).*(path|binary|bin|exe|location|override)`),
	},
	{
		// ADR-0172 §3.11 — stored-XSS + design-system governance; §4.3 Skins
		// is the sole sanctioned appearance mechanism.
		name:     "raw operator/user custom CSS or style/HTML injection",
		citation: "ADR-0172 §3.11 (ADR-0172 §4.3 Skins)",
		pattern:  regexp.MustCompile(`(?i)(custom.?css|raw.?css|css.?override|custom.?stylesheet|inject.*(css|style|html))`),
	},
}

// classifyForbiddenSetting returns the citation for the first forbidden shape a
// setting identifier matches, or "" when the identifier is permitted.
func classifyForbiddenSetting(identifier string) string {
	for _, forbidden := range forbiddenSettings {
		if forbidden.pattern.MatchString(identifier) {
			return forbidden.citation
		}
	}
	return ""
}

// TestSettingsTaxonomyForbiddenClassifier is the acceptance guard for JFSET-5:
// the taxonomy guard rejects an external-FFmpeg-path setting and a raw-CSS
// setting, while leaving legitimate settings alone.
func TestSettingsTaxonomyForbiddenClassifier(t *testing.T) {
	forbidden := []string{
		"transcoding-playback.ffmpeg_binary_path",
		"BEAMFALL_FFMPEG_PATH",
		"transcoding-playback.custom_ffprobe_location",
		"appearance.custom_css",
		"operator.customStylesheet",
		"appearance.raw_css_override",
	}
	for _, identifier := range forbidden {
		if classifyForbiddenSetting(identifier) == "" {
			t.Errorf("forbidden setting %q was accepted; ADR-0172 §3 requires rejection", identifier)
		}
	}

	permitted := []string{
		"appearance.theme",
		"appearance.skin_id",
		"playback.default_quality",
		"transcoding-playback.hardware_accel",
		"security-app-lock.require_on_open",
	}
	for _, identifier := range permitted {
		if citation := classifyForbiddenSetting(identifier); citation != "" {
			t.Errorf("legitimate setting %q was rejected as %s", identifier, citation)
		}
	}
}

// TestSettingsTaxonomyConfigEnvSurface gives the guard teeth against future
// additions: no operator config env var may select an FFmpeg binary path or
// inject CSS. It scans the one settings surface that exists today — the
// BEAMFALL_* env names parsed in internal/config/config.go.
func TestSettingsTaxonomyConfigEnvSurface(t *testing.T) {
	configPath := filepath.Join(requireRoot(t, "internal"), "config", "config.go")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.go: %v", err)
	}
	envName := regexp.MustCompile(`BEAMFALL_[A-Z0-9_]+`)
	for _, name := range envName.FindAllString(string(data), -1) {
		if citation := classifyForbiddenSetting(name); citation != "" {
			t.Errorf("config env %q is a forbidden setting: %s", name, citation)
		}
	}
}
