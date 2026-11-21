package scrape

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestIsTransient(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&statusError{host: "h", code: 429}, true},
		{&statusError{host: "h", code: 503}, true},
		{fmt.Errorf("lookup: %w", &statusError{host: "h", code: 500}), true},
		{&statusError{host: "h", code: 404}, false},
		{&statusError{host: "h", code: 401}, false},
		{errors.New("decode failed"), false},
		{fmt.Errorf("fetch: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}), true},
		{nil, false},
	} {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
