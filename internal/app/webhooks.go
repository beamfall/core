package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/webhook"
)

// buildWebhookManager wires the ADR-0040 §2.5 Webhook Emitter Integration
// Plugin behind its console management surface. It always runs: with no
// endpoints it is idle, and endpoints the owner adds in the console start
// without a restart. Config is plugin-private JSON at
// <DataDir>/webhook-endpoints.json; secrets (HMAC keys, chat tokens, SMTP
// passwords) live in 0600 files under <DataDir>/secrets (invariant #4) and
// never in the JSON. Cursors, the dead-letter and the delivery log are
// plugin-private JSON at <DataDir>/webhook-dead-letter.json — no core schema.
func buildWebhookManager(cfg config.Config, st *store.Store, art *artwork.Service, crypto *libencrypt.Manager, logger *slog.Logger) (*webhook.Manager, error) {
	sealed := func(id store.LibraryID) bool {
		return crypto.Provisioned(string(id)) && crypto.Sealed(string(id))
	}
	manager, err := webhook.OpenManager(webhook.ManagerOptions{
		ConfigPath: filepath.Join(cfg.DataDir, "webhook-endpoints.json"),
		StatePath:  filepath.Join(cfg.DataDir, "webhook-dead-letter.json"),
		Secrets:    secretFiles{dir: filepath.Join(cfg.DataDir, "secrets"), dataDir: cfg.DataDir},
		Events:     st,
		Derive: webhook.DeriveOptions{
			Catalog: st,
			Sealed:  sealed,
			Artwork: webhookArtwork{store: st, artwork: art},
		},
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("webhook emitter: %w", err)
	}
	return manager, nil
}

// webhookArtworkBucket is the poster derivative attached to chat messages:
// large enough for an embed, small enough to stay under upload limits.
const webhookArtworkBucket = "w640"

// webhookArtworkLimit bounds the bytes read for one attachment; the emitter
// drops anything over its own smaller cap.
const webhookArtworkLimit = 8 << 20

// webhookArtwork loads a Media's poster derivative for formats that attach
// the image. Encrypted Libraries never attach artwork: their files are
// ciphertext on disk, and a notification leaving the server is no place to
// decrypt them. The emitter has already refused sealed Libraries.
type webhookArtwork struct {
	store   *store.Store
	artwork *artwork.Service
}

func (w webhookArtwork) Artwork(ctx context.Context, id store.MediaID) (*webhook.Artwork, error) {
	media, err := w.store.Media(ctx, id, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		return nil, err
	}
	encrypted, err := w.store.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil || encrypted {
		return nil, err
	}
	served, ok := w.artwork.ResolveSlot(id, artwork.SlotPoster)
	if !ok {
		return nil, nil
	}
	path, err := w.artwork.ArtworkDerivative(ctx, id, served, webhookArtworkBucket)
	if err != nil {
		return nil, err
	}
	// Read through an os.Root so a link swapped into the artwork tree cannot
	// point the read outside its directory.
	file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, webhookArtworkLimit+1))
	if err != nil || len(data) == 0 || len(data) > webhookArtworkLimit {
		return nil, err
	}
	return &webhook.Artwork{Data: data, ContentType: http.DetectContentType(data)}, nil
}
