package apptest

import (
	"net/http"
	"testing"
)

// Assert holds response assertions bound to a test. Each fails via t.Fatalf.
type Assert struct {
	t testing.TB
}

// Status fails unless resp.Status == want.
func (a *Assert) Status(resp *Response, want int) {
	a.t.Helper()
	if resp.Status != want {
		a.t.Fatalf("status = %d, want %d\n%s", resp.Status, want, resp.Body())
	}
}

// OK fails unless resp.Status == 200.
func (a *Assert) OK(resp *Response) {
	a.t.Helper()
	a.Status(resp, http.StatusOK)
}
