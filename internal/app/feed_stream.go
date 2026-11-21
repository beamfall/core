package app

import (
	"context"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/proxytee"
	"github.com/beamfall/core/internal/store"
)

// feedStreamRegistrar adapts the proxy-tee to playback's origin-stream seam,
// so playback never imports the proxy (CORE-2954). The archive path is chosen
// by the proxy beneath its own root.
type feedStreamRegistrar struct {
	tee *proxytee.Service
}

func (r feedStreamRegistrar) RegisterFeedStream(ctx context.Context, stream playback.FeedStream) (store.MediaID, error) {
	return r.tee.EnsureRegistered(ctx, proxytee.Registration{
		LibraryID: stream.LibraryID,
		GUID:      stream.Key,
		Title:     stream.Title,
		Container: stream.Container,
		Codec:     stream.Codec,
		Class:     stream.Class,
		OriginURL: stream.OriginURL,
	})
}
