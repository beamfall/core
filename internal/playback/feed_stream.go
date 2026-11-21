package playback

import (
	"context"
	"mime"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// Reasons a catalogued feed entry without a playable file cannot stream.
const (
	// ReasonFeedStreamRequiresDownload: the entry's feed needs credentials, and
	// credentialed origins are not proxied; the entry plays once downloaded.
	ReasonFeedStreamRequiresDownload = "feed-stream-requires-download"
	// ReasonFeedStreamUnavailable: the origin stream could not be registered.
	ReasonFeedStreamUnavailable = "feed-stream-unavailable"
)

// FeedStream describes the origin enclosure of a catalogued feed entry that
// has no playable file. OriginURL is a server-only secret: it never reaches a
// client, a log line or an error string.
type FeedStream struct {
	LibraryID store.LibraryID
	// Key is the registration identity, store.SubscriptionEntryStreamKey; the
	// registered Media ID is store.ProvisionalMediaID(LibraryID, Key).
	Key       string
	Title     string
	Container string
	Codec     string
	Class     store.MediaClass
	OriginURL string
}

// FeedStreamRegistrar registers a server-side origin stream for a file-less
// Media and returns that Media's ID; the stream is then served at
// /api/media/proxy/<token> to a TargetMediaProxy Stream Token. It is
// idempotent per Key, and re-registers after a restart empties its registry.
// Interface at the consumer: composition adapts *proxytee.Service.
type FeedStreamRegistrar interface {
	RegisterFeedStream(ctx context.Context, stream FeedStream) (store.MediaID, error)
}

// feedEntryLookup resolves the catalogued feed entry a Media represents.
type feedEntryLookup interface {
	SubscriptionEntryForMedia(ctx context.Context, mediaID store.MediaID) (store.SubscriptionEntry, store.Subscription, bool, error)
}

// SetFeedStreams wires the origin-stream registrar so a catalogued feed entry
// without a playable file plays through the proxy. Optional; nil (the
// default) leaves such a Media unavailable.
func (s *Service) SetFeedStreams(registrar FeedStreamRegistrar) {
	s.feedStreams = registrar
	if s.feedEntries == nil && s.store != nil {
		s.feedEntries = s.store
	}
}

// playableFiles drops provisional placeholders: their bytes are an in-progress
// proxy copy, which plays only through the proxy stream.
func playableFiles(files []store.MediaFile) []store.MediaFile {
	kept := files[:0:0]
	for _, file := range files {
		if !store.IsProvisionalMediaFile(file) {
			kept = append(kept, file)
		}
	}
	return kept
}

// feedStreamDecision plays a file-less catalogued feed entry through the
// proxy. ok is false when media is not such an entry, so the caller keeps its
// own unavailable verdict.
func (s *Service) feedStreamDecision(ctx context.Context, scope auth.Scope, media store.Media, suffix string, timeline Timeline, profile CapabilityProfile, priority Priority, ov quirkOverlay) (dec decision, ok bool, err error) {
	if s.feedStreams == nil || s.feedEntries == nil {
		return decision{}, false, nil
	}
	entry, sub, found, lookupErr := s.feedEntries.SubscriptionEntryForMedia(ctx, media.ID)
	if lookupErr != nil || !found || entry.EnclosureURL == "" {
		return decision{}, false, nil
	}
	unavailable := func(reason string) (decision, bool, error) {
		return decision{media: media, verdict: "unavailable", reason: reason, streams: []StreamChoice{}, timeline: timeline}, true, nil
	}
	if sub.AuthRef != "" {
		return unavailable(ReasonFeedStreamRequiresDownload)
	}
	class := media.Class
	if class == "" {
		class = store.MediaClassAudio
	}
	container, codec := media.Container, media.Codec
	if container == "" || codec == "" {
		container, codec = enclosureFormat(entry.EnclosureType)
	}
	registered, regErr := s.feedStreams.RegisterFeedStream(ctx, FeedStream{
		LibraryID: sub.LibraryID,
		Key:       store.SubscriptionEntryStreamKey(sub.ID, entry.GUID),
		Title:     media.Title,
		Container: container,
		Codec:     codec,
		Class:     class,
		OriginURL: entry.EnclosureURL,
	})
	// A registration bound to another Media would front bytes this grant does
	// not name; refuse rather than mint a token for it.
	if regErr != nil || registered != media.ID {
		return unavailable(ReasonFeedStreamUnavailable)
	}
	restricted, err := s.decisionRestricted(ctx, media.LibraryID)
	if err != nil {
		return decision{}, false, err
	}
	expiresAt := s.now().Add(max(time.Hour, 2*time.Duration(media.DurationSeconds)*time.Second)).UTC()
	receiverBound := isReceiverCapabilityProfile(profile)
	token, err := s.issueDecisionGrant(ctx, streamtoken.Grant{
		MediaID:                 media.ID,
		DeviceID:                scope.DeviceID,
		SessionID:               scope.SessionID,
		ProfileID:               scope.ProfileID,
		Restricted:              restricted,
		ProfileCapability:       CapabilityFingerprint(profile),
		ReceiverBound:           receiverBound,
		ReceiverProfileResolved: receiverBound && ov.matched,
		Foreground:              priority == PriorityForeground,
		ExpiresAt:               expiresAt,
		Target:                  streamtoken.TargetMediaProxy,
	})
	if err != nil {
		return decision{}, false, err
	}
	return decision{
		media:   media,
		verdict: "direct-play",
		streams: []StreamChoice{{
			ID:        "direct-" + suffix,
			Kind:      "direct-play",
			URL:       "/api/media/proxy/" + token,
			Token:     token,
			ExpiresAt: expiresAt.Format(time.RFC3339),
		}},
		timeline: timeline,
	}, true, nil
}

// enclosureFormat maps an enclosure MIME type to a container and codec; ""
// lets the provisional writer apply its audio default.
func enclosureFormat(mimeType string) (container, codec string) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(mimeType))
	if err != nil {
		return "", ""
	}
	switch mediaType {
	case "audio/mpeg", "audio/mp3", "audio/mpeg3", "audio/x-mpeg", "audio/x-mp3":
		return "mp3", "mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac", "audio/x-aac", "audio/aacp":
		return "m4a", "aac"
	case "audio/ogg", "audio/vorbis":
		return "ogg", "vorbis"
	case "audio/opus":
		return "ogg", "opus"
	case "audio/flac", "audio/x-flac":
		return "flac", "flac"
	case "video/mp4", "video/x-m4v":
		return "mp4", "h264"
	case "video/quicktime":
		return "mov", "h264"
	}
	return "", ""
}
