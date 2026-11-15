package graph

// Console server logs (CORE-1716). An admin reads the most recent server log
// lines without shell access to the host. The lines are redacted before they
// are kept (tokens, secrets, home-directory user names and IP addresses; see
// internal/redact), so nothing sensitive crosses the wire. The field is
// Admin-Session gated in execute().

// ServerLogSource is the in-memory log tail behind the console field.
type ServerLogSource interface {
	Lines(limit int) []string
	Capacity() int
}

// SetServerLogs wires the log tail the operator logger tees into.
func (h *Handler) SetServerLogs(source ServerLogSource) {
	h.serverLogSource = source
}

// ServerLogsInfo is the recent-lines read on the wire.
type ServerLogsInfo struct {
	// Available is false when this server keeps no log tail (an embedded
	// build or a test harness); Lines is then empty.
	Available bool `json:"available"`
	// Capacity is how many lines the server keeps.
	Capacity int `json:"capacity"`
	// Lines are the most recent redacted log lines, oldest first.
	Lines []string `json:"lines"`
}

// serverLogsMaxLimit caps one read at the default ring size.
const serverLogsMaxLimit = 2000

func (h *Handler) serverLogs(req request) ServerLogsInfo {
	if h.serverLogSource == nil {
		return ServerLogsInfo{Lines: []string{}}
	}
	limit := boundedIntVar(req, "limit", serverLogsMaxLimit, serverLogsMaxLimit)
	lines := h.serverLogSource.Lines(limit)
	if lines == nil {
		lines = []string{}
	}
	return ServerLogsInfo{Available: true, Capacity: h.serverLogSource.Capacity(), Lines: lines}
}
