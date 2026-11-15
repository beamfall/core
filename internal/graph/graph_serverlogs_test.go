package graph

import (
	"encoding/json"
	"testing"

	"github.com/beamfall/core/internal/logtail"
	"github.com/beamfall/core/internal/store/storetest"
)

// serverLogs is Admin-Session gated, returns the newest redacted lines oldest
// first, honours limit, and reports a missing tail as unavailable rather than
// failing.
func TestServerLogsFieldIsAdminGatedAndRedacted(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	const (
		all     = `{"query":"query ServerLogs { serverLogs { available capacity lines } }"}`
		limited = `{"query":"query ServerLogs($limit: Int) { serverLogs(limit: $limit) { available lines } }","variables":{"limit":1}}`
	)
	if _, errs := postLibraryTrash(t, handler, all); len(errs) == 0 || errs[0] != "admin session required" {
		t.Fatalf("serverLogs without an Admin Session: errors = %q", errs)
	}
	openManagerAdminSession(t, st)

	var info ServerLogsInfo
	data, errs := postLibraryTrash(t, handler, all)
	if len(errs) != 0 || json.Unmarshal(data["serverLogs"], &info) != nil || info.Available || len(info.Lines) != 0 {
		t.Fatalf("serverLogs with no tail = %s, errors %q", data["serverLogs"], errs)
	}

	ring := logtail.NewRing(10)
	_, _ = ring.Write([]byte("level=INFO msg=started addr=192.0.2.4:8096\nlevel=WARN msg=denied token=abc123\n"))
	handler.SetServerLogs(ring)

	data, errs = postLibraryTrash(t, handler, all)
	if len(errs) != 0 || json.Unmarshal(data["serverLogs"], &info) != nil {
		t.Fatalf("serverLogs = %s, errors %q", data["serverLogs"], errs)
	}
	want := []string{"level=INFO msg=started addr=[IP]", "level=WARN msg=denied token=[REDACTED]"}
	if !info.Available || info.Capacity != 10 || len(info.Lines) != 2 || info.Lines[0] != want[0] || info.Lines[1] != want[1] {
		t.Fatalf("serverLogs = %+v, want lines %q", info, want)
	}

	data, errs = postLibraryTrash(t, handler, limited)
	info = ServerLogsInfo{}
	if len(errs) != 0 || json.Unmarshal(data["serverLogs"], &info) != nil || len(info.Lines) != 1 || info.Lines[0] != want[1] {
		t.Fatalf("serverLogs(limit: 1) = %s, errors %q", data["serverLogs"], errs)
	}
}
