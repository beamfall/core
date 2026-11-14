package store

import "testing"

func TestMediaDurationSummary(t *testing.T) {
	cases := []struct {
		seconds int
		want    string
	}{
		{-5, ""},
		{0, ""},
		{1, "1 sec"},
		{44, "44 sec"},
		{59, "59 sec"},
		{60, "1 min"},
		{61, "2 min"},
		{120, "2 min"},
		{5400, "90 min"},
	}
	for _, tc := range cases {
		if got := mediaDurationSummary(tc.seconds); got != tc.want {
			t.Errorf("mediaDurationSummary(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}
