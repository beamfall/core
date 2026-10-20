package graph

import (
	"context"

	"github.com/beamfall/core/internal/auth"
)

// Opt-in update awareness. The graph half of the version-check
// feature: an Admin-Session-gated (Console operation class) read that reports
// whether a newer server version is available on the operator's chosen release
// channel, so the Console can render an update banner.
//
// HARD CONSTRAINT — invariant #5, NO telemetry: the underlying check is a plain
// GET of a public version manifest. It sends NOTHING about this server. It is
// also default-off: until the operator opts in (consoleConfig.updateCheckEnabled,
// set in the setup wizard's final step) the resolver makes no outbound request
// and reports enabled=false.

// Wire tokens for UpdateStatusInfo.State. They mirror updatecheck.State's
// tokens exactly; the composition-root adapter is what maps one onto the other,
// so this package keeps no dependency on the checker implementation.
const (
	// UpdateStateUnavailable means the check produced no trustworthy answer:
	// no checker wired, or the outbound read failed / was refused. It is NOT a
	// statement about the running version, and it is the fail-closed default.
	UpdateStateUnavailable = "unavailable"
	// UpdateStateDisabled means the operator has not opted in, so no outbound
	// request was made (default-off, invariant #5).
	UpdateStateDisabled = "disabled"
	// UpdateStateUpToDate means a verified manifest was read and the running
	// version is current. This is the ONLY token that may be rendered as such.
	UpdateStateUpToDate = "upToDate"
	// UpdateStateUpdateAvailable means a verified manifest named a strictly
	// newer version than the running one.
	UpdateStateUpdateAvailable = "updateAvailable"
)

// UpdateStatusInfo is the wire shape of the version-check result.
// It carries only the opt-in config projection plus, when enabled, the fetched
// latest version — never any library or server-identity data.
type UpdateStatusInfo struct {
	// State is the coarse outcome of the check and the only field a client may
	// route the "you are current" answer through. An empty LatestVersion with
	// UpdateAvailable=false is produced by BOTH a current server and a failed
	// check; State is what separates them. Fail-closed: anything other than a
	// successful check reports a token that makes no version claim.
	State string `json:"state"`
	// Enabled mirrors the opt-in config. When false, no outbound request was
	// made.
	Enabled bool `json:"enabled"`
	// Channel is the release channel the check reads ("stable" or "beta").
	Channel string `json:"channel"`
	// CurrentVersion is the running server version.
	CurrentVersion string `json:"currentVersion"`
	// LatestVersion is the latest published version on Channel; empty when the
	// check is disabled or the fetch failed.
	LatestVersion string `json:"latestVersion"`
	// UpdateAvailable is true only when a strictly newer version was fetched.
	UpdateAvailable bool `json:"updateAvailable"`
}

// updateStatus reports the opt-in version-check result (Admin-Session-gated in
// execute). It reads the stored opt-in flag + channel, then — only when enabled
// — delegates to the wired UpdateChecker for the outbound manifest read. A
// fetch failure is not surfaced as a query error, so a transient network
// problem never breaks the Console status read — but it reports
// State=unavailable, which is a DISTINCT answer from "you are current".
func (h *Handler) updateStatus(ctx context.Context, _ auth.Scope) (UpdateStatusInfo, error) {
	cfg, err := h.store.ConsoleConfig(ctx)
	if err != nil {
		return UpdateStatusInfo{}, err
	}
	current := h.serverVersion
	if current == "" {
		current = serverVersionString
	}
	info := UpdateStatusInfo{
		State:          UpdateStateUnavailable,
		Enabled:        cfg.UpdateCheckEnabled,
		Channel:        cfg.UpdateCheckChannel,
		CurrentVersion: current,
	}
	if !cfg.UpdateCheckEnabled {
		// Default-off: no outbound request, and no claim about the version.
		info.State = UpdateStateDisabled
		return info, nil
	}
	if h.updateChecker == nil {
		// Opted in but nothing wired: the check cannot answer.
		return info, nil
	}
	result, err := h.updateChecker.Check(ctx, true, cfg.UpdateCheckChannel, current)
	if err != nil {
		return info, nil
	}
	if result.State == "" {
		// Fail-closed: an adapter that omitted the state never gets read as a
		// reassuring answer.
		result.State = UpdateStateUnavailable
	}
	return result, nil
}
