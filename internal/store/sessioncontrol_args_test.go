package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func int64Ptr(v int64) *int64    { return &v }
func stringPtr(v string) *string { return &v }

// TestValidateSessionControlArgs pins the widened session-control vocabulary
// (CORE-1678): each verb takes exactly its own arguments, bounds are enforced,
// and the defaults (skip direction "next", subtitle track off) are applied.
func TestValidateSessionControlArgs(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    SessionControlArgs
		want    SessionControlArgs
		wantErr string
	}{
		{name: "pause", command: SessionCommandPause},
		{name: "resume", command: SessionCommandResume},
		{name: "stop", command: SessionCommandStop},
		{name: "mute", command: SessionCommandMute},
		{name: "unmute", command: SessionCommandUnmute},
		{name: "skip default next", command: SessionCommandSkipMedia, want: SessionControlArgs{Direction: SessionDirectionNext}},
		{name: "skip previous", command: SessionCommandSkipTrack, args: SessionControlArgs{Direction: "previous"}, want: SessionControlArgs{Direction: SessionDirectionPrevious}},
		{name: "skip bad direction", command: SessionCommandSkipTrack, args: SessionControlArgs{Direction: "sideways"}, wantErr: "direction must be next or previous"},
		{name: "seek", command: SessionCommandSeek, args: SessionControlArgs{PositionMs: int64Ptr(90000)}, want: SessionControlArgs{PositionMs: int64Ptr(90000)}},
		{name: "seek missing", command: SessionCommandSeek, wantErr: "seek requires positionMs"},
		{name: "seek negative", command: SessionCommandSeek, args: SessionControlArgs{PositionMs: int64Ptr(-1)}, wantErr: "positionMs"},
		{name: "seek beyond max", command: SessionCommandSeek, args: SessionControlArgs{PositionMs: int64Ptr(SessionControlMaxPositionMs + 1)}, wantErr: "positionMs"},
		{name: "volume", command: SessionCommandSetVolume, args: SessionControlArgs{Volume: intPtr(0)}, want: SessionControlArgs{Volume: intPtr(0)}},
		{name: "volume missing", command: SessionCommandSetVolume, wantErr: "volume"},
		{name: "volume high", command: SessionCommandSetVolume, args: SessionControlArgs{Volume: intPtr(101)}, wantErr: "volume"},
		{name: "audio track", command: SessionCommandSelectAudioTrack, args: SessionControlArgs{TrackID: stringPtr("a2")}, want: SessionControlArgs{TrackID: stringPtr("a2")}},
		{name: "audio track missing", command: SessionCommandSelectAudioTrack, wantErr: "trackID"},
		{name: "audio track blank", command: SessionCommandSelectAudioTrack, args: SessionControlArgs{TrackID: stringPtr("")}, wantErr: "trackID"},
		{name: "audio track too long", command: SessionCommandSelectAudioTrack, args: SessionControlArgs{TrackID: stringPtr(strings.Repeat("x", SessionControlMaxTrackIDLen+1))}, wantErr: "trackID"},
		{name: "subtitle off by default", command: SessionCommandSelectSubtitleTrack, want: SessionControlArgs{TrackID: stringPtr("")}},
		{name: "subtitle track", command: SessionCommandSelectSubtitleTrack, args: SessionControlArgs{TrackID: stringPtr("s1")}, want: SessionControlArgs{TrackID: stringPtr("s1")}},
		{name: "play media", command: SessionCommandPlayMedia, args: SessionControlArgs{MediaID: "media-alpha", PositionMs: int64Ptr(5000)}, want: SessionControlArgs{MediaID: "media-alpha", PositionMs: int64Ptr(5000)}},
		{name: "play media missing", command: SessionCommandPlayMedia, wantErr: "mediaID"},
		{name: "message", command: SessionCommandDisplayMessage, args: SessionControlArgs{Text: "Dinner is ready"}, want: SessionControlArgs{Text: "Dinner is ready"}},
		{name: "message blank", command: SessionCommandDisplayMessage, args: SessionControlArgs{Text: "  "}, wantErr: "text"},
		{name: "message control char", command: SessionCommandDisplayMessage, args: SessionControlArgs{Text: "hi\x07"}, wantErr: "text"},
		{name: "message too long", command: SessionCommandDisplayMessage, args: SessionControlArgs{Text: strings.Repeat("é", SessionControlMaxMessageRune+1)}, wantErr: "text"},
		{name: "message invalid utf8", command: SessionCommandDisplayMessage, args: SessionControlArgs{Text: "\xff"}, wantErr: "text"},
		{name: "pause rejects position", command: SessionCommandPause, args: SessionControlArgs{PositionMs: int64Ptr(1)}, wantErr: "does not take that argument"},
		{name: "seek rejects volume", command: SessionCommandSeek, args: SessionControlArgs{PositionMs: int64Ptr(1), Volume: intPtr(3)}, wantErr: "does not take that argument"},
		{name: "mute rejects media", command: SessionCommandMute, args: SessionControlArgs{MediaID: "media-alpha"}, wantErr: "does not take that argument"},
		{name: "stop rejects direction", command: SessionCommandStop, args: SessionControlArgs{Direction: "next"}, wantErr: "does not take that argument"},
		{name: "unknown verb", command: "teleport", wantErr: "unknown transport command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateSessionControlArgs(tc.command, tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("args = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// TestSessionControlCommandsSortedAndValid keeps the published vocabulary
// list and the membership check in step.
func TestSessionControlCommandsSortedAndValid(t *testing.T) {
	commands := SessionControlCommands()
	if len(commands) != 13 {
		t.Fatalf("vocabulary size = %d, want 13: %v", len(commands), commands)
	}
	for i, command := range commands {
		if !SessionControlCommandValid(command) {
			t.Errorf("%q listed but not valid", command)
		}
		if i > 0 && commands[i-1] >= command {
			t.Errorf("vocabulary not sorted at %d: %v", i, commands)
		}
	}
}

// TestNewSessionControlPayloadAnchorsPlayMedia proves a play-media command's
// reveal anchor is the started Media, so SSE reveal filtering applies to it.
func TestNewSessionControlPayloadAnchorsPlayMedia(t *testing.T) {
	args := SessionControlArgs{MediaID: "media-alpha"}
	if _, err := NewSessionControlPayload(SessionCommandPlayMedia, args, "device-a", "profile-default", "media-other"); err == nil {
		t.Fatal("play-media with a mismatched anchor was accepted")
	}
	p, err := NewSessionControlPayload(SessionCommandPlayMedia, args, "device-a", "profile-default", "media-alpha")
	if err != nil {
		t.Fatalf("NewSessionControlPayload: %v", err)
	}
	if p.MediaID != "media-alpha" || p.Command != SessionCommandPlayMedia || p.FromDevice != "device-a" {
		t.Fatalf("payload = %#v", p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "volume") || strings.Contains(string(raw), "trackID") {
		t.Fatalf("unset arguments serialized: %s", raw)
	}
}
