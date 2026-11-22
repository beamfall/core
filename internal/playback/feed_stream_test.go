package playback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

const testEnclosureURL = "https://cdn.example.test/enclosure.mp3?signature=private-signature"

type fakeFeedEntries struct {
	entry store.SubscriptionEntry
	sub   store.Subscription
	found bool
}

func (f fakeFeedEntries) SubscriptionEntryForMedia(context.Context, store.MediaID) (store.SubscriptionEntry, store.Subscription, bool, error) {
	return f.entry, f.sub, f.found, nil
}

type fakeFeedRegistrar struct {
	mediaID store.MediaID
	err     error
	calls   []FeedStream
}

func (f *fakeFeedRegistrar) RegisterFeedStream(_ context.Context, stream FeedStream) (store.MediaID, error) {
	f.calls = append(f.calls, stream)
	return f.mediaID, f.err
}

func feedEntryFor(authRef string) fakeFeedEntries {
	return fakeFeedEntries{
		entry: store.SubscriptionEntry{SubscriptionID: "subscription-1", GUID: "entry-guid-1", EnclosureURL: testEnclosureURL, EnclosureType: "audio/mpeg"},
		sub:   store.Subscription{ID: "subscription-1", LibraryID: "library-local", AuthRef: authRef},
		found: true,
	}
}

func audioProfile() CapabilityProfile {
	return CapabilityProfile{Containers: []string{"mp3", "mp4"}, DirectCodecs: []string{"mp3", "h264"}}
}

func TestInfoStreamsFilelessPublicFeedEntryThroughProxy(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	registrar := &fakeFeedRegistrar{mediaID: "media-alpha"}
	service.feedEntries = feedEntryFor("")
	service.SetFeedStreams(registrar)

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: audioProfile()})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" || len(resp.Streams) != 1 {
		t.Fatalf("Decision = %q with %d streams, want one direct-play stream", resp.Decision, len(resp.Streams))
	}
	stream := resp.Streams[0]
	if !strings.HasPrefix(stream.URL, "/api/media/proxy/") || stream.URL != "/api/media/proxy/"+stream.Token {
		t.Fatalf("stream URL = %q, want /api/media/proxy/<token>", stream.URL)
	}
	grant, err := service.tokens.Verify(stream.Token, streamtoken.TargetMediaProxy, scope)
	if err != nil || grant.MediaID != "media-alpha" || grant.SessionID != scope.SessionID {
		t.Fatalf("proxy token grant = %+v, %v", grant, err)
	}
	if len(registrar.calls) != 1 {
		t.Fatalf("registrar calls = %d, want 1", len(registrar.calls))
	}
	got := registrar.calls[0]
	if got.Key != store.SubscriptionEntryStreamKey("subscription-1", "entry-guid-1") || got.LibraryID != "library-local" || got.OriginURL != testEnclosureURL || got.Container == "" || got.Codec == "" {
		t.Fatalf("registered stream = %+v", got)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cdn.example.test") || strings.Contains(string(raw), "private-signature") {
		t.Fatalf("response leaks the enclosure URL: %s", raw)
	}
}

func TestInfoKeepsPrivateFeedEntryUnavailableUntilDownloaded(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	registrar := &fakeFeedRegistrar{mediaID: "media-alpha"}
	service.feedEntries = feedEntryFor("subscription-auth-1")
	service.SetFeedStreams(registrar)

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: audioProfile()})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "unavailable" || resp.Reason != ReasonFeedStreamRequiresDownload || len(resp.Streams) != 0 {
		t.Fatalf("Decision = %q reason %q streams %d, want unavailable %q", resp.Decision, resp.Reason, len(resp.Streams), ReasonFeedStreamRequiresDownload)
	}
	if len(registrar.calls) != 0 {
		t.Fatalf("private feed registered an origin stream: %+v", registrar.calls)
	}
}

func TestInfoReportsFeedStreamUnavailableWhenRegistrationFails(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	service.feedEntries = feedEntryFor("")
	service.SetFeedStreams(&fakeFeedRegistrar{err: errors.New("origin refused")})

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: audioProfile()})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "unavailable" || resp.Reason != ReasonFeedStreamUnavailable {
		t.Fatalf("Decision = %q reason %q, want unavailable %q", resp.Decision, resp.Reason, ReasonFeedStreamUnavailable)
	}
}

func TestInfoKeepsDirectPathForDownloadedFeedEntry(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	mediaID := seedAudio(t, service, "fingerprint-downloaded-entry")
	registrar := &fakeFeedRegistrar{mediaID: mediaID}
	service.feedEntries = feedEntryFor("")
	service.SetFeedStreams(registrar)

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: audioProfile()})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" || len(resp.Streams) == 0 || !strings.HasPrefix(resp.Streams[0].URL, "/api/media/direct/") {
		t.Fatalf("Decision = %q streams %+v, want the unchanged direct path", resp.Decision, resp.Streams)
	}
	if len(registrar.calls) != 0 {
		t.Fatalf("downloaded entry registered an origin stream: %+v", registrar.calls)
	}
}

// A provisional placeholder is an in-progress proxy copy, not a playable file:
// the entry keeps streaming through the proxy.
func TestInfoStreamsFeedEntryWithOnlyProvisionalFileThroughProxy(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	key := store.SubscriptionEntryStreamKey("subscription-1", "entry-guid-1")
	mediaID, err := service.store.CreateProvisionalMedia(context.Background(), store.ProvisionalMedia{
		LibraryID: "library-local", GUID: key, Title: "Provisional", TeePath: t.TempDir() + "/entry.mp3.partial",
	})
	if err != nil {
		t.Fatalf("CreateProvisionalMedia: %v", err)
	}
	service.feedEntries = feedEntryFor("")
	service.SetFeedStreams(&fakeFeedRegistrar{mediaID: mediaID})

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: audioProfile()})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" || len(resp.Streams) != 1 || !strings.HasPrefix(resp.Streams[0].URL, "/api/media/proxy/") {
		t.Fatalf("Decision = %q streams %+v, want the proxy stream", resp.Decision, resp.Streams)
	}
}
