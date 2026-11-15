package redact

import (
	"strings"
	"testing"
)

func TestLogLineRedactsSecretsPathsAndAddresses(t *testing.T) {
	cases := []struct {
		name, in   string
		want       []string
		wantAbsent []string
	}{
		{
			name:       "api key and bearer",
			in:         `level=WARN msg="auth failed" header="Bearer abc.def" key=bfk_live123`,
			want:       []string{Marker},
			wantAbsent: []string{"abc.def", "bfk_live123"},
		},
		{
			name:       "secret attribute values",
			in:         `level=INFO msg=configured api_key=s3cr3t client_secret="quoted value" password=hunter2 webhookToken=tok9`,
			want:       []string{"api_key=" + Marker, "client_secret=" + Marker, "password=" + Marker, "webhookToken=" + Marker},
			wantAbsent: []string{"s3cr3t", "quoted value", "hunter2", "tok9"},
		},
		{
			name:       "unix and windows home paths",
			in:         `msg=scan path=/Users/someone/Media/x.mkv other=/home/person/lib win="C:\\Users\\owner\\Videos"`,
			want:       []string{"/Users/" + UserMarker + "/Media/x.mkv", "/home/" + UserMarker + "/lib", UserMarker},
			wantAbsent: []string{"someone", "person", "owner"},
		},
		{
			name:       "ipv4 ipv6 and ports",
			in:         `msg=request remote=192.0.2.10:51234 peer=2001:db8::1 local=[::1]:8096 end=198.51.100.7.`,
			want:       []string{"remote=" + IPMarker, "peer=" + IPMarker, "local=" + IPMarker, "end=" + IPMarker + "."},
			wantAbsent: []string{"192.0.2.10", "2001:db8::1", "::1", "198.51.100.7"},
		},
		{
			name: "times and durations stay readable",
			in:   `time=2026-11-14T22:16:17.123Z level=INFO msg=done elapsed=1.5s ratio=0.75`,
			want: []string{"22:16:17.123Z", "elapsed=1.5s", "ratio=0.75"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LogLine(tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("LogLine(%q) = %q, want it to contain %q", tc.in, got, w)
				}
			}
			for _, a := range tc.wantAbsent {
				if strings.Contains(got, a) {
					t.Errorf("LogLine(%q) = %q, still contains %q", tc.in, got, a)
				}
			}
		})
	}
}

func TestSecretsKeepsPlainText(t *testing.T) {
	in := "scan finished for library 3"
	if got := Secrets(in); got != in {
		t.Fatalf("Secrets(%q) = %q", in, got)
	}
}
