package app

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/proxytee"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// feedStreamRegistrar adapts the proxy-tee to playback's origin-stream seam,
// so playback never imports the proxy (CORE-2954). Private feed credentials
// are resolved from the secure store and scoped to the feed's schemeful site
// (CORE-3001). The archive path is chosen by the proxy beneath its own root.
type feedStreamRegistrar struct {
	tee     *proxytee.Service
	secrets scrape.CredentialSource
}

func (r feedStreamRegistrar) RegisterFeedStream(ctx context.Context, stream playback.FeedStream) (store.MediaID, error) {
	authHeader := ""
	if stream.AuthRef != "" {
		if r.secrets == nil {
			return "", errors.New("subscription credentials unavailable")
		}
		secret, err := r.secrets.Secret(stream.AuthRef)
		if err != nil {
			return "", err
		}
		if scrape.SameSite(stream.FeedURL, stream.OriginURL) {
			authHeader = secret
		}
	}
	return r.tee.EnsureRegistered(ctx, proxytee.Registration{
		LibraryID:  stream.LibraryID,
		GUID:       stream.Key,
		Title:      stream.Title,
		Container:  stream.Container,
		Codec:      stream.Codec,
		Class:      stream.Class,
		OriginURL:  stream.OriginURL,
		FeedURL:    stream.FeedURL,
		AuthHeader: authHeader,
	})
}
