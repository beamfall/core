//go:build unix

package playback

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// resumeTranscoder writes init.mp4 and the start segment for every encoder it
// starts, then idles like an encoder waiting on its reader.
func resumeTranscoder(t *testing.T) *scriptedTranscoder {
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), mediaSegment())
		return []string{"sleep", "60"}
	}
	return transcoder
}

func startsOf(transcoder *scriptedTranscoder) []int {
	transcoder.mu.Lock()
	defer transcoder.mu.Unlock()
	return append([]int(nil), transcoder.starts...)
}

func requireStarts(t *testing.T, transcoder *scriptedTranscoder, want ...int) {
	t.Helper()
	got := startsOf(transcoder)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
}

// TestResumedSessionStartsItsEncoderAtTheResumeSegment proves a session whose
// playback info resumed the viewer at 41s starts its only encoder at the
// segment holding 41s when the player asks for init.mp4, and the player's
// first segment request then reuses that encoder instead of spawning another.
func TestResumedSessionStartsItsEncoderAtTheResumeSegment(t *testing.T) {
	t.Parallel()
	transcoder := resumeTranscoder(t)
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	key := SessionKey{SessionID: "session-resume", MediaID: "media-resume"}

	sessions.NoteResumePosition(key, 41)
	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatalf("init.mp4: %v", err)
	}
	if _, err := filePath720(sessions, key, "720p/seg00010.m4s"); err != nil {
		t.Fatalf("seg00010.m4s: %v", err)
	}
	requireStarts(t, transcoder, 10)
}

// TestInitAfterSegmentReusesTheRunningEncoder proves an init.mp4 request on
// the rung an encoder is already producing waits for that encoder's init.mp4
// instead of restarting it at segment 0.
func TestInitAfterSegmentReusesTheRunningEncoder(t *testing.T) {
	t.Parallel()
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", startSegment), mediaSegment())
		return []string{"sh", "-c", `sleep 0.2; printf init > "$1"/init.mp4; exec sleep 60`, "sh", dir}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	key := SessionKey{SessionID: "session-seek", MediaID: "media-seek"}

	if _, err := filePath720(sessions, key, "720p/seg00007.m4s"); err != nil {
		t.Fatalf("seg00007.m4s: %v", err)
	}
	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatalf("init.mp4: %v", err)
	}
	requireStarts(t, transcoder, 7)
}

// TestResumeHintIsClearedExpiredOrPastTheEnd proves the cases where init.mp4
// starts at segment 0: a later position of 0 cleared the hint, the hint went
// stale before the session was created, or it lies past the source's end.
func TestResumeHintIsClearedExpiredOrPastTheEnd(t *testing.T) {
	t.Parallel()
	for name, note := range map[string]func(*Sessions, SessionKey, func(time.Duration)){
		"cleared": func(s *Sessions, key SessionKey, _ func(time.Duration)) {
			s.NoteResumePosition(key, 41)
			s.NoteResumePosition(key, 0)
		},
		"expired": func(s *Sessions, key SessionKey, advance func(time.Duration)) {
			s.NoteResumePosition(key, 41)
			advance(sessionIdleTimeout)
		},
		"past the end": func(s *Sessions, key SessionKey, _ func(time.Duration)) {
			s.NoteResumePosition(key, 500)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			now := time.Unix(1_800_000_000, 0)
			clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
			advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
			transcoder := resumeTranscoder(t)
			sessions := NewSessions(transcoder, t.TempDir(), clock)
			t.Cleanup(sessions.Close)
			key := SessionKey{SessionID: store.SessionID("session-" + name), MediaID: "media-hint"}

			note(sessions, key, advance)
			if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
				t.Fatalf("init.mp4: %v", err)
			}
			requireStarts(t, transcoder, 0)
		})
	}
}

// TestResumeHintAfterEncoderStartIsIgnored proves a position noted once the
// session's encoder is running does not move it: the next init.mp4 on the
// same rung keeps the running encoder.
func TestResumeHintAfterEncoderStartIsIgnored(t *testing.T) {
	t.Parallel()
	transcoder := resumeTranscoder(t)
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	key := SessionKey{SessionID: "session-started", MediaID: "media-started"}

	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatalf("init.mp4: %v", err)
	}
	sessions.NoteResumePosition(key, 41)
	sessions.mu.Lock()
	resume := sessions.sessions[key].resumeSeconds
	sessions.mu.Unlock()
	if resume != 0 {
		t.Fatalf("resumeSeconds = %d after the encoder started, want 0", resume)
	}
	if _, err := filePath720(sessions, key, "720p/init.mp4"); err != nil {
		t.Fatalf("second init.mp4: %v", err)
	}
	requireStarts(t, transcoder, 0)
}

// TestPlaybackInfoNotesTheResumePositionForTheHLSSession proves playback info
// that resumes the viewer and hands out an HLS stream records the resume
// position under the exact session key the stream's requests will use.
func TestPlaybackInfoNotesTheResumePositionForTheHLSSession(t *testing.T) {
	t.Parallel()
	service, st, scope := capabilityTestService(t)
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	mediaID := importProbedMedia(t, st, "fp-resume-hls", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "aac", 2),
	})
	if err := st.UpsertPlaybackProgress(context.Background(), store.PlaybackProgress{
		ProfileID: scope.ProfileID, MediaID: mediaID, PositionSeconds: 41, DurationSeconds: 120,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress: %v", err)
	}
	h264Only := CapabilityProfile{
		Client:      "web-h264",
		Containers:  []string{"mp4"},
		VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
		AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 2}},
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: h264Only})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if len(resp.Streams) == 0 || resp.Streams[0].Kind != "hls" {
		t.Fatalf("decision %q gave streams %+v, want an HLS stream", resp.Decision, resp.Streams)
	}
	grant, err := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() }).
		UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS grant: %v", err)
	}
	key := SessionKey{SessionID: scope.SessionID, MediaID: mediaID, ProfileCapability: grant.ProfileCapability}
	sessions.mu.Lock()
	hint, ok := sessions.resumeHints[key]
	sessions.mu.Unlock()
	if !ok || hint.seconds != 41 {
		t.Fatalf("resume hint for %+v = %+v (present %v), want 41s", key, hint, ok)
	}
}
