package migrate

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/store"
)

var foreignDBDialects = map[string]store.ForeignDBDialect{
	"filmtv\x00plex":     store.ForeignDBPlex,
	"filmtv\x00emby":     store.ForeignDBEmby,
	"filmtv\x00jellyfin": store.ForeignDBJellyfin,
}

func checkForeignDBCompatibility(ctx context.Context, payload Payload) error {
	dialect, ok := foreignDBDialects[payload.PluginID+"\x00"+payload.ImporterID]
	if !ok {
		return nil
	}
	if strings.TrimSpace(payload.SourcePath) == "" {
		return nil
	}
	return store.CheckCertifiedForeignDBSchema(ctx, dialect, payload.SourcePath)
}
